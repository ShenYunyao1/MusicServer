package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Include every existing row, including opaque private metadata and settings.
// A migration must not deserialize and reconstruct these application records.
func migrationSnapshot(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name!='schema_migrations' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	snapshot := map[string][]string{}
	for _, name := range names {
		query := `SELECT * FROM "` + strings.ReplaceAll(name, `"`, `""`) + `"`
		if name == "meta" {
			query += " WHERE key!='schema_version'"
		}
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		snapshot[name] = []string{}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			snapshot[name] = append(snapshot[name], string(encoded))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(snapshot[name])
	}
	return snapshot
}

func migrationFixture(t *testing.T) *Store {
	t.Helper()
	s, _, tracks := seedStore(t)
	t.Cleanup(func() { closeStoreForTest(t, s) })
	var owner string
	if err := s.db.QueryRow("SELECT id FROM users WHERE username='admin'").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPlaylist(Playlist{ID: "migration-playlist", Name: "Private playlist", TrackIDs: []string{tracks[0].ID}, TrackNotes: map[string]string{tracks[0].ID: "private note"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAPIKey(APIKeyCreateRequest{Name: "Keep player key", AllowedPlaylistIDs: []string{"migration-playlist"}}); err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{"INSERT OR REPLACE INTO meta VALUES('opaque/private','keep opaque private value'),('account_email/'||?,?),('email_challenge/keep','pending challenge'),('mail_job/keep','encrypted job'),('upload_resume/keep','pending upload')", []any{owner, `{"email":"keep@example.com","verified":true}`}},
		{"INSERT OR REPLACE INTO settings VALUES('storage_quota/'||?,?),('mail_settings',?),('registration_policy',?)", []any{owner, `{"limit_bytes":123456789}`, `{"tested":true,"host":"mail.example.com"}`, `{"enabled":true}`}},
		{"INSERT INTO sessions VALUES('migration-session','unique-token',?,1,'created','active','expires','')", []any{owner}},
		{"INSERT INTO login_limits VALUES('keep-bucket',3,'keep-window')", nil},
		{"INSERT INTO subscriptions VALUES(?,'migration-playlist','Subscribed name',3,'created')", []any{owner}},
		{"INSERT INTO subscription_notes VALUES(?,'migration-playlist',?,'private subscription note')", []any{owner, tracks[0].ID}},
		{"INSERT INTO users VALUES('migration-member','migration-member','Member','hash','member',1,1,2,'created','updated',0)", nil},
		{"INSERT INTO playlist_invitations VALUES('keep-invitation','migration-playlist',?,'migration-member','pending',2,'Last name','created','updated')", []any{owner}},
		{"INSERT INTO playlist_tombstones VALUES('removed-playlist')", nil},
		{"INSERT INTO metadata_overrides VALUES(?,'genre','AC/DC')", []any{tracks[0].ID}},
		{"INSERT INTO cover_assets VALUES('keep-cover','image/png',X'001122FF')", nil},
		{"INSERT INTO upload_staging VALUES('keep-stage','keep.mp3',17,'hash','temp','{}','created','expires',?)", []any{owner}},
		{"INSERT OR REPLACE INTO api_call_stats SELECT id,9,8,1,123,456,'last',200 FROM api_keys", nil},
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("%s: %v", statement.query, err)
		}
	}
	return s
}

func assertMigrationSchema(t *testing.T, db *sql.DB, expected int) {
	t.Helper()
	var schema int
	var metadata string
	if err := db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT value FROM meta WHERE key='schema_version'").Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if schema != expected || metadata != fmt.Sprint(expected) {
		t.Fatalf("schema=%d metadata=%s; want %d", schema, metadata, expected)
	}
}

