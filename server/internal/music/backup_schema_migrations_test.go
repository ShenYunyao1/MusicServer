package music

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func migrationArchive(t *testing.T, s *Store, schema int, mutate func(string)) string {
	t.Helper()
	snapshot, err := s.Backup()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(snapshot)
	if mutate != nil {
		mutate(snapshot)
	}
	database, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(s.Settings())
	if err != nil {
		t.Fatal(err)
	}
	manifest := BackupManifest{Format: 1, ID: "b0e3e30d-00be-4fdd-bc75-398e3a21f321", CreatedAt: time.Now().UTC(), Version: "test", Schema: schema}
	contents := map[string][]byte{"database.sqlite": database, "config.json": config}
	for _, name := range []string{"database.sqlite", "config.json"} {
		data := contents[name]
		hash := sha256.Sum256(data)
		manifest.Files = append(manifest.Files, BackupFile{Path: name, Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])})
	}
	archivePath := filepath.Join(t.TempDir(), "migration.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(encoded))}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(encoded); err != nil {
		t.Fatal(err)
	}
	for _, declaration := range manifest.Files {
		if err := archive.WriteHeader(&tar.Header{Name: declaration.Path, Mode: 0600, Size: declaration.Size}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(contents[declaration.Path]); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
	return archivePath
}

func TestBackupSchemaMigrationRestoresOldArchiveAndRetainsSafetyCleanup(t *testing.T) {
	s := migrationFixture(t)
	archive, err := s.createBackupBundleForTarget(context.Background(), true, nil, 7)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(s.backupDirectory(), archive.Name)
	var snapshots []map[string][]string
	for _, target := range []int{7, 8} {
		destination := filepath.Join(t.TempDir(), "restored")
		manifest, err := restoreBackupForTarget(context.Background(), archivePath, destination, target)
		if err != nil {
			t.Fatal(err)
		}
		if manifest.Schema != 7 {
			t.Fatal("restore rewrote the source archive declaration")
		}
		db := sqliteConnection(t, filepath.Join(destination, "data/library.sqlite"))
		assertMigrationSchema(t, db, target)
		var count int
		if err := db.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 0 {
			t.Fatal("restored sessions retained", err)
		}
		if err := db.QueryRow("SELECT count(*) FROM upload_staging").Scan(&count); err != nil || count != 0 {
			t.Fatal("restored staging retained", err)
		}
		if err := db.QueryRow("SELECT count(*) FROM meta WHERE key LIKE 'email_challenge/%' OR key LIKE 'mail_job/%' OR key LIKE 'upload_resume/%'").Scan(&count); err != nil || count != 0 {
			t.Fatal("restored pending secrets retained", err)
		}
		if _, err := os.Stat(filepath.Join(destination, s.Tracks()[0].Path)); err != nil {
			t.Fatal("media was not restored", err)
		}
		// Both restore paths deliberately rotate sync secrets and rebase paths.
		if _, err := db.Exec("UPDATE meta SET value='normalized' WHERE key='sync_secret'; UPDATE settings SET value=json_set(value,'$.media_root','normalized') WHERE key='server'"); err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, migrationSnapshot(t, db))
	}
	if !reflect.DeepEqual(snapshots[0], snapshots[1]) {
		t.Fatalf("target 8 changed restore behavior: %v vs %v", snapshots[0], snapshots[1])
	}
	assertMigrationSchema(t, s.db, 7)
}

func TestBackupSchemaMigrationRejectsDeclarationsDowngradeAndFailure(t *testing.T) {
	s := migrationFixture(t)
	for _, test := range []struct {
		name                     string
		declared, actual, target int
		sql                      string
	}{
		{"declaration ahead of database", 8, 7, 8, ""},
		{"database ahead of declaration", 7, 8, 8, ""},
		{"metadata mismatch before migration", 7, 7, 8, "UPDATE meta SET value='8' WHERE key='schema_version'"},
		{"downgrade", 8, 8, 7, ""},
		{"unknown declaration", 9, 7, 8, ""},
		{"unknown target", 7, 7, 9, ""},
		{"unknown database", 7, 9, 8, "PRAGMA user_version=9"},
		{"migration failure", 7, 7, 8, "CREATE TRIGGER reject_migration BEFORE UPDATE OF value ON meta WHEN NEW.key='schema_version' AND NEW.value='8' BEGIN SELECT RAISE(ABORT,'injected migration failure'); END"},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := migrationArchive(t, s, test.declared, func(path string) {
				db, err := openSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if test.actual == 8 {
					if err := initializeSQLiteSchemaForTarget(db, 8); err != nil {
						t.Fatal(err)
					}
				}
				if test.sql != "" {
					if _, err := db.Exec(test.sql); err != nil {
						t.Fatal(err)
					}
				}
			})
			parent := t.TempDir()
			destination := filepath.Join(parent, "restored")
			if _, err := restoreBackupForTarget(context.Background(), archive, destination, test.target); err == nil {
				t.Fatal("invalid restore accepted")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("failed restore published destination")
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed restore leaked staging", err)
			}
			assertMigrationSchema(t, s.db, 7)
		})
	}
}

func TestBackupSchemaMigrationCreationUsesVerifiedDatabaseIdentity(t *testing.T) {
	s := migrationFixture(t)
	if _, err := s.db.Exec("UPDATE meta SET value='8' WHERE key='schema_version'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBackupBundle(context.Background(), false, nil); err == nil {
		t.Fatal("backup emitted a mismatched database")
	}
	if _, err := s.db.Exec("UPDATE meta SET value='7' WHERE key='schema_version'"); err != nil {
		t.Fatal(err)
	}
	if err := initializeSQLiteSchemaForTarget(s.db, 8); err != nil {
		t.Fatal(err)
	}
	archive, err := s.CreateBackupBundle(context.Background(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(s.backupDirectory(), archive.Name)
	destination := filepath.Join(t.TempDir(), "restored")
	manifest, err := RestoreBackup(context.Background(), archivePath, destination)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != 8 {
		t.Fatal("backup schema differs from database identity")
	}
	db := sqliteConnection(t, filepath.Join(destination, "data/library.sqlite"))
	assertMigrationSchema(t, db, 8)
	if _, err := restoreBackupForTarget(context.Background(), archivePath, filepath.Join(t.TempDir(), "downgrade"), 7); err == nil {
		t.Fatal("legacy target accepted schema 8 downgrade")
	}
}
