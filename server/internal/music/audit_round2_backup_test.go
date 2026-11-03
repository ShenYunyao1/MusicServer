package music

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"musicserver/internal/diskspace"
)

func TestRound2BackupSpaceChecksAreBoundedByBytes(t *testing.T) {
	a, _ := testAPI(t)
	calls := 0
	a.store.spaceProbe = func(string) (diskspace.Space, error) { calls++; return diskspace.Space{Available: 1 << 40}, nil }
	var output bytes.Buffer
	w := &backupSpaceWriter{store: a.store, path: a.store.backupDirectory(), writer: &output}
	for i := 0; i < 100; i++ {
		if _, err := w.Write(make([]byte, 1024)); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("small gzip writes repeated database/disk probe", calls)
	}
	w.checked = time.Now().Add(-2 * time.Second)
	if _, err := w.Write([]byte("x")); err != nil || calls != 2 {
		t.Fatal("elapsed time did not refresh policy", calls, err)
	}
}

func TestRound2BackupTimestampChangeKeepsMatchingBytes(t *testing.T) {
	a, media := testAPI(t)
	s := a.store
	track := s.Tracks()[0]
	s.spaceProbe = func(string) (diskspace.Space, error) {
		if err := os.Chtimes(filepath.Join(media, track.Path), time.Now().Add(time.Minute), time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		return diskspace.Space{Available: 1 << 40}, nil
	}
	root, err := os.OpenRoot(media)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	_, changed, err := s.freezeBackupMedia(context.Background(), root, backupInput{BackupFile: BackupFile{Path: "media/" + track.Path, Size: track.Size, SHA256: track.ContentHash}, Local: track.Path}, filepath.Join(t.TempDir(), "frozen"))
	if err != nil || changed {
		t.Fatal("timestamp alone excluded matching media", changed, err)
	}
}

func TestRound2BackupCancelAfterPackingIsRejected(t *testing.T) {
	a, _ := testAPI(t)
	u, _, _ := loginTestAccount(t, a.store, "admin")
	called := false
	a.store.backupJob = BackupJob{ID: "published", State: "running", Phase: "completed"}
	a.store.backupCancel = func() { called = true }
	if err := a.store.CancelBackupJob("published", MutationOptions{UserID: u.ID}); err != errBackupJobFinished || called {
		t.Fatal("published archive accepted cancellation", err, called)
	}
}

func TestRound2BackupCanContinueAfterUnrelatedCleanupFailure(t *testing.T) {
	a, _ := testAPI(t)
	a.store.backupCleanupHook = func() error { return os.ErrPermission }
	if _, err := a.store.CreateBackupBundle(context.Background(), false, nil); err != nil {
		t.Fatal("stale temporary blocked new backup", err)
	}
}
