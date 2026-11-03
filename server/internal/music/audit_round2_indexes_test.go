package music

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRound2IndexVersionReplacesOldTrigger(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	track := s.Tracks()[0]
	_, err := s.db.Exec(`INSERT OR REPLACE INTO meta VALUES('browse/schema_version','1');DROP TRIGGER browse_search_UPDATE;CREATE TRIGGER browse_search_UPDATE AFTER UPDATE ON tracks BEGIN SELECT 1;END;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := initializeBrowseIndexes(s.db); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE tracks SET payload=json_set(payload,'$.artist','round2-versioned') WHERE id=?", track.ID); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := s.db.QueryRow("SELECT search_text FROM browse_track_search WHERE track_id=?", track.ID).Scan(&text); err != nil || !strings.Contains(text, "round2-versioned") {
		t.Fatal("old trigger survived upgrade", text, err)
	}
}

func TestRound2LegacyBadJSONDoesNotPreventRuntimeOpen(t *testing.T) {
	a, media := testAPI(t)
	path := a.store.dataPath
	track := a.store.Tracks()[0]
	a.store.Close()
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`DROP INDEX tracks_cover_lookup;DROP INDEX audit_public_contributions;DROP TRIGGER browse_search_UPDATE;UPDATE tracks SET payload='broken json';INSERT INTO audit(actor,action,entity_id,changes,created_at,actor_type) VALUES('old','tag.create','legacy','broken json','old','local');INSERT OR REPLACE INTO meta VALUES('browse/schema_version','1');`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(path, media)
	if err != nil {
		t.Fatal("derived indexing blocked valid maintenance/startup", err)
	}
	defer s.Close()
	var raw string
	if err := s.db.QueryRow("SELECT payload FROM tracks WHERE id=?", track.ID).Scan(&raw); err != nil || raw != "broken json" {
		t.Fatal("source row rewritten", raw, err)
	}
}

func TestRound2OfflineAdminSkipsPerformanceDDL(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "media")
	os.Mkdir(media, 0700)
	s, err := NewOfflineStore(filepath.Join(root, "data.sqlite"), media)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='browse_track_search'").Scan(&count); err != nil || count != 0 {
		t.Fatal("offline management built runtime indexes", count, err)
	}
}
