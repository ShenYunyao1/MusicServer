package music

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"musicserver/internal/activity"
)

type mailRuntime struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	wake    chan struct{}
	started bool
	// Test injection stays private and is never exposed through configuration.
	sendHook func(context.Context, MailSettings, mailSecret, mailEnvelope, string) error
	roots    *x509.CertPool
}
type mailJob struct {
	ID            string    `json:"id"`
	UserID        string    `json:"user_id"`
	ChallengeID   string    `json:"challenge_id,omitempty"`
	State         string    `json:"state"`
	Payload       string    `json:"payload,omitempty"`
	ConfigVersion int64     `json:"config_version"`
	Test          bool      `json:"test"`
	Attempts      int       `json:"attempts"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	LeaseUntil    time.Time `json:"lease_until"`
	Reason        string    `json:"reason,omitempty"`
}

func (s *Store) InitMail() error {
	s.mail.mu.Lock()
	defer s.mail.mu.Unlock()
	if s.mail.started {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mail.cancel = cancel
	s.mail.wake = make(chan struct{}, 1)
	s.mail.started = true
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			case <-s.mail.wake:
			}
			_ = s.ProcessMailJobs(ctx, 4)
		}
	}()
	return nil
}
func (s *Store) StartMail() { _ = s.InitMail() }
func (s *Store) CloseMail() {
	s.mail.mu.Lock()
	defer s.mail.mu.Unlock()
	if s.mail.cancel != nil {
		s.mail.cancel()
	}
}
func (s *Store) wakeMail() {
	s.mail.mu.Lock()
	wake := s.mail.wake
	s.mail.mu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}
func (s *Store) emitMail(event string, job mailJob, reason string) {
	_ = s.activity.Emit(event, activity.Fields{UserID: job.UserID, RequestID: job.ID, Reason: reason})
}
func mailCleanup(tx *sql.Tx, now time.Time) error {
	// Terminal jobs retain only status for one day; all live encrypted bodies
	// have a short expiry and are erased after delivery, supersession or expiry.
	if _, err := tx.Exec("DELETE FROM meta WHERE key LIKE 'mail_job/%' AND julianday(json_extract(value,'$.expires_at'))<julianday(?)", persistedTime(now.Add(-24*time.Hour))); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM meta WHERE key LIKE 'mail_limit/%' AND julianday(json_extract(value,'$.expires_at'))<julianday(?)", persistedTime(now)); err != nil {
		return err
	}
	_, err := tx.Exec("DELETE FROM meta WHERE key LIKE 'email_challenge/%' AND julianday(json_extract(value,'$.expires_at'))<julianday(?)", persistedTime(now.Add(-24*time.Hour)))
	return err
}
func (s *Store) enqueueMail(tx *sql.Tx, userID, challengeID string, envelope mailEnvelope, settings mailSettingsRecord) (mailJob, error) {
	var job mailJob
	var pending int
	if err := mailCleanup(tx, time.Now()); err != nil {
		return job, err
	}
	if err := tx.QueryRow("SELECT count(*) FROM meta WHERE key LIKE 'mail_job/%' AND json_extract(value,'$.state') IN ('queued','sending')").Scan(&pending); err != nil {
		return job, err
	}
	if pending >= 128 {
		return job, errMailLimited
	}
	raw, _ := json.Marshal(envelope)
	encrypted, err := s.encryptMailPayload(raw)
	if err != nil {
		return job, err
	}
	id, err := randomToken(8)
	if err != nil {
		return job, err
	}
	now := time.Now().UTC()
	job = mailJob{ID: id, UserID: userID, ChallengeID: challengeID, State: "queued", Payload: encrypted, ConfigVersion: settings.Version, Test: envelope.Test, CreatedAt: now, ExpiresAt: now.Add(mailChallengeTTL), NextAttemptAt: now}
	return job, mailWriteJSON(tx, "mail_job/"+job.ID, job)
}
func (s *Store) QueueMailTest(auth authContext, target string) (mailJob, error) {
	normalized, err := normalizeMailAddress(target)
	if err != nil {
		return mailJob{}, err
	}
	var job mailJob
	err = s.transaction(func(tx *sql.Tx) error {
		if err := validateAccountSession(tx, auth); err != nil {
			return err
		}
		if err := requireAdminActor(tx, []MutationOptions{{UserID: auth.userID}}); err != nil {
			return err
		}
		settings, err := readMailSettings(tx)
		if err != nil {
			return err
		}
		if !settings.Configured {
			return errMailUnavailable
		}
		if _, err = s.mailCredential(settings); err != nil {
			return err
		}
		if err = reserveMailLimits(tx, auth.userID, normalized, "admin-test", true); err != nil {
			return err
		}
		job, err = s.enqueueMail(tx, auth.userID, "", mailEnvelope{To: normalized, Test: true}, settings)
		if err != nil {
			return err
		}
		return auditMutation(tx, "mail.test_queued", job.ID, nil, map[string]any{"version": settings.Version}, []MutationOptions{{UserID: auth.userID, CredentialID: auth.keyID, ActorType: "session"}})
	})
	if err == nil {
		s.emitMail("mail.queued", job, "")
		s.wakeMail()
	}
	return job, err
}
func (s *Store) claimMailJob() (mailJob, bool, error) {
	var job mailJob
	found := false
	err := s.transaction(func(tx *sql.Tx) error {
		now := time.Now().UTC()
		if err := mailCleanup(tx, now); err != nil {
			return err
		}
		var raw string
		err := tx.QueryRow("SELECT value FROM meta WHERE key LIKE 'mail_job/%' AND ((json_extract(value,'$.state')='queued' AND julianday(json_extract(value,'$.next_attempt_at'))<=julianday(?)) OR (json_extract(value,'$.state')='sending' AND julianday(json_extract(value,'$.lease_until'))<=julianday(?))) ORDER BY json_extract(value,'$.created_at'),key LIMIT 1", persistedTime(now), persistedTime(now)).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(raw), &job); err != nil {
			return err
		}
		found = true
		job.State = "sending"
		job.LeaseUntil = now.Add(time.Minute)
		job.Attempts++
		return mailWriteJSON(tx, "mail_job/"+job.ID, job)
	})
	return job, found, err
}
func (s *Store) ProcessMailJobs(ctx context.Context, limit int) error {
	limit = min(max(limit, 1), 4)
	for i := 0; i < limit; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		job, found, err := s.claimMailJob()
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		reason := ""
		var config mailSettingsRecord
		var envelope mailEnvelope
		var secret mailSecret
		err = s.transaction(func(tx *sql.Tx) error {
			if !time.Now().Before(job.ExpiresAt) {
				reason = "MAIL_EXPIRED"
				return nil
			}
			user, err := readUser(tx, job.UserID)
			if err != nil || !user.Enabled || user.DeletedAt != "" || user.MustChangePassword {
				reason = "MAIL_ACCOUNT_UNAVAILABLE"
				return nil
			}
			if !job.Test {
				var challenge emailChallenge
				if mailReadJSON(tx, "email_challenge/"+job.UserID, &challenge) != nil || challenge.ID != job.ChallengeID || challenge.Consumed || challenge.Attempts >= 5 || !time.Now().Before(challenge.ExpiresAt) {
					reason = "MAIL_SUPERSEDED"
					return nil
				}
			}
			config, err = readMailSettings(tx)
			if err != nil {
				return err
			}
			if !config.Configured || config.Version != job.ConfigVersion {
				reason = "MAIL_CONFIG_CHANGED"
				return nil
			}
			secret, err = s.mailCredential(config)
			if err != nil {
				reason = "MAIL_UNAVAILABLE"
				return nil
			}
			raw, err := s.decryptMailPayload(job.Payload)
			if err != nil || json.Unmarshal(raw, &envelope) != nil {
				reason = "MAIL_UNAVAILABLE"
				return nil
			}
			return nil
		})
		if err != nil {
			reason = "MAIL_UNAVAILABLE"
		}
		if reason == "" {
			s.mail.mu.Lock()
			hook, roots := s.mail.sendHook, s.mail.roots
			s.mail.mu.Unlock()
			sendCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			if hook != nil {
				err = hook(sendCtx, config.MailSettings, secret, envelope, job.ID)
			} else {
				err = sendSMTP(sendCtx, config.MailSettings, secret, envelope, job.ID, roots)
			}
			cancel()
			reason = mailFailureCode(err)
		}
		if err = s.completeMailJob(job, reason); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) completeMailJob(job mailJob, reason string) error {
	submitted := reason == ""
	err := s.transaction(func(tx *sql.Tx) error {
		var current mailJob
		if err := mailReadJSON(tx, "mail_job/"+job.ID, &current); err != nil {
			return err
		}
		if current.State != "sending" || current.Attempts != job.Attempts {
			return nil
		}
		current.Reason = reason
		if submitted {
			current.State = "submitted"
			current.Payload = ""
		} else if current.Attempts < 3 && time.Now().Before(current.ExpiresAt) && !stringsMailTerminal(reason) {
			current.State = "queued"
			current.NextAttemptAt = time.Now().UTC().Add(time.Duration(1<<current.Attempts) * 5 * time.Second)
		} else {
			current.State = "failed"
			current.Payload = ""
		}
		if err := mailWriteJSON(tx, "mail_job/"+current.ID, current); err != nil {
			return err
		}
		if job.Test && submitted {
			config, err := readMailSettings(tx)
			if err != nil {
				return err
			}
			if config.Version == job.ConfigVersion {
				config.Tested = true
				raw, _ := json.Marshal(config)
				if _, err = tx.Exec("UPDATE settings SET value=? WHERE key='mail_settings'", string(raw)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err == nil {
		event := "mail.failed"
		if submitted {
			event = "mail.submitted"
			if job.Test {
				event = "mail.tested"
			}
		}
		s.emitMail(event, job, reason)
	}
	return err
}
func stringsMailTerminal(reason string) bool {
	switch reason {
	case "MAIL_EXPIRED", "MAIL_ACCOUNT_UNAVAILABLE", "MAIL_SUPERSEDED", "MAIL_CONFIG_CHANGED", "MAIL_UNAVAILABLE":
		return true
	}
	return false
}
