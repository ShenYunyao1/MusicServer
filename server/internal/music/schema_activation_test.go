package music

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSchemaActivationNormalStartupMigratesOldDatabaseWithSnapshot(t *testing.T) {
	s, path, _ := seedStoreForSchema(t, 8)
	media := s.mediaRoot
	before := migrationSnapshot(t, s.db)
	closeStoreForTest(t, s)
	current, err := NewStore(path, media)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStoreForTest(t, current)
	assertMigrationSchema(t, current.db, 9)
	snapshots := listeningMigrationBackupFiles(t, current)
	if len(snapshots) != 1 {
		t.Fatal("normal startup must retain one old database snapshot", snapshots)
	}
	old := sqliteConnection(t, snapshots[0])
	assertMigrationSchema(t, old, 8)
	after := migrationSnapshot(t, old)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("legacy rows changed in snapshot")
	}
	if !reflect.DeepEqual(before, migrationSnapshot(t, current.db)) {
		t.Fatal("normal startup migration changed legacy application rows")
	}
	if err := validateMigrationDatabase(current.db, 9); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaActivationNewDatabaseUsesCurrentSchema(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "library.sqlite"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer closeStoreForTest(t, s)
	assertMigrationSchema(t, s.db, 9)
	if len(listeningMigrationBackupFiles(t, s)) != 0 {
		t.Fatal("new database created a migration backup")
	}
}

func TestSchemaActivationNormalStartupFailureLeavesLegacyUsable(t *testing.T) {
	s, path, _ := seedStoreForSchema(t, 8)
	media := s.mediaRoot
	before := migrationSnapshot(t, s.db)
	if _, err := s.db.Exec("CREATE TRIGGER reject_schema_nine BEFORE UPDATE ON meta WHEN NEW.key='schema_version' AND NEW.value='9' BEGIN SELECT RAISE(ABORT,'reject schema marker'); END"); err != nil {
		t.Fatal(err)
	}
	closeStoreForTest(t, s)
	if current, err := NewStore(path, media); err == nil {
		current.Close()
		t.Fatal("fault-injected normal startup succeeded")
	}
	db := sqliteConnection(t, path)
	assertMigrationSchema(t, db, 8)
	if !reflect.DeepEqual(before, migrationSnapshot(t, db)) {
		t.Fatal("failed normal startup rewrote old rows")
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='listening_sessions'").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial tag DDL survived", count, err)
	}
	if len(listeningMigrationBackupFiles(t, s)) != 1 {
		t.Fatal("failed migration did not leave snapshot")
	}
	if _, err := db.Exec("DROP TRIGGER reject_schema_nine"); err != nil {
		t.Fatal(err)
	}
	current, err := NewStore(path, media)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStoreForTest(t, current)
	assertMigrationSchema(t, current.db, 9)
	if len(listeningMigrationBackupFiles(t, current)) != 2 {
		t.Fatal("retry reused an older backup")
	}
}
func listeningMigrationBackupFiles(t *testing.T, s *Store) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(filepath.Dir(s.dataPath), ".schema-8-to-9-*", "snapshot.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}
