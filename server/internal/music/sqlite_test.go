package music

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func closeStoreForTest(t *testing.T, store *Store) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func sqliteConnection(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLiteRejectsJSONDataPath(t *testing.T) {
	directory := t.TempDir()
	jsonPath := filepath.Join(directory, "library.json")
	if err := os.WriteFile(jsonPath, []byte("legacy data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if store, err := NewStore(jsonPath, t.TempDir()); err == nil {
		closeStoreForTest(t, store)
		t.Fatal("JSON data path was accepted")
	}
	if _, err := os.Stat(filepath.Join(directory, "library.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("rejecting JSON path created a sibling SQLite file: %v", err)
	}
}

func TestSQLiteCreatesCleanDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "音乐 # library.sqlite")
	store, err := NewStore(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeStoreForTest(t, store) })
	if store.DataPath() != path {
		t.Fatalf("DataPath = %q, want %q", store.DataPath(), path)
	}
	persisted, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(persisted, []byte("SQLite format 3\x00")) {
		t.Fatalf("expected SQLite database header: %v", err)
	}
	if store.Revision() != 0 || len(store.Tracks()) != 0 {
		t.Fatal("new SQLite database was not empty")
	}
	db := sqliteConnection(t, path)
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != sqliteSchemaVersion {
		t.Fatalf("schema version = %d: %v", version, err)
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity = %q: %v", integrity, err)
	}
	var revision string
	if err := db.QueryRow("SELECT value FROM meta WHERE key = 'revision'").Scan(&revision); err != nil || revision != "0" {
		t.Fatalf("initial revision = %q: %v", revision, err)
	}
}

