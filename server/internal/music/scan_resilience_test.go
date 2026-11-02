package music

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestScanConcurrentPurgeDoesNotAbortOtherImports(t *testing.T) {
	a, root := testAPI(t)
	track := a.store.Tracks()[0]
	if err := a.store.DeleteTrack(track.ID); err != nil {
		t.Fatal(err)
	}
	track = a.store.RemovedTracks()[0]
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	if err := os.WriteFile(filepath.Join(root, "new.wav"), validTestAudio(t), 0600); err != nil {
		t.Fatal(err)
	}
	walk := func(root string, fn fs.WalkDirFunc) error {
		if err := filepath.WalkDir(root, fn); err != nil {
			return err
		}
		return a.store.PurgeTrack(track.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: track.Version})
	}
	if _, err := a.store.runScanWithWalker(context.Background(), true, nil, walk); err != nil {
		t.Fatal("concurrent purge aborted scan", err)
	}
	if _, err := readTrackFrom(a.store.db, track.ID, true); err == nil {
		t.Fatal("purged track resurrected")
	}
	if len(a.store.Tracks()) != 1 || a.store.Tracks()[0].Path != "new.wav" {
		t.Fatal("unrelated import was lost")
	}
}

func TestScanUnreadableSubtreePreservesOldMissingState(t *testing.T) {
	a, root := testAPI(t)
	track := a.store.Tracks()[0]
	if err := os.Mkdir(filepath.Join(root, "unreadable"), 0700); err != nil {
		t.Fatal(err)
	}
	track.Path = "unreadable/song.mp3"
	if err := a.store.transaction(func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	walk := func(root string, fn fs.WalkDirFunc) error {
		if err := filepath.WalkDir(root, fn); err != nil {
			return err
		}
		return fn(filepath.Join(root, "unreadable"), nil, fs.ErrPermission)
	}
	if _, err := a.store.runScanWithWalker(context.Background(), true, nil, walk); err != nil {
		t.Fatal("subtree failure aborted scan", err)
	}
	after, err := readTrackFrom(a.store.db, track.ID, true)
	if err != nil || after.Missing {
		t.Fatal("unreadable path incorrectly marked missing", err)
	}
	if len(a.store.ScanStatus().Errors) == 0 {
		t.Fatal("subtree failure was not reported")
	}
	rootFailure := func(root string, fn fs.WalkDirFunc) error { return fn(root, nil, fs.ErrPermission) }
	if _, err := a.store.runScanWithWalker(context.Background(), true, nil, rootFailure); !errors.Is(err, fs.ErrPermission) {
		t.Fatal("root failure was hidden", err)
	}
}
