//go:build linux

package update

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyDatabaseRejectsBrokenRelationsWithoutChangingBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.sqlite")
	createBridgeDatabase(t, path, 7)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("PRAGMA foreign_keys=OFF; CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(parent_id INTEGER REFERENCES parent(id)); INSERT INTO child VALUES(123);")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = verifyDatabaseSchema(path, 7); err == nil || !strings.Contains(err.Error(), "foreign key") {
		t.Fatalf("invalid backup relation accepted: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("verification changed backup")
	}
}
