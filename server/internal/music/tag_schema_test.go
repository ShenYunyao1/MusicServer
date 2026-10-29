package music

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestTagSchemaMigrationSeedsAndEnforcesRelationalBoundaries(t *testing.T) {
	s, auth := tagTestStore(t)
	assertMigrationSchema(t, s.db, 8)
	if err := requireTagsSchema(s.db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM tag_groups WHERE builtin=1 AND version=1").Scan(&count); err != nil || count != 6 {
		t.Fatal("incorrect builtin seed", count, err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM tag_scopes WHERE id='public' AND user_id IS NULL AND revision=0").Scan(&count); err != nil || count != 1 {
		t.Fatal("incorrect public scope seed", count, err)
	}
	public, err := s.CreateTag(auth, TagInput{GroupID: "genre", Name: "Public"})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := s.CreateTag(auth, TagInput{Scope: "personal", GroupID: "genre", Name: "Personal"})
	if err != nil {
		t.Fatal(err)
	}
	otherGroup, err := s.CreateTag(auth, TagInput{GroupID: "mood", Name: "Other group"})
	if err != nil {
		t.Fatal(err)
	}
	track := s.Tracks()[0]
	for _, test := range []struct {
		name, query string
		args        []any
	}{
		{"scope impersonation", "INSERT INTO tag_scopes VALUES('someone-else',?,0)", []any{auth.userID}},
		{"second public scope", "INSERT INTO tag_scopes VALUES('another-public',NULL,0)", nil},
		{"negative revision", "UPDATE tag_scopes SET revision=-1 WHERE id='public'", nil},
		{"invalid version", "UPDATE tags SET version=0 WHERE id=?", []any{public.ID}},
		{"invalid deletion flag", "UPDATE tags SET deleted=2 WHERE id=?", []any{public.ID}},
		{"self merge", "UPDATE tags SET deleted=1,merged_into=id WHERE id=?", []any{public.ID}},
		{"cross-scope merge", "UPDATE tags SET deleted=1,merged_into=? WHERE id=?", []any{personal.ID, public.ID}},
		{"cross-group merge", "UPDATE tags SET deleted=1,merged_into=? WHERE id=?", []any{otherGroup.ID, public.ID}},
		{"live merged source", "UPDATE tags SET merged_into=? WHERE id=?", []any{uuid.NewString(), public.ID}},
		{"alias scope spoof", "INSERT INTO tag_aliases VALUES(?,'genre','spoof',?,'Spoof',0)", []any{auth.userID, public.ID}},
		{"alias group spoof", "INSERT INTO tag_aliases VALUES('public','mood','spoof',?,'Spoof',0)", []any{public.ID}},
		{"alias primary collision", "INSERT INTO tag_aliases VALUES('public','genre','second-primary',?,'Second primary',1)", []any{public.ID}},
		{"namespace collision", "INSERT INTO tag_aliases VALUES('public','genre','public',?,'Public',0)", []any{public.ID}},
		{"track scope spoof", "INSERT INTO track_tags VALUES(?,?,?,'manual',?,'created')", []any{track.ID, public.ID, auth.userID, auth.userID}},
		{"track invalid source", "INSERT INTO track_tags VALUES(?,?,'public','guessed',?,'created')", []any{track.ID, public.ID, auth.userID}},
		{"override scope spoof", "INSERT INTO track_tag_overrides VALUES(?,?,?,'confirmed',?,'created')", []any{track.ID, public.ID, auth.userID, auth.userID}},
		{"override invalid action", "INSERT INTO track_tag_overrides VALUES(?,?,'public','guessed',?,'created')", []any{track.ID, public.ID, auth.userID}},
		{"state invalid version", "INSERT INTO track_tag_states VALUES(?,'public',0)", []any{track.ID}},
		{"state missing scope", "INSERT INTO track_tag_states VALUES(?,'missing',1)", []any{track.ID}},
		{"filter invalid json", "INSERT INTO saved_track_filters VALUES(?,?,'Invalid','{bad',1,'created','updated')", []any{uuid.NewString(), auth.userID}},
		{"filter missing user", "INSERT INTO saved_track_filters VALUES(?,'missing','Invalid','{}',1,'created','updated')", []any{uuid.NewString()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := s.db.Exec(test.query, test.args...); err == nil {
				t.Fatal("invalid relational write accepted")
			}
		})
	}
	rows, err := s.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation after rejected mutations")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestTagSchemaMigrationRollsBackTablesSeedsAndOldRows(t *testing.T) {
	s, _, _ := seedStoreForSchema(t, 7)
	t.Cleanup(func() { closeStoreForTest(t, s) })
	before := migrationSnapshot(t, s.db)
	if _, err := s.db.Exec("CREATE TRIGGER reject_schema_eight BEFORE UPDATE ON meta WHEN NEW.key='schema_version' AND NEW.value='8' BEGIN SELECT RAISE(ABORT,'reject final schema marker'); END"); err != nil {
		t.Fatal(err)
	}
	if err := initializeSQLiteSchemaForTarget(s.db, 8); err == nil {
		t.Fatal("fault-injected migration succeeded")
	}
	assertMigrationSchema(t, s.db, 7)
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND (name LIKE 'tag_%' OR name LIKE 'track_tag%' OR name='saved_track_filters' OR name='schema_migrations')").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed migration retained tag tables/seeds", count, err)
	}
	if after := migrationSnapshot(t, s.db); !reflect.DeepEqual(before, after) {
		t.Fatal("failed tag migration changed legacy records")
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_schema_eight"); err != nil {
		t.Fatal(err)
	}
	if err := initializeSQLiteSchemaForTarget(s.db, 8); err != nil {
		t.Fatal(err)
	}
	if err := initializeSQLiteSchemaForTarget(s.db, 8); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM tag_groups").Scan(&count); err != nil || count != 6 {
		t.Fatal("repeated migration reseeded groups", count, err)
	}
	if after := migrationSnapshot(t, s.db); !reflect.DeepEqual(before, after) {
		t.Fatal("tag migration changed legacy records")
	}
}

func TestTagSchemaScopeCascade(t *testing.T) {
	s, auth := tagTestStore(t)
	tag, err := s.CreateTag(auth, TagInput{Scope: "personal", GroupID: "custom", Name: "Remove scope"})
	if err != nil {
		t.Fatal(err)
	}
	trackID := s.Tracks()[0].ID
	if err := s.tagTransaction(false, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO track_tags VALUES(?,?,?,'manual',?,'created')", trackID, tag.ID, auth.userID, auth.userID); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO track_tag_overrides VALUES(?,?,?,'confirmed',?,'created')", trackID, tag.ID, auth.userID, auth.userID); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO track_tag_states VALUES(?,?,1)", trackID, auth.userID); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM tag_scopes WHERE id=?", auth.userID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"tags", "tag_aliases", "track_tags", "track_tag_overrides", "track_tag_states"} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("scope cascade failed", table, count, err)
		}
	}
}
