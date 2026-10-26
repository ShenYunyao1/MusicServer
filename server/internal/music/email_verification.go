package music

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
)

type emailChallenge struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Email       string    `json:"email"`
	Digest      string    `json:"digest"`
	AuthVersion int64     `json:"auth_version"`
	Attempts    int       `json:"attempts"`
	Consumed    bool      `json:"consumed"`
	JobID       string    `json:"job_id"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}
type EmailStatus struct {
	Email                     string `json:"email"`
	EmailVerified             bool   `json:"email_verified"`
	EmailVerificationRequired bool   `json:"email_verification_required"`
	UploadAllowed             bool   `json:"upload_allowed"`
	PendingEmail              string `json:"pending_email,omitempty"`
	ChallengeID               string `json:"challenge_id,omitempty"`
	JobID                     string `json:"job_id,omitempty"`
	ExpiresAt                 string `json:"expires_at,omitempty"`
	RetryAfter                int    `json:"retry_after"`
	DeliveryStatus            string `json:"delivery_status,omitempty"`
}
type mailLimit struct {
	Count     int       `json:"count"`
	ExpiresAt time.Time `json:"expires_at"`
}

func reserveMailLimits(tx *sql.Tx, owner, email, ip string, test bool) error {
	now := time.Now().UTC()
	window := now.Truncate(time.Hour)
	suffix := window.Format("2006010215")
	scopes := []struct {
		key string
		max int
	}{{"global", 200}, {"account/" + hashSecret(owner), 10}, {"email/" + hashSecret(email), 10}, {"ip/" + hashSecret(ip), 30}}
	if test {
		scopes[1].max = 5
	}
	for _, scope := range scopes {
		key := "mail_limit/" + suffix + "/" + scope.key
		var entry mailLimit
		err := mailReadJSON(tx, key, &entry)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if entry.Count >= scope.max {
			return errMailLimited
		}
	}
	for _, scope := range scopes {
		key := "mail_limit/" + suffix + "/" + scope.key
		var entry mailLimit
		_ = mailReadJSON(tx, key, &entry)
		entry.Count++
		entry.ExpiresAt = window.Add(time.Hour)
		if err := mailWriteJSON(tx, key, entry); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) emailCodeDigest(challenge emailChallenge, code string) (string, error) {
	master, err := s.mailMasterKey(false)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, mailDerivedKey(master, "challenge"))
	_, _ = mac.Write([]byte(challenge.UserID + "\x00" + challenge.ID + "\x00" + challenge.Email + "\x00bind\x00" + code))
	return hex.EncodeToString(mac.Sum(nil)), nil
}
func emailStatusFrom(reader sqlReader, auth authContext) (EmailStatus, error) {
	var result EmailStatus
	if err := validateAccountSession(reader, auth); err != nil {
		return result, err
	}
	user, err := readUser(reader, auth.userID)
	if err != nil {
		return result, err
	}
	account, err := readAccountEmail(reader, user.ID)
	if err != nil {
		return result, err
	}
	result = EmailStatus{Email: account.Email, EmailVerified: accountEmailVerified(account), EmailVerificationRequired: account.Required, UploadAllowed: !account.Required || accountEmailVerified(account)}
	var challenge emailChallenge
	err = mailReadJSON(reader, "email_challenge/"+auth.userID, &challenge)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if err == nil && !challenge.Consumed && challenge.AuthVersion == user.AuthVersion && time.Now().Before(challenge.ExpiresAt) {
		result.PendingEmail = challenge.Email
		result.ChallengeID = challenge.ID
		result.JobID = challenge.JobID
		result.ExpiresAt = persistedTime(challenge.ExpiresAt)
		result.RetryAfter = max(0, int(time.Until(challenge.CreatedAt.Add(time.Minute)).Seconds())+1)
		if challenge.Attempts >= 5 {
			result.DeliveryStatus = "locked"
		} else {
			var job mailJob
			if mailReadJSON(reader, "mail_job/"+challenge.JobID, &job) == nil {
				result.DeliveryStatus = job.State
			}
		}
	}
	return result, nil
}
func (s *Store) EmailStatus(auth authContext) (EmailStatus, error) {
	return emailStatusFrom(s.db, auth)
}
func (s *Store) RequestEmailChallenge(auth authContext, target, currentPassword, ip string) (EmailStatus, error) {
	target, err := normalizeMailAddress(target)
	if err != nil {
		return EmailStatus{}, err
	}
	if len(currentPassword) > 1024 {
		return EmailStatus{}, errCredentials
	}
	var initial AccountEmailState
	// Count attempts before expensive password verification. Even failures spend
	// the per-account/address/IP budget and cannot be used as a hash oracle.
	err = s.transaction(func(tx *sql.Tx) error {
		if err := validateAccountSession(tx, auth); err != nil {
			return err
		}
		if !s.mailReadyFrom(tx) {
			return errMailUnavailable
		}
		var err error
		initial, err = readAccountEmail(tx, auth.userID)
		if err != nil {
			return err
		}
		var previous emailChallenge
		if mailReadJSON(tx, "email_challenge/"+auth.userID, &previous) == nil && time.Now().Before(previous.CreatedAt.Add(time.Minute)) {
			return errMailLimited
		}
		return reserveMailLimits(tx, auth.userID, target, ip, false)
	})
	if err != nil {
		return EmailStatus{}, err
	}
	if initial.Email != "" {
		var encoded string
		if s.db.QueryRow("SELECT password_hash FROM users WHERE id=?", auth.userID).Scan(&encoded) != nil || !verifyPassword(encoded, currentPassword) {
			return EmailStatus{}, errCredentials
		}
	}
	var challenge emailChallenge
	var job mailJob
	err = s.transaction(func(tx *sql.Tx) error {
		if err := validateAccountSession(tx, auth); err != nil {
			return err
		}
		current, err := readAccountEmail(tx, auth.userID)
		if err != nil {
			return err
		}
		if current.Email != initial.Email || current.VerifiedAt != initial.VerifiedAt {
			return errVersionConflict
		}
		settings, err := readMailSettings(tx)
		if err != nil {
			return err
		}
		if !s.mailReadyFrom(tx) {
			return errMailUnavailable
		}
		var owner string
		err = tx.QueryRow("SELECT value FROM meta WHERE key=?", "email_binding/"+hashSecret(target)).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && owner != auth.userID {
			return errMailAddressTaken
		}
		var previous emailChallenge
		if mailReadJSON(tx, "email_challenge/"+auth.userID, &previous) == nil && time.Now().Before(previous.CreatedAt.Add(time.Minute)) {
			return errMailLimited
		}
		number, err := rand.Int(rand.Reader, big.NewInt(1000000))
		if err != nil {
			return err
		}
		code := fmt.Sprintf("%06d", number.Int64())
		now := time.Now().UTC()
		challenge = emailChallenge{ID: uuid.NewString(), UserID: auth.userID, Email: target, AuthVersion: auth.authVersion, CreatedAt: now, ExpiresAt: now.Add(mailChallengeTTL)}
		challenge.Digest, err = s.emailCodeDigest(challenge, code)
		if err != nil {
			return err
		}
		job, err = s.enqueueMail(tx, auth.userID, challenge.ID, mailEnvelope{To: target, Code: code}, settings)
		if err != nil {
			return err
		}
		challenge.JobID = job.ID
		if previous.JobID != "" {
			var old mailJob
			if mailReadJSON(tx, "mail_job/"+previous.JobID, &old) == nil && old.State == "queued" {
				old.State = "failed"
				old.Reason = "MAIL_SUPERSEDED"
				old.Payload = ""
				if err = mailWriteJSON(tx, "mail_job/"+old.ID, old); err != nil {
					return err
				}
			}
		}
		if err = mailWriteJSON(tx, "email_challenge/"+auth.userID, challenge); err != nil {
			return err
		}
		return auditMutation(tx, "email.challenge", challenge.ID, nil, map[string]any{"queued": true}, []MutationOptions{{UserID: auth.userID, CredentialID: auth.keyID, ActorType: "session"}})
	})
	if err != nil {
		return EmailStatus{}, err
	}
	s.emitMail("mail.queued", job, "")
	s.wakeMail()
	return s.EmailStatus(auth)
}
func (s *Store) VerifyEmailChallenge(auth authContext, id, code string) (EmailStatus, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id || len(code) != 6 {
		return EmailStatus{}, errMailInvalid
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return EmailStatus{}, errMailInvalid
		}
	}
	invalid := false
	var verifiedJobID string
	err = s.transaction(func(tx *sql.Tx) error {
		if err := validateAccountSession(tx, auth); err != nil {
			return err
		}
		var challenge emailChallenge
		if err := mailReadJSON(tx, "email_challenge/"+auth.userID, &challenge); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				invalid = true
				return nil
			}
			return err
		}
		if challenge.ID != id || challenge.UserID != auth.userID || challenge.AuthVersion != auth.authVersion || !time.Now().Before(challenge.ExpiresAt) || challenge.Attempts >= 5 {
			invalid = true
			return nil
		}
		verifiedJobID = challenge.JobID
		digest, err := s.emailCodeDigest(challenge, code)
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare([]byte(challenge.Digest), []byte(digest)) != 1 {
			challenge.Attempts++
			invalid = true
			return mailWriteJSON(tx, "email_challenge/"+auth.userID, challenge)
		}
		account, err := readAccountEmail(tx, auth.userID)
		if err != nil {
			return err
		}
		if challenge.Consumed {
			if account.Email != challenge.Email || !accountEmailVerified(account) {
				invalid = true
			}
			return nil
		}
		key := "email_binding/" + hashSecret(challenge.Email)
		var owner string
		err = tx.QueryRow("SELECT value FROM meta WHERE key=?", key).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && owner != auth.userID {
			return errMailAddressTaken
		}
		if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, auth.userID); err != nil {
			return err
		}
		if account.Email != "" && account.Email != challenge.Email {
			if _, err = tx.Exec("DELETE FROM meta WHERE key=? AND value=?", "email_binding/"+hashSecret(account.Email), auth.userID); err != nil {
				return err
			}
		}
		account.Email = challenge.Email
		account.VerifiedAt = persistedTime(time.Now())
		if err = writeAccountEmail(tx, auth.userID, account); err != nil {
			return err
		}
		challenge.Consumed = true
		if err = mailWriteJSON(tx, "email_challenge/"+auth.userID, challenge); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE users SET version=version+1,updated_at=? WHERE id=?", persistedTime(time.Now()), auth.userID); err != nil {
			return err
		}
		return auditMutation(tx, "email.verified", challenge.ID, nil, map[string]any{"verified": true}, []MutationOptions{{UserID: auth.userID, CredentialID: auth.keyID, ActorType: "session"}})
	})
	if err != nil {
		return EmailStatus{}, err
	}
	if invalid {
		return EmailStatus{}, errMailInvalid
	}
	s.emitMail("mail.verified", mailJob{ID: verifiedJobID, UserID: auth.userID}, "")
	return s.EmailStatus(auth)
}
func (s *Store) ReleaseDeletedEmail(auth authContext, id string, expected int64) error {
	return s.transaction(func(tx *sql.Tx) error {
		if err := validateAccountSession(tx, auth); err != nil {
			return err
		}
		if err := requireAdminActor(tx, []MutationOptions{{UserID: auth.userID}}); err != nil {
			return err
		}
		user, err := readUser(tx, id)
		if err != nil {
			return err
		}
		if user.DeletedAt == "" {
			return errors.New("只能释放已删除账户的邮箱占用")
		}
		state, err := readAccountEmail(tx, id)
		if err != nil {
			return err
		}
		if state.Email == "" {
			return nil
		}
		if user.Version != expected {
			return errVersionConflict
		}
		if _, err = tx.Exec("DELETE FROM meta WHERE key=? AND value=?", "email_binding/"+hashSecret(state.Email), id); err != nil {
			return err
		}
		state.Email, state.VerifiedAt = "", ""
		if err = writeAccountEmail(tx, id, state); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE users SET version=version+1,updated_at=? WHERE id=?", persistedTime(time.Now()), id); err != nil {
			return err
		}
		return auditMutation(tx, "email.released", id, nil, map[string]any{"released": true}, []MutationOptions{{UserID: auth.userID, CredentialID: auth.keyID, ActorType: "session"}})
	})
}
func mailMaskedAddress(address string) string {
	local, domain, _ := strings.Cut(address, "@")
	if local == "" {
		return ""
	}
	return local[:1] + "***@" + domain
}
