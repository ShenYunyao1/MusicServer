package music

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRound2PurgeDiscardsDisabledPreviewAndInvalidTask(t *testing.T) {
	a, root := testAPI(t)
	u, _ := readyMember(t, a, "purge-preview-r2")
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	p, err := a.store.StageAudio("preview.wav", bytes.NewReader(validTestAudio(t)), nil, MutationOptions{UserID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	var path string
	if err := a.store.db.QueryRow("SELECT temp_path FROM upload_staging WHERE id=?", p.ID).Scan(&path); err != nil {
		t.Fatal(err)
	}
	a.store.SetUserEnabled(u.ID, false, u.Version)
	u, _ = readUser(a.store.db, u.ID)
	if _, err := a.store.db.Exec("INSERT INTO meta VALUES('upload_resume/invalid',json_object('owner',?,'state','uploading'));INSERT OR REPLACE INTO meta VALUES('browse/account/'||?,'7')", u.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.store.PurgeUser(u.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: u.Version}); err != nil {
		t.Fatal("discardable upload blocked purge", err)
	}
	if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
		t.Fatal("preview file retained", err)
	}
	var count int
	a.store.db.QueryRow("SELECT count(*) FROM meta WHERE key='browse/account/'||?", u.ID).Scan(&count)
	if count != 0 {
		t.Fatal("account counter remained")
	}
}
