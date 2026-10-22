package music

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDuplicateDirectUploadDoesNotRetainUnusedArtwork(t *testing.T) {
	api, root := testAPI(t)
	audio := validTestAudio(t)
	track, err := api.store.ImportAudio("original.mp3", bytes.NewReader(audio))
	if err != nil {
		t.Fatal(err)
	}
	before := databaseState(t, api.store)
	if err := os.WriteFile(filepath.Join(root, "uploads", "cover.png"), tinyPNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	duplicate, err := api.store.ImportAudio("again.mp3", bytes.NewReader(audio))
	if err != nil || duplicate.ID != track.ID {
		t.Fatalf("duplicate import failed: track=%+v err=%v", duplicate, err)
	}
	if !reflect.DeepEqual(before, databaseState(t, api.store)) {
		t.Fatal("duplicate import changed library or retained unused artwork")
	}
}

// This is the relational schema shipped in v0.5.0, kept independent of the
// current schema so an actual v1 database exercises the ALTER migration.
const sqliteV1BehaviorFixture = `
CREATE TABLE meta (key TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL);
CREATE TABLE tracks (id TEXT PRIMARY KEY NOT NULL, title TEXT NOT NULL, artist TEXT NOT NULL, album TEXT NOT NULL, genre TEXT NOT NULL, comment TEXT NOT NULL, lyrics TEXT NOT NULL, embedded_lyrics INTEGER NOT NULL DEFAULT 0 CHECK (embedded_lyrics IN (0,1)), lyrics_source TEXT NOT NULL DEFAULT '', path TEXT NOT NULL, size INTEGER NOT NULL, content_hash TEXT NOT NULL, format TEXT NOT NULL, modified_at TEXT NOT NULL);
CREATE TABLE playlists (id TEXT PRIMARY KEY NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE playlist_tracks (playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE, position INTEGER NOT NULL CHECK (position>=0), track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE, note TEXT, PRIMARY KEY (playlist_id,position));
CREATE INDEX playlist_tracks_track_id ON playlist_tracks(track_id);
CREATE TABLE api_keys (id TEXT PRIMARY KEY NOT NULL, name TEXT NOT NULL, secret_hash TEXT NOT NULL, role TEXT NOT NULL, device_id TEXT NOT NULL, max_devices INTEGER NOT NULL, max_concurrency INTEGER NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, last_used_at TEXT NOT NULL, revoked_at TEXT NOT NULL);
CREATE TABLE key_playlist_grants (key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE, playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE, position INTEGER NOT NULL CHECK (position>=0), PRIMARY KEY (key_id,playlist_id), UNIQUE(key_id,position));
CREATE INDEX key_playlist_grants_playlist_id ON key_playlist_grants(playlist_id);
CREATE TABLE scopes (key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE, position INTEGER NOT NULL CHECK(position>=0), scope TEXT NOT NULL, PRIMARY KEY(key_id,position));
CREATE TABLE metadata_overrides (track_id TEXT NOT NULL, field TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY(track_id,field));
CREATE TABLE excluded_tracks (track_id TEXT PRIMARY KEY NOT NULL, excluded INTEGER NOT NULL CHECK(excluded IN(0,1)));
INSERT INTO meta VALUES ('schema_version','1'),('revision','19');
INSERT INTO tracks VALUES ('track-a','Old title','Artist','Album','Pop','Comment','[00:01]Words',1,'ID3v2/USLT','old.mp3',12,'hash-a','mp3','2026-10-06T01:02:03Z');
INSERT INTO playlists VALUES ('mix','Mix','Description','2026-10-06T01:02:03Z');
INSERT INTO playlist_tracks VALUES ('mix',0,'track-a','Saved note');
INSERT INTO api_keys VALUES ('reader','Reader','','player','',1,2,'2026-10-06T01:02:03Z','','','');
INSERT INTO scopes VALUES ('reader',0,'library:read'),('reader',1,'media:read');
INSERT INTO key_playlist_grants VALUES ('reader','mix',0);
INSERT INTO metadata_overrides VALUES ('track-a','title','Old title');
INSERT INTO excluded_tracks VALUES ('previously-removed',1);
PRAGMA user_version=1;
`

func TestOldSchemaIsRejectedWithoutModification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(sqliteV1BehaviorFixture); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if store, err := NewStore(path, t.TempDir()); err == nil {
		_ = store.Close()
		t.Fatal("old schema accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("old database was modified")
	}
}

func TestV06SettingsPersistFalseAndRejectMediaRootChange(t *testing.T) {
	api, root := testAPI(t)
	settings := api.store.Settings()
	settings.ScanExternalLyrics, settings.ScanExternalCovers = false, false
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settings.FFprobePath = executable
	settings.MaxUploadBytes = 7 << 20
	if err := api.store.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	path := api.store.DataPath()
	if err := api.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(path, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if actual := reopened.Settings(); actual != settings {
		t.Fatalf("settings changed after restart: got=%+v want=%+v", actual, settings)
	}
	before := reopened.Revision()
	body, _ := json.Marshal(map[string]string{"media_root": t.TempDir()})
	response := callAPI(NewAPI(reopened), http.MethodPatch, "/api/v2/admin/settings", string(body))
	if response.Code != http.StatusBadRequest || reopened.Revision() != before || reopened.Settings().MediaRoot != root {
		t.Fatalf("read-only root was accepted: status=%d revision=%d response=%s", response.Code, reopened.Revision(), response.Body.String())
	}
}

func TestV06PlaylistOrderPersistsInListAndSync(t *testing.T) {
	api, root := testAPI(t)
	for _, playlist := range []Playlist{{ID: "a", Name: "A"}, {ID: "z", Name: "Z"}} {
		if err := api.store.PutPlaylist(playlist); err != nil {
			t.Fatal(err)
		}
	}
	order := []string{"library", "z", "a"}
	stale, _ := api.store.Playlist("z")
	if err := api.store.ReorderPlaylists(order); err != nil {
		t.Fatal(err)
	}
	// Editing membership or metadata must not overwrite a newer display order.
	stale.Description = "Edited after reordering"
	stale.TrackIDs = []string{api.store.Tracks()[0].ID}
	if err := api.store.PutPlaylist(stale); err != nil {
		t.Fatal(err)
	}
	check := func(store *Store) {
		t.Helper()
		for _, playlists := range [][]Playlist{store.Playlists()} {
			ids := make([]string, len(playlists))
			for i, playlist := range playlists {
				ids[i] = playlist.ID
			}
			if !reflect.DeepEqual(ids, order) {
				t.Fatalf("playlist ordering=%v want=%v", ids, order)
			}
		}
	}
	check(api.store)
	path := api.store.DataPath()
	if err := api.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(path, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	check(reopened)
}

func TestSessionAndAnonymousCallsAreExcludedAcrossRestart(t *testing.T) {
	api, root := testAPI(t)
	if response := callAPI(api, http.MethodGet, "/api/v2/tracks", ""); response.Code != http.StatusOK {
		t.Fatalf("startup admin request failed: %d", response.Code)
	}
	if response := callAPIWithKey(api, http.MethodGet, "/api/v2/tracks", "", "bad-key"); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous request status=%d", response.Code)
	}
	_ = callAPIWithKey(api, http.MethodGet, "/api/v2/health", "", "")
	check := func(api *API) {
		t.Helper()
		stats := api.readCallStats()
		if stats.err != nil || stats.Total.TotalRequests != 0 || stats.Total.SuccessRequests != 0 || stats.Total.FailureRequests != 0 {
			t.Fatalf("unexpected total stats: %+v err=%v", stats.Total, stats.err)
		}
		if len(stats.Keys) != 0 {
			t.Fatalf("non-key calls were persisted: %+v", stats.Keys)
		}
	}
	check(api)
	// Track updates must not turn management calls into player statistics.
	track := api.store.Tracks()[0]
	if err := api.store.UpdateTrack(track.ID, map[string]string{"comment": "Mutation"}); err != nil {
		t.Fatal(err)
	}
	check(api)
	path := api.store.DataPath()
	if err := api.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(path, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	check(NewAPI(reopened))
}

func TestV06FailedScanRollsBackArtworkAndLibrary(t *testing.T) {
	api, root := testAPI(t)
	before := databaseState(t, api.store)
	if err := os.WriteFile(filepath.Join(root, "Artist - Song.png"), tinyPNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.db.Exec("CREATE TRIGGER reject_artwork BEFORE INSERT ON cover_assets BEGIN SELECT RAISE(ABORT, 'test artwork write failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.Scan(); err == nil {
		t.Fatal("scan did not reach the rejecting artwork trigger")
	}
	after := databaseState(t, api.store)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed scan changed memory: covers=%d -> %d revision=%d -> %d", before.Covers, after.Covers, before.Revision, after.Revision)
	}
	persisted := databaseState(t, api.store)
	if persisted.Covers != before.Covers || persisted.Revision != before.Revision || !reflect.DeepEqual(persisted.Tracks, before.Tracks) {
		t.Fatal("failed scan left artwork or modified tracks in SQLite")
	}
	if _, err := api.store.db.Exec("DROP TRIGGER reject_artwork"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatalf("retry after artwork failure: %v", err)
	}
	track := api.store.Tracks()[0]
	if track.CoverID == "" {
		t.Fatal("successful retry did not assign artwork")
	}
	if _, exists := api.store.Cover(track.CoverID); !exists {
		t.Fatal("successful retry did not publish artwork asset")
	}
}

type testDatabaseState struct {
	Revision int64
	Tracks   []Track
	Covers   int
}

func databaseState(t *testing.T, store *Store) testDatabaseState {
	t.Helper()
	return testDatabaseState{store.Revision(), store.Tracks(), coverCount(t, store)}
}
