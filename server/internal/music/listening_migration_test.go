package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func snapshotListeningBaseline(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='schema_migrations' AND name NOT LIKE 'listening_%'")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	result := map[string][]string{}
	for _, name := range tables {
		q := `SELECT * FROM "` + strings.ReplaceAll(name, `"`, `""`) + `"`
		if name == "meta" {
			q += " WHERE key!='schema_version'"
		}
		rs, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rs.Columns()
		result[name] = []string{}
		for rs.Next() {
			v := make([]any, len(cols))
			p := make([]any, len(cols))
			for i := range v {
				p[i] = &v[i]
			}
			if err = rs.Scan(p...); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(v)
			result[name] = append(result[name], string(b))
		}
		rs.Close()
		sort.Strings(result[name])
	}
	return result
}

func TestListeningMigrationPreservesSchema8AndRollback(t *testing.T) {
	s := migrationFixture(t)
	if err := initializeSQLiteSchemaForTarget(s.db, 8); err != nil {
		t.Fatal(err)
	}
	before := snapshotListeningBaseline(t, s.db)
	if err := s.prepareSQLiteMigrationBackup(9); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(s.dataPath), ".schema-8-to-9-*", "snapshot.sqlite"))
	if err != nil || len(backups) != 1 {
		t.Fatal(backups, err)
	}
	db, err := openSQLite(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	assertMigrationSchema(t, db, 8)
	if !reflect.DeepEqual(before, snapshotListeningBaseline(t, db)) {
		t.Fatal("backup lost original rows")
	}
	db.Close()
	_, err = s.db.Exec("CREATE TRIGGER reject_listening_migration BEFORE UPDATE OF value ON meta WHEN NEW.key='schema_version' AND NEW.value='9' BEGIN SELECT RAISE(ABORT,'injected migration failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	if err = initializeSQLiteSchemaForTarget(s.db, 9); err == nil {
		t.Fatal("injected migration accepted")
	}
	assertMigrationSchema(t, s.db, 8)
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name LIKE 'listening_%'").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial schema survived rollback", count, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER reject_listening_migration"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = initializeSQLiteSchemaForTarget(s.db, 9); err != nil {
			t.Fatal(err)
		}
	}
	assertMigrationSchema(t, s.db, 9)
	if !reflect.DeepEqual(before, snapshotListeningBaseline(t, s.db)) {
		t.Fatal("schema8 rows changed")
	}
	if err = s.db.QueryRow("SELECT count(*) FROM schema_migrations WHERE from_version=8 AND to_version=9").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err = initializeSQLiteSchemaForTarget(s.db, 8); err == nil {
		t.Fatal("downgrade accepted")
	}
}

func TestListeningBackupRestoreFrom8To9(t *testing.T) {
	s := migrationFixture(t)
	if err := initializeSQLiteSchemaForTarget(s.db, 8); err != nil {
		t.Fatal(err)
	}
	archive := migrationArchive(t, s, 8, nil)
	dest := filepath.Join(t.TempDir(), "restored")
	if _, err := restoreBackupForTarget(context.Background(), archive, dest, 9); err != nil {
		t.Fatal(err)
	}
	db, err := openSQLite(filepath.Join(dest, "data/library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertMigrationSchema(t, db, 9)
	var n int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('listening_sessions','listening_totals','listening_daily')").Scan(&n); err != nil || n != 3 {
		t.Fatal(n, err)
	}
}

func TestListeningBackupRestoreNineRetainsPrivateCounts(t *testing.T) {
	api, _ := testAPIForSchema(t, 9)
	user, _ := readyMember(t, api, "restore-listening")
	ids := browseSeedTracks(t, api, user.ID, user.ID, 1)
	for _, query := range []string{
		"INSERT INTO listening_totals VALUES(?,?,3,1000)",
		"INSERT INTO listening_daily VALUES(?,?,'2026-11-02',3,1000)",
		"INSERT INTO listening_sessions VALUES(?,'12345678-1234-4234-8234-123456789012',?,100,30000,30000,1,1000)",
	} {
		if _, err := api.store.db.Exec(query, user.ID, ids[0]); err != nil {
			t.Fatal(err)
		}
	}
	archive := migrationArchive(t, api.store, 9, nil)
	dest := filepath.Join(t.TempDir(), "restored")
	if _, err := restoreBackupForTarget(context.Background(), archive, dest, 9); err != nil {
		t.Fatal(err)
	}
	db := sqliteConnection(t, filepath.Join(dest, "data/library.sqlite"))
	assertMigrationSchema(t, db, 9)
	for _, table := range []string{"listening_totals", "listening_daily", "listening_sessions"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM "+table+" WHERE user_id=?", user.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("lost %s: %d %v", table, count, err)
		}
	}
	if _, err := restoreBackupForTarget(context.Background(), archive, filepath.Join(t.TempDir(), "downgrade"), 8); err == nil {
		t.Fatal("schema9 backup downgraded")
	}
}
