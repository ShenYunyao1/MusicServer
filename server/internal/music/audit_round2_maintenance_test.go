package music

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRound2StartupRemovesOnlyOrphanMailCredentials(t *testing.T) {
	a, auth, _ := mailTestSetup(t)
	s := a.store
	record, err := readMailSettings(s.db)
	if err != nil {
		t.Fatal(err)
	}
	orphan := strings.Repeat("a", 32) + ".json"
	if err := s.writeNewMailSecret(orphan, []byte(`{"password":"uncommitted"}`)); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(s.mailSecretsDirectory(), "operator-note.txt")
	if err := os.WriteFile(unknown, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(s.dataPath, s.mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := os.Stat(filepath.Join(s.mailSecretsDirectory(), orphan)); !os.IsNotExist(err) {
		t.Fatal("orphan credential retained after restart", err)
	}
	for _, name := range []string{record.SecretID + ".json", "master.key", "operator-note.txt"} {
		if _, err := os.Stat(filepath.Join(s.mailSecretsDirectory(), name)); err != nil {
			t.Fatal("referenced/unknown secret deleted", name, err)
		}
	}
	if _, err := readUser(reopened.db, auth.userID); err != nil {
		t.Fatal(err)
	}
}

func TestRound2MailCleanupWarningAndRetry(t *testing.T) {
	a, _, _ := mailTestSetup(t)
	s := a.store
	orphan := strings.Repeat("b", 32) + ".json"
	if err := s.writeNewMailSecret(orphan, []byte(`{"password":"uncommitted"}`)); err != nil {
		t.Fatal(err)
	}
	s.mail.secretRemoveHook = func(string) error { return os.ErrPermission }
	if err := s.CleanupMailSecrets(); !errors.Is(err, os.ErrPermission) {
		t.Fatal("cleanup failure hidden", err)
	}
	settings, err := s.MailSettings()
	if err != nil || settings.CleanupWarning == "" {
		t.Fatal("administrator warning missing", settings, err)
	}
	s.mail.secretRemoveHook = nil
	if err := s.CleanupMailSecrets(); err != nil {
		t.Fatal(err)
	}
	settings, err = s.MailSettings()
	if err != nil || settings.CleanupWarning != "" {
		t.Fatal("warning did not clear after retry", settings, err)
	}
}

func TestRound2WALFailureExplainsSupportedStorage(t *testing.T) {
	for _, cause := range []error{nil, os.ErrPermission} {
		err := walSupportError("library.sqlite", "delete", cause)
		if err == nil || !strings.Contains(err.Error(), "library.sqlite") || !strings.Contains(err.Error(), "local writable") || !strings.Contains(err.Error(), "-wal/-shm") {
			t.Fatal("WAL diagnostic incomplete", err)
		}
		if cause != nil && !errors.Is(err, cause) {
			t.Fatal("cause hidden", err)
		}
	}
	if err := walSupportError("library.sqlite", "wal", nil); err != nil {
		t.Fatal(err)
	}
}

func TestRound2RestoreCleanupReportsLockIOErrorSeparately(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	key := restoreTargetKey(target)
	stage := filepath.Join(parent, ".musicserver-restore-"+key+"-123")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeRestoreMarker(stage, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(parent, ".musicserver-restorelock-"+key+".lock"), 0700); err != nil {
		t.Fatal(err)
	}
	removed, active, err := CleanupRestoreStaging(parent)
	if err == nil || active != 0 || removed != 0 {
		t.Fatal("lock I/O error misreported as active restore", removed, active, err)
	}
}
