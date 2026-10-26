package music

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMailOutboxEncryptedRestartRetryAndConfigVersion(t *testing.T) {
	api, auth, _ := mailTestSetup(t)
	s := api.store
	status, err := s.RequestEmailChallenge(auth, "restart@example.com", "", "local")
	if err != nil {
		t.Fatal(err)
	}
	code := mailChallengeCode(t, s, auth.userID)
	var challenge emailChallenge
	_ = mailReadJSON(s.db, "email_challenge/"+auth.userID, &challenge)
	var job mailJob
	_ = mailReadJSON(s.db, "mail_job/"+challenge.JobID, &job)
	if strings.Contains(job.Payload, code) {
		t.Fatal("outbox plaintext code")
	}
	dataPath, mediaRoot := s.dataPath, s.mediaRoot
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewStore(dataPath, mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	restarted.CloseMail()
	t.Cleanup(func() { _ = restarted.Close() })
	calls := 0
	var delivered mailEnvelope
	restarted.mail.sendHook = func(_ context.Context, _ MailSettings, _ mailSecret, envelope mailEnvelope, _ string) error {
		calls++
		delivered = envelope
		if calls == 1 {
			return errors.New("SMTP_CONNECT")
		}
		return nil
	}
	if err = restarted.ProcessMailJobs(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	job = mailJob{}
	_ = mailReadJSON(restarted.db, "mail_job/"+challenge.JobID, &job)
	if job.State != "queued" || job.Attempts != 1 || job.Payload == "" {
		t.Fatal(job)
	}
	job.NextAttemptAt = time.Now().Add(-time.Second)
	if err = restarted.transaction(func(tx *sql.Tx) error { return mailWriteJSON(tx, "mail_job/"+job.ID, job) }); err != nil {
		t.Fatal(err)
	}
	if err = restarted.ProcessMailJobs(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	job = mailJob{}
	_ = mailReadJSON(restarted.db, "mail_job/"+challenge.JobID, &job)
	if calls != 2 || delivered.Code != code || job.State != "submitted" || job.Payload != "" {
		t.Fatal(calls, job)
	}
	if _, err = restarted.VerifyEmailChallenge(auth, status.ChallengeID, code); err != nil {
		t.Fatal(err)
	}
	settings, _ := restarted.MailSettings()
	if _, err = restarted.UpdateMailSettings(MailSettingsInput{Host: settings.Host, Port: settings.Port, TLSMode: settings.TLSMode, FromAddress: settings.FromAddress, Username: settings.Username, Version: settings.Version}, MutationOptions{UserID: auth.userID}); err != nil {
		t.Fatal(err)
	}
	if restarted.MailReady() {
		t.Fatal("edited configuration retained test success")
	}
}
func TestMailOutboxRetriesAreBoundedAndPayloadIsErased(t *testing.T) {
	api, auth, _ := mailTestSetup(t)
	s := api.store
	if _, err := s.RequestEmailChallenge(auth, "failed@example.com", "", "local"); err != nil {
		t.Fatal(err)
	}
	var ch emailChallenge
	_ = mailReadJSON(s.db, "email_challenge/"+auth.userID, &ch)
	s.mail.sendHook = func(context.Context, MailSettings, mailSecret, mailEnvelope, string) error {
		return errors.New("SMTP_CONNECT")
	}
	for i := 0; i < 4; i++ {
		var job mailJob
		_ = mailReadJSON(s.db, "mail_job/"+ch.JobID, &job)
		job.NextAttemptAt = time.Now().Add(-time.Second)
		_ = s.transaction(func(tx *sql.Tx) error { return mailWriteJSON(tx, "mail_job/"+job.ID, job) })
		if err := s.ProcessMailJobs(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
	}
	var job mailJob
	_ = mailReadJSON(s.db, "mail_job/"+ch.JobID, &job)
	if job.State != "failed" || job.Attempts != 3 || job.Payload != "" {
		t.Fatal(job)
	}
}
func TestMailLimitsRollbackAndExpiredLeases(t *testing.T) {
	api, auth, _ := mailTestSetup(t)
	s := api.store
	for i := 0; i < 10; i++ {
		err := s.transaction(func(tx *sql.Tx) error {
			return reserveMailLimits(tx, "other-owner", "target@example.com", "local", false)
		})
		if err != nil {
			t.Fatal(i, err)
		}
	}
	if err := s.transaction(func(tx *sql.Tx) error {
		return reserveMailLimits(tx, "other-owner", "target@example.com", "local", false)
	}); !errors.Is(err, errMailLimited) {
		t.Fatal(err)
	}
	if _, err := s.RequestEmailChallenge(auth, "lease@example.com", "", "different"); err != nil {
		t.Fatal(err)
	}
	job, found, err := s.claimMailJob()
	if err != nil || !found {
		t.Fatal(job, found, err)
	}
	called := 0
	s.mail.sendHook = func(context.Context, MailSettings, mailSecret, mailEnvelope, string) error { called++; return nil }
	if err = s.ProcessMailJobs(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatal("active lease reused")
	}
	job.LeaseUntil = time.Now().Add(-time.Second)
	_ = s.transaction(func(tx *sql.Tx) error { return mailWriteJSON(tx, "mail_job/"+job.ID, job) })
	if err = s.ProcessMailJobs(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatal("expired lease not recovered")
	}
}
