package music

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAuditBackupChangedMediaDoesNotDiscardDatabaseBackup(t *testing.T) {
	a, root := testAPI(t)
	track := a.store.Tracks()[0]
	changed := false
	archive, err := a.store.CreateBackupBundle(context.Background(), true, func(phase string, _, _ int64) {
		if phase == "checking" && !changed {
			changed = true
			if err := os.WriteFile(filepath.Join(root, track.Path), []byte("external media changed"), 0600); err != nil {
				t.Error(err)
			}
		}
	})
	if err != nil {
		t.Fatal("one changed file discarded entire backup", err)
	}
	manifest, err := RestoreBackup(context.Background(), filepath.Join(a.store.backupDirectory(), archive.Name), filepath.Join(t.TempDir(), "restored"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.MissingMedia) != 1 || manifest.MissingMedia[0] != filepath.ToSlash(track.Path) {
		t.Fatal("changed file not reported", manifest.MissingMedia)
	}
}

func TestAuditBackupCancelRejectsFinishedJobWithoutAudit(t *testing.T) {
	a, _ := testAPI(t)
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	a.store.backupJob = BackupJob{ID: "finished", State: "completed"}
	var before, after int
	a.store.db.QueryRow("SELECT count(*) FROM audit WHERE action='backup.cancel_requested'").Scan(&before)
	err := a.store.CancelBackupJob("finished", MutationOptions{UserID: admin.ID})
	if err == nil || errors.Is(err, errBackupJobNotFound) {
		t.Fatal("finished job accepted cancellation", err)
	}
	a.store.db.QueryRow("SELECT count(*) FROM audit WHERE action='backup.cancel_requested'").Scan(&after)
	if before != after {
		t.Fatal("rejected cancellation was audited")
	}
}

func TestAuditRestoreCleanupSkipsLockedTargetsAndUnmanagedNames(t *testing.T) {
	parent := t.TempDir()
	free := "00000000000000000000000000000001"
	active := "00000000000000000000000000000002"
	for _, name := range []string{".musicserver-restore-" + free + "-123", ".musicserver-restore-" + active + "-456", ".musicserver-restore-user-copy"} {
		if err := os.Mkdir(filepath.Join(parent, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	unlock, err := LockDatabaseProcess(filepath.Join(parent, ".musicserver-restorelock-"+active))
	if err != nil {
		t.Fatal(err)
	}
	removed, skipped, err := CleanupRestoreStaging(parent)
	unlock()
	if err != nil || removed != 1 || skipped != 1 {
		t.Fatal(removed, skipped, err)
	}
	if _, err := os.Stat(filepath.Join(parent, ".musicserver-restore-user-copy")); err != nil {
		t.Fatal("unmanaged folder removed", err)
	}
	removed, skipped, err = CleanupRestoreStaging(parent)
	if err != nil || removed != 1 || skipped != 0 {
		t.Fatal(removed, skipped, err)
	}
}

func TestAuditBackupUsesFrozenMediaDuringPacking(t *testing.T) {
	a, root := testAPI(t)
	track := a.store.Tracks()[0]
	changed := false
	archive, err := a.store.CreateBackupBundle(context.Background(), true, func(phase string, _, _ int64) {
		if phase == "packing" && !changed {
			changed = true
			if err := os.WriteFile(filepath.Join(root, track.Path), []byte("changed during compression"), 0600); err != nil {
				t.Error(err)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := RestoreBackup(context.Background(), filepath.Join(a.store.backupDirectory(), archive.Name), filepath.Join(t.TempDir(), "restored"))
	if err != nil || len(manifest.MissingMedia) != 0 {
		t.Fatal("frozen snapshot damaged", err, manifest.MissingMedia)
	}
}
