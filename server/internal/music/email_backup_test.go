package music

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmailBackupRestoreExcludesSecretsAndPendingMail(t *testing.T) {
	api, _ := testAPI(t)
	s := api.store
	admin, session, _ := loginTestAccount(t, s, "admin")
	s.CloseMail()
	s.workers.Wait()
	settings, err := s.UpdateMailSettings(MailSettingsInput{Host: "smtp.example.test", Port: 465, TLSMode: "tls", FromAddress: "sender@example.test", Username: "sender", Password: "private-backup-smtp-fixture"}, MutationOptions{UserID: admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.markMailTested(settings.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStorageQuota(admin.ID, 1048576, 0, MutationOptions{UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.transaction(func(tx *sql.Tx) error {
		state, err := readAccountEmail(tx, admin.ID)
		if err != nil {
			return err
		}
		state.Required = true
		state.Email = "bound@example.test"
		state.VerifiedAt = persistedTime(time.Now())
		if err := writeAccountEmail(tx, admin.ID, state); err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?)", "email_binding/"+hashSecret(state.Email), admin.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	quota := int64(2097152)
	if _, err := s.UpdateRegistrationPolicy(RegistrationPolicyInput{Enabled: &enabled, DefaultQuotaBytes: &quota}, MutationOptions{UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	auth := authContext{session: true, userID: admin.ID, keyID: session.ID, authVersion: admin.AuthVersion, role: admin.Role}
	if _, err := s.RequestEmailChallenge(auth, "pending@example.test", "test administrator password", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	archive, err := s.CreateBackupBundle(context.Background(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(s.backupDirectory(), archive.Name)
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer zipped.Close()
	reader := tar.NewReader(zipped)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(header.Name, ".mail-secrets") {
			t.Fatal("mail secrets were archived", header.Name)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "private-backup-smtp-fixture") {
			t.Fatal("plaintext SMTP credential in backup", header.Name)
		}
	}
	destination := filepath.Join(t.TempDir(), "restored")
	if _, err := RestoreBackup(context.Background(), archivePath, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "data", ".mail-secrets")); !os.IsNotExist(err) {
		t.Fatal("restore materialized mail secrets", err)
	}
	restored, err := NewStore(filepath.Join(destination, "data", "library.sqlite"), destination)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	state, err := readAccountEmail(restored.db, admin.ID)
	if err != nil || !state.Required || !accountEmailVerified(state) || state.Email != "bound@example.test" {
		t.Fatal("verified policy was not retained", state, err)
	}
	if restored.MailReady() {
		t.Fatal("restored service falsely claims mail ready")
	}
	policy, err := restored.RegistrationPolicy()
	if err != nil || policy.Enabled || policy.DefaultQuotaBytes != quota {
		t.Fatal("restore did not safely close registration while preserving policy", policy, err)
	}
	var pending int
	if err := restored.db.QueryRow("SELECT count(*) FROM meta WHERE key LIKE 'email_challenge/%' OR key LIKE 'mail_job/%' OR key LIKE 'mail_limit/%'").Scan(&pending); err != nil || pending != 0 {
		t.Fatal("temporary mail survived restore", pending, err)
	}
	var binding string
	if err := restored.db.QueryRow("SELECT value FROM meta WHERE key=?", "email_binding/"+hashSecret(state.Email)).Scan(&binding); err != nil || binding != admin.ID {
		t.Fatal("verified address binding was lost", binding, err)
	}
}
