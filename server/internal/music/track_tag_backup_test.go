package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
)

func tagDataSnapshot(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	result := map[string][]string{}
	for _, table := range []string{"tag_groups", "tag_scopes", "tags", "tag_aliases", "track_tags", "track_tag_overrides", "track_tag_states", "saved_track_filters", "track_tag_imports"} {
		rows, err := db.Query("SELECT * FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		result[table] = []string{}
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			result[table] = append(result[table], string(encoded))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(result[table])
	}
	return result
}

func TestTrackTagBackupRestoresDictionaryRelationshipsAndPrivateTombstones(t *testing.T) {
	a, _ := tagHTTPAPI(t)
	s := a.store
	auth := tagTestAuth(t, s, "admin")
	public := mustLinkTag(t, s, auth, "public", "Backup public")
	personal := mustLinkTag(t, s, auth, "personal", "Backup private")
	target := mustLinkTag(t, s, auth, "personal", "Backup merged")
	track := s.Tracks()[0]
	for _, item := range []Tag{public, personal} {
		if _, err := s.ChangeTrackTags(auth, track.ID, item.Scope, 1, []string{item.ID}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.MergeTags(auth, personal.ID, target.ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	runGenreImport(t, s, track.ID, []string{public.Name, "Unknown saved source"})
	if _, err := s.db.Exec("INSERT INTO saved_track_filters VALUES(?,?,?, ?,1,'created','updated')", uuid.NewString(), auth.userID, "Private saved", `{"tags":["`+personal.ID+`"]}`); err != nil {
		t.Fatal(err)
	}
	before := tagDataSnapshot(t, s.db)
	archive, err := s.createBackupBundleForTarget(context.Background(), false, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored")
	if _, err := restoreBackupForTarget(context.Background(), filepath.Join(s.backupDirectory(), archive.Name), destination, 8); err != nil {
		t.Fatal(err)
	}
	db := sqliteConnection(t, filepath.Join(destination, "data/library.sqlite"))
	if after := tagDataSnapshot(t, db); !reflect.DeepEqual(before, after) {
		t.Fatal("restoring schema 8 changed tag data", before, after)
	}
	var sessions int
	if err := db.QueryRow("SELECT count(*) FROM sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("restored sessions retained", sessions, err)
	}
}