func TestSchemaMigrationPreservesAllRowsAndIsIdempotent(t *testing.T) {
	s := migrationFixture(t)
	before := migrationSnapshot(t, s.db)
	if err := initializeSQLiteSchema(s.db); err != nil {
		t.Fatal(err)
	}
	assertMigrationSchema(t, s.db, 7)
	if err := initializeSQLiteSchemaForTarget(s.db, 8); err != nil {
		t.Fatal(err)
	}
	assertMigrationSchema(t, s.db, 8)
	if after := migrationSnapshot(t, s.db); !reflect.DeepEqual(before, after) {
		t.Fatalf("migration changed existing rows: before=%v after=%v", before, after)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM schema_migrations WHERE from_version=7 AND to_version=8").Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration ledger count=%d err=%v", count, err)
	}
	if err := initializeSQLiteSchemaForTarget(s.db, 8); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 1 {
		t.Fatalf("repeated migration count=%d err=%v", count, err)
	}
	if err := initializeSQLiteSchema(s.db); err == nil {
		t.Fatal("bridge server accepted schema 8 at current target 7")
	}
}

func TestSchemaMigrationFailureRollsBackDDLRowsAndVersions(t *testing.T) {
	s := migrationFixture(t)
	before := migrationSnapshot(t, s.db)
	injected := errors.New("injected migration failure")
	migrations := []sqliteMigration{{from: 7, to: 8, apply: func(tx *sql.Tx) error {
		if err := migrateSQLite7To8(tx); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE tracks SET title='corrupt'; UPDATE meta SET value='8' WHERE key='schema_version'; PRAGMA user_version=8"); err != nil {
			return err
		}
		return injected
	}}}
	if err := initializeSQLiteSchemaWithMigrations(s.db, 8, migrations); !errors.Is(err, injected) {
		t.Fatalf("expected injected failure: %v", err)
	}
	assertMigrationSchema(t, s.db, 7)
	if !reflect.DeepEqual(before, migrationSnapshot(t, s.db)) {
		t.Fatal("failed migration changed existing rows")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='schema_migrations'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration published DDL: %d %v", count, err)
	}
	if err := initializeSQLiteSchema(s.db); err != nil {
		t.Fatal(err)
	}
	reopened := sqliteConnection(t, s.dataPath)
	assertMigrationSchema(t, reopened, 7)
	if !reflect.DeepEqual(before, migrationSnapshot(t, reopened)) {
		t.Fatal("failed migration persisted changed rows on another connection")
	}
	if err := initializeSQLiteSchemaForTarget(reopened, 8); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaMigrationNewDatabaseTargets(t *testing.T) {
	for _, target := range []int{7, 8} {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			db := sqliteConnection(t, filepath.Join(t.TempDir(), "new.sqlite"))
			if err := initializeSQLiteSchemaForTarget(db, target); err != nil {
				t.Fatal(err)
			}
			assertMigrationSchema(t, db, target)
			if err := initializeSQLiteSchemaForTarget(db, target); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSchemaMigrationRejectsUnsupportedOrInconsistentDatabases(t *testing.T) {
	for _, test := range []struct {
		name, sql string
		target    int
	}{
		{"unknown source", "PRAGMA user_version=99", 8},
		{"unsupported old source", "PRAGMA user_version=6; UPDATE meta SET value='6' WHERE key='schema_version'", 8},
		{"unknown target", "", 9},
		{"downgrade", "PRAGMA user_version=8; UPDATE meta SET value='8' WHERE key='schema_version'", 7},
		{"metadata mismatch", "UPDATE meta SET value='8' WHERE key='schema_version'", 8},
		{"unversioned nonempty", "PRAGMA user_version=0", 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := sqliteConnection(t, filepath.Join(t.TempDir(), "library.sqlite"))
			if err := initializeSQLiteSchema(db); err != nil {
				t.Fatal(err)
			}
			if test.sql != "" {
				if _, err := db.Exec(test.sql); err != nil {
					t.Fatal(err)
				}
			}
			before := migrationSnapshot(t, db)
			if err := initializeSQLiteSchemaForTarget(db, test.target); err == nil {
				t.Fatal("invalid migration accepted")
			}
			if !reflect.DeepEqual(before, migrationSnapshot(t, db)) {
				t.Fatal("rejected migration modified rows")
			}
		})
	}
}
