package music

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMultiuserSchemaOwnership(t *testing.T) {
	api, _ := testAPI(t)
	var schema int
	if err := api.store.db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil || schema != 5 {
		t.Fatalf("schema: %d %v", schema, err)
	}
	var owner, uploader string
	if err := api.store.db.QueryRow("SELECT owner_id,uploaded_by FROM tracks LIMIT 1").Scan(&owner, &uploader); err != nil || owner == "" || owner != uploader {
		t.Fatalf("ownership: %s %s %v", owner, uploader, err)
	}
	var mustChange bool
	if err := api.store.db.QueryRow("SELECT must_change_password FROM users WHERE id=?", owner).Scan(&mustChange); err != nil || mustChange {
		t.Fatalf("bootstrap password state: %t %v", mustChange, err)
	}
	var count int
	if err := api.store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='playlists_owner_order'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("owner ordering index: %d %v", count, err)
	}
	if _, err := api.store.db.Exec("UPDATE tracks SET owner_id='nonexistent'"); err == nil {
		t.Fatal("ownership foreign key was not enforced")
	}
}

func TestMultiuserSchemaRefusesOldFourUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.sqlite")
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=4; CREATE TABLE preserved(value TEXT); INSERT INTO preserved VALUES('keep')"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if store, err := NewStore(path, t.TempDir()); err == nil {
		store.Close()
		t.Fatal("old schema accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("old database was modified")
	}
}
