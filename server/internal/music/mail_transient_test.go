package music

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMailTemporaryCredentialFailureRetriesWithoutLosingPayload(t *testing.T) {
	a, auth, messages := mailTestSetup(t)
	s := a.store
	if _, err := s.RequestEmailChallenge(auth, "retry-credential@example.com", "", "local"); err != nil {
		t.Fatal(err)
	}
	var challenge emailChallenge
	if err := mailReadJSON(s.db, "email_challenge/"+auth.userID, &challenge); err != nil {
		t.Fatal(err)
	}
	settings, err := readMailSettings(s.db)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.mailSecretsDirectory(), settings.SecretID+".json")
	if err := os.Rename(path, path+".temporarily-unavailable"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(path+".temporarily-unavailable", path)
	before := len(*messages)
	if err := s.ProcessMailJobs(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	var job mailJob
	if err := mailReadJSON(s.db, "mail_job/"+challenge.JobID, &job); err != nil {
		t.Fatal(err)
	}
	if job.State != "queued" || job.Payload == "" || job.Attempts != 1 || !job.NextAttemptAt.After(time.Now()) || len(*messages) != before {
		t.Fatal("temporary credential read discarded job", job.State, job.Reason, job.Attempts)
	}
	if err := os.Rename(path+".temporarily-unavailable", path); err != nil {
		t.Fatal(err)
	}
	job.NextAttemptAt = time.Now().Add(-time.Second)
	if err := s.transaction(func(tx *sql.Tx) error { return mailWriteJSON(tx, "mail_job/"+job.ID, job) }); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessMailJobs(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	job = mailJob{}
	if err := mailReadJSON(s.db, "mail_job/"+challenge.JobID, &job); err != nil {
		t.Fatal(err)
	}
	if job.State != "submitted" || job.Payload != "" || len(*messages) != before+1 {
		t.Fatal("retry did not submit original job", job.State, job.Reason)
	}
}

func TestMailCredentialReadsOutsideWriteTransaction(t *testing.T) {
	a, auth, _ := mailTestSetup(t)
	s := a.store
	reads := 0
	s.mail.secretReadHook = func() {
		reads++
		if !s.writeMu.TryLock() {
			t.Error("credential file read holds database write mutex")
		} else {
			s.writeMu.Unlock()
		}
	}
	defer func() { s.mail.secretReadHook = nil }()
	if _, err := s.RequestEmailChallenge(auth, "outside-lock@example.com", "", "local"); err != nil {
		t.Fatal(err)
	}
	code := mailChallengeCode(t, s, auth.userID)
	var challenge emailChallenge
	if err := mailReadJSON(s.db, "email_challenge/"+auth.userID, &challenge); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessMailJobs(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyEmailChallenge(auth, challenge.ID, code); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueueMailTest(auth, "outside-test@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessMailJobs(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if reads == 0 {
		t.Fatal("credential reads were not exercised")
	}
	admin, err := readUser(s.db, auth.userID)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.RegistrationPolicy()
	if err != nil {
		t.Fatal(err)
	}
	setRegistration(t, s, admin, true, 0, policy.Version)
	if _, err := s.RegisterAccount("outside-lock-member", "isolated registration password", "local"); err != nil {
		t.Fatal(err)
	}
	ready := s.preparedMailReady()
	if _, err := s.db.Exec("UPDATE settings SET value=json_set(value,'$.version',json_extract(value,'$.version')+1) WHERE key='mail_settings'"); err != nil {
		t.Fatal(err)
	}
	if err := s.transaction(func(tx *sql.Tx) error {
		if ready(tx) {
			t.Error("old credential evidence accepted new settings")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMailValidationQueryFailureRetriesAndStopsAfterThreeAttempts(t *testing.T) {
	a, auth, messages := mailTestSetup(t)
	s := a.store
	if _, err := s.RequestEmailChallenge(auth, "retry-database@example.com", "", "local"); err != nil {
		t.Fatal(err)
	}
	var challenge emailChallenge
	if err := mailReadJSON(s.db, "email_challenge/"+auth.userID, &challenge); err != nil {
		t.Fatal(err)
	}
	// Claim/complete use meta; removing this table only from the query name
	// deterministically fails validation without fabricating an absent account.
	if _, err := s.db.Exec("ALTER TABLE users RENAME TO temporarily_unreadable_users"); err != nil {
		t.Fatal(err)
	}
	defer s.db.Exec("ALTER TABLE temporarily_unreadable_users RENAME TO users")
	before := len(*messages)
	for attempt := 1; attempt <= 3; attempt++ {
		if err := s.ProcessMailJobs(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		var job mailJob
		if err := mailReadJSON(s.db, "mail_job/"+challenge.JobID, &job); err != nil {
			t.Fatal(err)
		}
		if job.Attempts != attempt || job.Reason != "MAIL_RETRY" || len(*messages) != before {
			t.Fatal("validation failure handling", attempt, job.State, job.Reason)
		}
		if attempt < 3 {
			if job.State != "queued" || job.Payload == "" {
				t.Fatal("query failure discarded payload", job.State)
			}
			job.NextAttemptAt = time.Now().Add(-time.Second)
			if err := s.transaction(func(tx *sql.Tx) error { return mailWriteJSON(tx, "mail_job/"+job.ID, job) }); err != nil {
				t.Fatal(err)
			}
		} else if job.State != "failed" || job.Payload != "" {
			t.Fatal("retry budget did not end", job.State)
		}
	}
}
