package music

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"musicserver/internal/diskspace"
)

func migrationBackupFiles(t *testing.T, s *Store) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(filepath.Dir(s.dataPath), ".schema-7-to-8-*", "snapshot.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestSchemaMigrationStartupBackupPreservesOldDatabase(t *testing.T) {
	s := migrationFixture(t)
	before := migrationSnapshot(t, s.db)
	if err := s.prepareSQLiteMigrationBackup(7); err != nil {
		t.Fatal(err)
	}
	if len(migrationBackupFiles(t, s)) != 0 {
		t.Fatal("same-schema startup backed up")
	}
	if err := s.prepareSQLiteMigrationBackup(8); err != nil {
		t.Fatal(err)
	}
	files := migrationBackupFiles(t, s)
	if len(files) != 1 {
		t.Fatal(files)
	}
	backup := sqliteConnection(t, files[0])
	assertMigrationSchema(t, backup, 7)
	if !reflect.DeepEqual(before, migrationSnapshot(t, backup)) {
		t.Fatal("snapshot lost old rows")
	}
	if err := initializeSQLiteSchemaForTarget(s.db, 8); err != nil {
		t.Fatal(err)
	}
	if err := s.prepareSQLiteMigrationBackup(8); err != nil {
		t.Fatal(err)
	}
	if len(migrationBackupFiles(t, s)) != 1 {
		t.Fatal("repeat startup backed up again")
	}
	assertMigrationSchema(t, backup, 7)
	if !reflect.DeepEqual(before, migrationSnapshot(t, s.db)) {
		t.Fatal("migration changed legacy rows")
	}
}

func TestSchemaMigrationStartupBackupFailureKeepsSource(t *testing.T) {
	t.Run("space", func(t *testing.T) {
		s := migrationFixture(t)
		before := migrationSnapshot(t, s.db)
		s.spaceProbe = func(string) (diskspace.Space, error) { return diskspace.Space{}, nil }
		if err := s.prepareSQLiteMigrationBackup(8); !errors.Is(err, errStorageLow) {
			t.Fatal(err)
		}
		assertMigrationSchema(t, s.db, 7)
		if !reflect.DeepEqual(before, migrationSnapshot(t, s.db)) || len(migrationBackupFiles(t, s)) != 0 {
			t.Fatal("failed preflight changed source")
		}
	})
	t.Run("foreign key", func(t *testing.T) {
		s := migrationFixture(t)
		if _, err := s.db.Exec("PRAGMA foreign_keys=OFF; INSERT INTO playlist_tracks VALUES('not-a-playlist',?,0,''); PRAGMA foreign_keys=ON", s.Tracks()[0].ID); err != nil {
			t.Fatal(err)
		}
		before := migrationSnapshot(t, s.db)
		if err := s.prepareSQLiteMigrationBackup(8); err == nil {
			t.Fatal("invalid references accepted")
		}
		assertMigrationSchema(t, s.db, 7)
		if !reflect.DeepEqual(before, migrationSnapshot(t, s.db)) || len(migrationBackupFiles(t, s)) != 0 {
			t.Fatal("failed validation changed source")
		}
	})
	t.Run("failed migration retry", func(t *testing.T) {
		s := migrationFixture(t)
		if err := s.prepareSQLiteMigrationBackup(8); err != nil {
			t.Fatal(err)
		}
		err := initializeSQLiteSchemaWithMigrations(s.db, 8, []sqliteMigration{{from: 7, to: 8, apply: func(tx *sql.Tx) error {
			if err := migrateSQLite7To8(tx); err != nil {
				return err
			}
			return errors.New("injected")
		}}})
		if err == nil {
			t.Fatal("injected migration succeeded")
		}
		assertMigrationSchema(t, s.db, 7)
		if err := s.prepareSQLiteMigrationBackup(8); err != nil {
			t.Fatal(err)
		}
		if len(migrationBackupFiles(t, s)) != 2 {
			t.Fatal("retry reused prior snapshot")
		}
	})
	t.Run("new database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "new.sqlite")
		db := sqliteConnection(t, path)
		s := &Store{db: db, dataPath: path}
		if err := s.prepareSQLiteMigrationBackup(8); err != nil {
			t.Fatal(err)
		}
		if len(migrationBackupFiles(t, s)) != 0 {
			t.Fatal("new database backup")
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	})
}
