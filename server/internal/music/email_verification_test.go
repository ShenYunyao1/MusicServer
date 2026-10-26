package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func mailTestSetup(t *testing.T) (*API, authContext, *[]mailEnvelope) {
	t.Helper()
	api, _ := testAPI(t)
	api.store.CloseMail()
	user, session, _ := loginTestAccount(t, api.store, "admin")
	auth := authContext{session: true, userID: user.ID, keyID: session.ID, authVersion: user.AuthVersion, role: user.Role}
	_, err := api.store.UpdateMailSettings(MailSettingsInput{Host: "smtp.example.com", Port: 465, TLSMode: "tls", FromAddress: "sender@example.com", Username: "sender", Password: "private-smtp-password"}, MutationOptions{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	messages := []mailEnvelope{}
	api.store.mail.mu.Lock()
	api.store.mail.sendHook = func(_ context.Context, _ MailSettings, _ mailSecret, e mailEnvelope, _ string) error {
		messages = append(messages, e)
		return nil
	}
	api.store.mail.mu.Unlock()
	if _, err = api.store.QueueMailTest(auth, "test@example.com"); err != nil {
		t.Fatal(err)
	}
	if err = api.store.ProcessMailJobs(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	if !api.store.MailReady() {
		t.Fatal("SMTP test did not activate readiness")
	}
	return api, auth, &messages
}
func mailChallengeCode(t *testing.T, s *Store, owner string) string {
	t.Helper()
	var challenge emailChallenge
	if err := mailReadJSON(s.db, "email_challenge/"+owner, &challenge); err != nil {
		t.Fatal(err)
	}
	var job mailJob
	if err := mailReadJSON(s.db, "mail_job/"+challenge.JobID, &job); err != nil {
		t.Fatal(err)
	}
	raw, err := s.decryptMailPayload(job.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var envelope mailEnvelope
	if json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("bad encrypted envelope")
	}
	return envelope.Code
}
func TestEmailVerificationAtomicIdempotentAndQuotaPreserved(t *testing.T) {
	api, auth, messages := mailTestSetup(t)
	s := api.store
	if _, err := s.SetStorageQuota(auth.userID, 12345, 0, MutationOptions{UserID: auth.userID}); err != nil {
		t.Fatal(err)
	}
	before := s.Revision()
	status, err := s.RequestEmailChallenge(auth, "Person+test@EXAMPLE.COM", "", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingEmail != "Person+test@example.com" || status.ChallengeID == "" || status.EmailVerified {
		t.Fatal(status)
	}
	code := mailChallengeCode(t, s, auth.userID)
	var database strings.Builder
	rows, err := s.db.Query("SELECT value FROM meta WHERE key LIKE 'email_challenge/%' OR key LIKE 'mail_job/%'")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var value string
		_ = rows.Scan(&value)
		database.WriteString(value)
	}
	_ = rows.Close()
	if strings.Contains(database.String(), `"code":"`+code+`"`) || strings.Contains(database.String(), "private-smtp-password") {
		t.Fatal("plaintext mail secrets persisted in database")
	}
	result, err := s.VerifyEmailChallenge(auth, status.ChallengeID, code)
	if err != nil || !result.EmailVerified || result.Email != "Person+test@example.com" {
		t.Fatal(result, err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.VerifyEmailChallenge(auth, status.ChallengeID, code)
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var quota StorageQuota
	if err := readStorageJSON(s.db, "storage_quota/"+auth.userID, &quota); err != nil || quota.LimitBytes != 12345 {
		t.Fatal(quota, err)
	}
	var audits int
	_ = s.db.QueryRow("SELECT count(*) FROM audit WHERE action='email.verified'").Scan(&audits)
	if audits != 1 {
		t.Fatal("duplicate verification audit", audits)
	}
	if s.Revision() != before {
		t.Fatal("email verification must not reset music sync revision")
	}
	if err := s.ProcessMailJobs(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	if len(*messages) != 1 {
		t.Fatal("a consumed challenge must not send its old message")
	}
}
func TestEmailVerificationAttemptsExpirySessionsAndAddressIsolation(t *testing.T) {
	for _, scenario := range []string{"attempts", "expired", "revoked", "password", "wrong-account"} {
		t.Run(scenario, func(t *testing.T) {
			api, auth, _ := mailTestSetup(t)
			s := api.store
			status, err := s.RequestEmailChallenge(auth, "target@example.com", "", "local")
			if err != nil {
				t.Fatal(err)
			}
			code := mailChallengeCode(t, s, auth.userID)
			switch scenario {
			case "attempts":
				wrong := "000000"
				if code == wrong {
					wrong = "000001"
				}
				for i := 0; i < 5; i++ {
					if _, err = s.VerifyEmailChallenge(auth, status.ChallengeID, wrong); !errors.Is(err, errMailInvalid) {
						t.Fatal(err)
					}
				}
			case "expired":
				var ch emailChallenge
				_ = mailReadJSON(s.db, "email_challenge/"+auth.userID, &ch)
				ch.ExpiresAt = time.Now().Add(-time.Minute)
				_ = s.transaction(func(tx *sql.Tx) error { return mailWriteJSON(tx, "email_challenge/"+auth.userID, ch) })
			case "revoked":
				if err = s.RevokeSession(auth.userID, auth.keyID); err != nil {
					t.Fatal(err)
				}
			case "password":
				_, err = s.db.Exec("UPDATE users SET auth_version=auth_version+1 WHERE id=?", auth.userID)
				if err != nil {
					t.Fatal(err)
				}
			case "wrong-account":
				other, token := readyMember(t, api, "mail-other")
				_, session, ok := s.sessionIdentity(token)
				if !ok {
					t.Fatal("session unavailable")
				}
				auth = authContext{session: true, userID: other.ID, keyID: session.ID, authVersion: other.AuthVersion}
			}
			if _, err = s.VerifyEmailChallenge(auth, status.ChallengeID, code); err == nil {
				t.Fatal("invalid verification accepted")
			}
		})
	}
}
func TestEmailVerificationResendChangeAndUniqueBinding(t *testing.T) {
	api, auth, _ := mailTestSetup(t)
	s := api.store
	first, err := s.RequestEmailChallenge(auth, "first@example.com", "", "local")
	if err != nil {
		t.Fatal(err)
	}
	oldCode := mailChallengeCode(t, s, auth.userID)
	if _, err = s.RequestEmailChallenge(auth, "first@example.com", "", "local"); !errors.Is(err, errMailLimited) {
		t.Fatal("cooldown not applied", err)
	}
	var previous emailChallenge
	_ = mailReadJSON(s.db, "email_challenge/"+auth.userID, &previous)
	previous.CreatedAt = time.Now().Add(-2 * time.Minute)
	_ = s.transaction(func(tx *sql.Tx) error { return mailWriteJSON(tx, "email_challenge/"+auth.userID, previous) })
	next, err := s.RequestEmailChallenge(auth, "first@example.com", "", "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyEmailChallenge(auth, first.ChallengeID, oldCode); err == nil {
		t.Fatal("superseded challenge accepted")
	}
	if _, err = s.VerifyEmailChallenge(auth, next.ChallengeID, mailChallengeCode(t, s, auth.userID)); err != nil {
		t.Fatal(err)
	}
	previous = emailChallenge{}
	_ = mailReadJSON(s.db, "email_challenge/"+auth.userID, &previous)
	previous.CreatedAt = time.Now().Add(-2 * time.Minute)
	_ = s.transaction(func(tx *sql.Tx) error { return mailWriteJSON(tx, "email_challenge/"+auth.userID, previous) })
	if _, err = s.RequestEmailChallenge(auth, "new@example.com", "wrong-password", "local"); !errors.Is(err, errCredentials) {
		t.Fatal(err)
	}
	changed, err := s.RequestEmailChallenge(auth, "new@example.com", "test administrator password", "local")
	if err != nil {
		t.Fatal(err)
	}
	current, _ := s.EmailStatus(auth)
	if current.Email != "first@example.com" {
		t.Fatal("old email replaced before verification", current)
	}
	if _, err = s.VerifyEmailChallenge(auth, changed.ChallengeID, mailChallengeCode(t, s, auth.userID)); err != nil {
		t.Fatal(err)
	}
	other, token := readyMember(t, api, "mail-second")
	_, session, _ := s.sessionIdentity(token)
	second := authContext{session: true, userID: other.ID, keyID: session.ID, authVersion: other.AuthVersion}
	if _, err = s.RequestEmailChallenge(second, "new@example.com", "", "different-ip"); !errors.Is(err, errMailAddressTaken) {
		t.Fatal("duplicate binding requested", err)
	}
	if _, err = s.RequestEmailChallenge(second, "first@example.com", "", "different-ip"); err != nil {
		t.Fatal("old email not released on successful change", err)
	}
}
func TestMailSecretsAreConfinedAndMissingSecretsDisableReadiness(t *testing.T) {
	api, _, _ := mailTestSetup(t)
	s := api.store
	raw, err := os.ReadFile(filepath.Join(s.mailSecretsDirectory(), "master.key"))
	if err != nil || len(raw) == 0 {
		t.Fatal(err)
	}
	record, err := readMailSettings(s.db)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(s.mailSecretsDirectory(), record.SecretID+".json")); err != nil {
		t.Fatal(err)
	}
	if s.MailReady() {
		t.Fatal("missing credentials silently retained readiness")
	}
	state, err := s.MailSettings()
	if err != nil || state.Tested || state.Configured {
		t.Fatal(state, err)
	}
}
