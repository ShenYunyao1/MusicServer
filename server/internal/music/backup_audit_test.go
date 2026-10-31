package music

import (
	"context"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupListSurvivesCorruptArchive(t *testing.T) {
	a, _ := testAPI(t)
	good, err := a.store.CreateBackupBundle(context.Background(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	name, _ := backupArchiveName(id)
	if err := os.WriteFile(filepath.Join(a.store.backupDirectory(), name), []byte("broken archive"), 0600); err != nil {
		t.Fatal(err)
	}
	archives, err := a.store.BackupArchives()
	if err != nil {
		t.Fatalf("bad file blocked list: %v", err)
	}
	if len(archives) != 2 {
		t.Fatal(archives)
	}
	var valid, invalid bool
	for _, item := range archives {
		if item.ID == good.ID {
			valid = true
		}
		if item.ID == id {
			invalid = item.Error != ""
		}
	}
	if !valid || !invalid {
		t.Fatal("invalid backup was not represented", archives)
	}
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	if err := a.store.DeleteBackup(id, MutationOptions{UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
}