func seedStore(t *testing.T) (*Store, string, []Track) {
	t.Helper()
	mediaRoot := t.TempDir()
	for _, name := range []string{"First.mp3", "Second.mp3"} {
		if err := os.WriteFile(filepath.Join(mediaRoot, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "library.sqlite")
	store, err := NewStore(path, mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InitializeAdmin("admin", "test administrator password"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Scan(); err != nil {
		closeStoreForTest(t, store)
		t.Fatal(err)
	}
	return store, path, store.Tracks()
}

func TestSQLiteRelationsAndIntegrity(t *testing.T) {
	store, path, tracks := seedStore(t)
	t.Cleanup(func() { closeStoreForTest(t, store) })
	if err := store.PutPlaylist(Playlist{ID: "favorites", Name: "Favorites", TrackIDs: []string{tracks[0].ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(APIKeyCreateRequest{Name: "Player", AllowedPlaylistIDs: []string{"favorites"}}); err != nil {
		t.Fatal(err)
	}
	db := sqliteConnection(t, path)
	for _, table := range []string{"tracks", "playlists", "playlist_tracks", "api_keys", "key_playlist_grants", "scopes", "metadata_overrides", "meta"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("missing relation %s: %v", table, err)
		}
	}
	var foreignKeys int
	if err := store.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("store connection foreign_keys = %d: %v", foreignKeys, err)
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign key violation")
	}
	_ = rows.Close()
	if _, err := db.Exec("INSERT INTO key_playlist_grants (key_id, playlist_id, position) VALUES ('missing', 'favorites', 1)"); err == nil {
		t.Fatal("API key foreign key is not enforced")
	}
}

func TestSQLiteRejectsOrphanTrackNotes(t *testing.T) {
	store, _, tracks := seedStore(t)
	t.Cleanup(func() { closeStoreForTest(t, store) })
	if err := store.PutPlaylist(Playlist{ID: "invalid", Name: "Invalid", TrackIDs: []string{tracks[0].ID}, TrackNotes: map[string]string{tracks[1].ID: "orphan"}}); err == nil {
		t.Fatal("orphan track note was accepted")
	}
	if _, exists := store.Playlist("invalid"); exists {
		t.Fatal("invalid playlist was published")
	}
}

func TestSQLiteTransactionRollsBackEntireSnapshot(t *testing.T) {
	store, _, tracks := seedStore(t)
	t.Cleanup(func() { closeStoreForTest(t, store) })
	track := tracks[0]
	beforeRevision := store.Revision()
	before, _ := store.Track(track.ID)
	if _, err := store.db.Exec(`CREATE TRIGGER reject_title BEFORE INSERT ON tracks WHEN NEW.title = 'Rejected' BEGIN SELECT RAISE(ABORT, 'test write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTrack(track.ID, map[string]string{"title": "Rejected"}); err == nil {
		t.Fatal("expected SQLite insertion failure")
	}
	after, _ := store.Track(track.ID)
	if store.Revision() != beforeRevision || !reflect.DeepEqual(after, before) {
		t.Fatal("failed transaction changed live snapshot")
	}
	durable, err := readTrackFrom(store.db, track.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if apiRevisionForTest(store) != beforeRevision || !reflect.DeepEqual(durable, before) {
		t.Fatal("failed transaction left a partial durable snapshot")
	}
	if _, err := store.db.Exec("DROP TRIGGER reject_title"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTrack(track.ID, map[string]string{"title": "Accepted"}); err != nil {
		t.Fatalf("write after rollback failed: %v", err)
	}
}

func TestSQLiteRejectsNewerSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.sqlite")
	store, err := NewStore(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	closeStoreForTest(t, store)
	if reopened, err := NewStore(path, t.TempDir()); err == nil {
		closeStoreForTest(t, reopened)
		t.Fatal("newer schema version was silently downgraded")
	}
}

func TestSQLitePersistsUpdatesExclusionsAndKeyLifecycle(t *testing.T) {
	store, path, tracks := seedStore(t)
	if err := store.UpdateTrack(tracks[0].ID, map[string]string{"title": "Edited", "lyrics": "Stored words"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutPlaylist(Playlist{ID: "mix", Name: "Mix", TrackIDs: []string{tracks[1].ID, tracks[0].ID}, TrackNotes: map[string]string{tracks[0].ID: "Cue"}}); err != nil {
		t.Fatal(err)
	}
	view, err := store.CreateAPIKey(APIKeyCreateRequest{Name: "Player", Scopes: []string{"media:read"}, AllowedPlaylistIDs: []string{"mix"}})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := store.RotateAPIKey(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTrack(tracks[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePlaylist("mix"); err != nil {
		t.Fatal(err)
	}
	revision := store.Revision()
	closeStoreForTest(t, store)
	reopened, err := NewStore(path, store.mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Revision() != revision || !removedForTest(t, reopened, tracks[0].ID) || overrideValue(t, reopened, tracks[0].ID, "title") != "Edited" {
		closeStoreForTest(t, reopened)
		t.Fatal("revision, overrides, or exclusions lost after track deletion")
	}
	if _, ok := reopened.AuthenticateSecret(view.Secret); ok {
		closeStoreForTest(t, reopened)
		t.Fatal("old rotated secret accepted")
	}
	if _, ok := reopened.AuthenticateSecret(rotated.Secret); !ok {
		closeStoreForTest(t, reopened)
		t.Fatal("new secret did not survive reopening")
	}
	if grants := reopened.APIKeys()[0].AllowedPlaylistIDs; len(grants) != 0 {
		closeStoreForTest(t, reopened)
		t.Fatalf("deleted playlist grant retained: %v", grants)
	}
	if _, err := reopened.Scan(); err != nil {
		closeStoreForTest(t, reopened)
		t.Fatal(err)
	}
	if _, exists := reopened.Track(tracks[0].ID); exists {
		closeStoreForTest(t, reopened)
		t.Fatal("excluded track reappeared after reopening and scanning")
	}
	if err := reopened.RevokeAPIKey(view.ID); err != nil {
		closeStoreForTest(t, reopened)
		t.Fatal(err)
	}
	closeStoreForTest(t, reopened)
	final, err := NewStore(path, filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeStoreForTest(t, final) })
	if _, ok := final.AuthenticateSecret(rotated.Secret); ok {
		t.Fatal("revocation did not survive reopening")
	}
	if content, err := os.ReadFile(path); err != nil || strings.Contains(string(content), rotated.Secret) {
		t.Fatalf("raw key secret persisted: %v", err)
	}
}

func apiRevisionForTest(store *Store) int64 { return store.Revision() }
func removedForTest(t *testing.T, store *Store, id string) bool {
	t.Helper()
	track, err := readTrackFrom(store.db, id, true)
	if err != nil {
		t.Fatal(err)
	}
	return track.Removed
}
func overrideValue(t *testing.T, store *Store, id, field string) string {
	t.Helper()
	values, err := readOverrides(store.db, id)
	if err != nil {
		t.Fatal(err)
	}
	return values[field]
}
func coverCount(t *testing.T, store *Store) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM cover_assets").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
