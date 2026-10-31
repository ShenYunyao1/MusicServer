package music

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"musicserver/internal/diskspace"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"time"
)

func validTestAudio(t *testing.T) []byte {
	t.Helper()
	data := make([]byte, 44+800)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], 8000)
	binary.LittleEndian.PutUint32(data[28:32], 16000)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:44], 800)
	for index := 44; index < len(data); index++ {
		data[index] = byte(index)
	}
	return data
}

func TestPermanentTrackIdentityAndNoopScan(t *testing.T) {
	api, root := testAPI(t)
	before := api.store.Tracks()[0]
	if _, err := uuid.Parse(before.ID); err != nil {
		t.Fatalf("track ID must be UUID: %q", before.ID)
	}
	if err := api.store.PutPlaylist(Playlist{ID: "identity", Name: "Identity", TrackIDs: []string{before.ID}, TrackNotes: map[string]string{before.ID: "keep"}}); err != nil {
		t.Fatal(err)
	}
	if err := api.store.UpdateTrack(before.ID, map[string]string{"title": "Manual"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, before.Path), filepath.Join(root, "Moved.mp3")); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	after := api.store.Tracks()[0]
	playlist, _ := api.store.Playlist("identity")
	if after.ID != before.ID || after.Title != "Manual" || playlist.TrackNotes[before.ID] != "keep" {
		t.Fatalf("move lost identity: %+v %+v", after, playlist)
	}
	revision := api.store.Revision()
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	if api.store.Revision() != revision {
		t.Fatal("unchanged scan incremented revision")
	}
}

func TestAmbiguousMoveDoesNotMergeTracks(t *testing.T) {
	api, root := testAPI(t)
	original := api.store.Tracks()[0]
	data, err := os.ReadFile(filepath.Join(root, original.Path))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Copy.mp3"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	before := api.store.Tracks()
	for _, track := range before {
		if err := os.Remove(filepath.Join(root, track.Path)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "Ambiguous.mp3"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	after := api.store.Tracks()
	if len(after) != 3 {
		t.Fatalf("ambiguous identical tracks merged: %+v", after)
	}
	for _, track := range before {
		retained, ok := api.store.Track(track.ID)
		if !ok || !retained.Missing {
			t.Fatal("missing identity discarded")
		}
	}
}

func TestRestoreKeepsPlaylistAndManualMetadata(t *testing.T) {
	api, root := testAPI(t)
	track := api.store.Tracks()[0]
	if err := api.store.UpdateTrack(track.ID, map[string]string{"title": "Manual"}); err != nil {
		t.Fatal(err)
	}
	if err := api.store.PutPlaylist(Playlist{ID: "restore", Name: "Restore", TrackIDs: []string{track.ID}, TrackNotes: map[string]string{track.ID: "cue"}}); err != nil {
		t.Fatal(err)
	}
	if err := api.store.DeleteTrack(track.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.store.Track(track.ID); ok {
		t.Fatal("removed track still visible")
	}
	if len(api.store.RemovedTracks()) != 1 {
		t.Fatal("removed track not listed")
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := api.store.RestoreTrack(track.ID); err != nil {
		t.Fatal(err)
	}
	restored, ok := api.store.Track(track.ID)
	playlist, _ := api.store.Playlist("restore")
	if !ok || restored.ID != track.ID || restored.Title != "Manual" || playlist.TrackNotes[track.ID] != "cue" {
		t.Fatalf("restore lost associations: %+v %+v", restored, playlist)
	}
	if _, err := os.Stat(filepath.Join(root, track.Path)); err != nil {
		t.Fatal(err)
	}
}

func TestPartialUpdateDoesNotRewriteUnrelatedRows(t *testing.T) {
	api, root := testAPI(t)
	if err := os.WriteFile(filepath.Join(root, "Other.mp3"), []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	tracks := api.store.Tracks()
	target, other := tracks[0], tracks[1]
	if err := api.store.SetTrackCover(target.ID, tinyPNG()); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.db.Exec(fmt.Sprintf("CREATE TRIGGER reject_unrelated BEFORE INSERT ON tracks WHEN NEW.id='%s' BEGIN SELECT RAISE(ABORT,'unrelated rewritten'); END", other.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.db.Exec("CREATE TRIGGER reject_cover_rewrite BEFORE INSERT ON cover_assets BEGIN SELECT RAISE(ABORT,'cover rewritten'); END"); err != nil {
		t.Fatal(err)
	}
	if err := api.store.UpdateTrack(target.ID, map[string]string{"title": "Only target"}); err != nil {
		t.Fatal(err)
	}
	after, _ := api.store.Track(other.ID)
	if !reflect.DeepEqual(other, after) {
		t.Fatal("unrelated track changed")
	}
}

func TestFailedTransactionRollsBackAuditAndChanges(t *testing.T) {
	api, _ := testAPI(t)
	track := api.store.Tracks()[0]
	before := api.store.Revision()
	entries, err := api.store.Audit(100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.db.Exec("CREATE TRIGGER reject_audit BEFORE INSERT ON audit BEGIN SELECT RAISE(ABORT,'audit failed'); END"); err != nil {
		t.Fatal(err)
	}
	if err := api.store.UpdateTrack(track.ID, map[string]string{"title": "Must roll back"}); err == nil {
		t.Fatal("audit failure accepted")
	}
	after, _ := api.store.Track(track.ID)
	afterEntries, err := api.store.Audit(100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if api.store.Revision() != before || !reflect.DeepEqual(track, after) || len(entries) != len(afterEntries) {
		t.Fatal("transaction partially committed")
	}
}

func adminSync(t *testing.T, store *Store, cursor string, limit int) SyncResponse {
	t.Helper()
	response, err := store.SyncPage(testSyncIdentity(store), cursor, limit)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func TestIncrementalSyncRemovalAndRestore(t *testing.T) {
	api, _ := testAPI(t)
	track := api.store.Tracks()[0]
	initial := adminSync(t, api.store, "", 100)
	if !initial.FullSnapshot || initial.Cursor == "" {
		t.Fatal("initial sync is not a snapshot")
	}
	if err := api.store.UpdateTrack(track.ID, map[string]string{"title": "Changed"}); err != nil {
		t.Fatal(err)
	}
	delta := adminSync(t, api.store, initial.Cursor, 100)
	if delta.FullSnapshot || len(delta.Changed) != 1 || delta.Changed[0].Title != "Changed" {
		t.Fatalf("incorrect delta: %+v", delta)
	}
	if err := api.store.DeleteTrack(track.ID); err != nil {
		t.Fatal(err)
	}
	removed := adminSync(t, api.store, delta.Cursor, 100)
	if removed.FullSnapshot || len(removed.DeletedTracks) != 1 || removed.DeletedTracks[0] != track.ID {
		t.Fatalf("missing tombstone: %+v", removed)
	}
	if err := api.store.RestoreTrack(track.ID); err != nil {
		t.Fatal(err)
	}
	restored := adminSync(t, api.store, removed.Cursor, 100)
	if restored.FullSnapshot || len(restored.Changed) != 1 || restored.Changed[0].ID != track.ID {
		t.Fatalf("missing restoration: %+v", restored)
	}
	unchanged := adminSync(t, api.store, restored.Cursor, 100)
	if unchanged.FullSnapshot || len(unchanged.Changed) != 0 || len(unchanged.Playlists) != 0 {
		t.Fatal("unchanged cursor returned data")
	}
}

func TestSyncPaginationAndConcurrentMutation(t *testing.T) {
	api, _ := testAPI(t)
	first := adminSync(t, api.store, "", 1)
	if !first.FullSnapshot || !first.HasMore {
		t.Fatalf("missing pagination: %+v", first)
	}
	second := adminSync(t, api.store, first.Cursor, 1)
	if second.HasMore || !second.FullSnapshot {
		t.Fatalf("snapshot end incorrect: %+v", second)
	}
	partial := adminSync(t, api.store, "", 1)
	track := api.store.Tracks()[0]
	if err := api.store.UpdateTrack(track.ID, map[string]string{"comment": "During pagination"}); err != nil {
		t.Fatal(err)
	}
	_, err := api.store.SyncPage(testSyncIdentity(api.store), partial.Cursor, 1)
	if !errors.Is(err, errSyncRestart) {
		t.Fatalf("mixed revision snapshot accepted: %v", err)
	}
	token := partial.Cursor[:len(partial.Cursor)-5] + "XXXXX"
	if _, err := api.store.SyncPage(testSyncIdentity(api.store), token, 1); err == nil {
		t.Fatal("tampered cursor accepted")
	}
}

func TestSyncAuthorizationChangesResetSnapshot(t *testing.T) {
	api, _ := testAPI(t)
	track := api.store.Tracks()[0]
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "sync test", AllowedPlaylistIDs: []string{"library"}})
	if err != nil {
		t.Fatal(err)
	}
	auth := testPlayerIdentity(t, api, key.Secret)
	initial, err := api.store.SyncPage(auth, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Changed) != 1 {
		t.Fatal("authorized track missing")
	}
	empty := []string{}
	if _, err := api.store.UpdateAPIKey(key.ID, APIKeyUpdateRequest{AllowedPlaylistIDs: &empty}); err != nil {
		t.Fatal(err)
	}
	auth = testPlayerIdentity(t, api, key.Secret)
	reset, err := api.store.SyncPage(auth, initial.Cursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reset.FullSnapshot || !reset.Reset || len(reset.Changed) != 0 || len(reset.Playlists) != 0 {
		t.Fatalf("revoked authorization retained data: %+v", reset)
	}
	emptyCursor := reset.Cursor
	if err := api.store.UpdateTrack(track.ID, map[string]string{"title": "Private title"}); err != nil {
		t.Fatal(err)
	}
	hidden, err := api.store.SyncPage(auth, emptyCursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden.Changed) != 0 || len(hidden.DeletedTracks) != 0 {
		t.Fatal("unrelated track identity leaked")
	}
}

func TestPlaylistMetadataSyncIsIncrementalForPlayer(t *testing.T) {
	api, _ := testAPI(t)
	track := api.store.Tracks()[0]
	if err := api.store.PutPlaylist(Playlist{ID: "granted", Name: "Granted", TrackIDs: []string{track.ID}}); err != nil {
		t.Fatal(err)
	}
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "sync test", AllowedPlaylistIDs: []string{"granted"}})
	if err != nil {
		t.Fatal(err)
	}
	auth := testPlayerIdentity(t, api, key.Secret)
	first, err := api.store.SyncPage(auth, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	if _, err := api.store.UpdatePlaylistMetadata("granted", &name, nil); err != nil {
		t.Fatal(err)
	}
	delta, err := api.store.SyncPage(auth, first.Cursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if delta.FullSnapshot || delta.Reset || len(delta.Playlists) != 1 || delta.Playlists[0].Name != name {
		t.Fatalf("playlist metadata forced snapshot: %+v", delta)
	}
}

func TestSnapshotResetOnlyOnFirstPage(t *testing.T) {
	api, _ := testAPI(t)
	first := adminSync(t, api.store, "", 1)
	if !first.Reset {
		t.Fatal("initial page missing reset")
	}
	second := adminSync(t, api.store, first.Cursor, 1)
	if second.Reset {
		t.Fatal("continuation would discard earlier page")
	}
	delta := adminSync(t, api.store, second.Cursor, 1)
	if delta.Reset {
		t.Fatal("unchanged delta reset snapshot")
	}
}

func TestQuickScanSeesSidecarChangesAndKeepsRevisionStable(t *testing.T) {
	api, root := testAPI(t)
	if _, err := api.store.StartScan(false); err != nil {
		t.Fatal(err)
	}
	waitScan(t, api.store)
	before := api.store.Revision()
	if _, err := api.store.StartScan(false); err != nil {
		t.Fatal(err)
	}
	waitScan(t, api.store)
	if api.store.Revision() != before {
		t.Fatal("quick scan changed revision")
	}
	if err := os.WriteFile(filepath.Join(root, "Artist - Song.lrc"), []byte("[00:01]Changed sidecar"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.StartScan(false); err != nil {
		t.Fatal(err)
	}
	waitScan(t, api.store)
	if track := api.store.Tracks()[0]; track.Lyrics != "[00:01]Changed sidecar" {
		t.Fatalf("quick scan missed sidecar: %+v", track)
	}
}

func TestScanFailureAndCancellationPreserveLibrary(t *testing.T) {
	api, _ := testAPI(t)
	before := api.store.Tracks()
	revision := api.store.Revision()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := api.store.runScan(ctx, true, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored: %v", err)
	}
	original := api.store.mediaRoot
	api.store.mediaRoot = filepath.Join(original, "missing-root")
	if _, err := api.store.Scan(); err == nil {
		t.Fatal("unavailable root accepted")
	}
	api.store.mediaRoot = original
	if api.store.Revision() != revision || !reflect.DeepEqual(before, api.store.Tracks()) {
		t.Fatal("cancel/failure changed library")
	}
}

func waitScan(t *testing.T, store *Store) ScanStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		status := store.ScanStatus()
		if status.State != "running" {
			return status
		}
		if time.Now().After(deadline) {
			store.CancelScan()
			t.Fatal("scan timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func TestBackgroundScanPreservesConcurrentEdits(t *testing.T) {
	api, _ := testAPI(t)
	track := api.store.Tracks()[0]
	if _, err := api.store.StartScan(true, MutationOptions{Actor: "scanner"}); err != nil {
		t.Fatal(err)
	}
	if err := api.store.UpdateTrack(track.ID, map[string]string{"title": "Concurrent title"}, MutationOptions{Actor: "editor"}); err != nil {
		t.Fatal(err)
	}
	status := waitScan(t, api.store)
	if status.State != "completed" {
		t.Fatalf("scan failed: %+v", status)
	}
	after, ok := api.store.Track(track.ID)
	if !ok || after.Title != "Concurrent title" {
		t.Fatalf("scan overwrote edit: %+v", after)
	}
}

func TestInvalidAudioRejectedAndVerifiedPreview(t *testing.T) {
	api, _ := testAPI(t)
	before := api.store.Revision()
	if _, err := api.store.StageAudio("fake.mp3", strings.NewReader("not music"), nil); err == nil {
		t.Fatal("non-audio accepted")
	}
	if api.store.Revision() != before {
		t.Fatal("invalid upload changed library")
	}
	stage, err := api.store.StageAudio("valid.wav", bytes.NewReader(validTestAudio(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if stage.Validation != "audio_stream_verified" {
		t.Fatal("probe status missing")
	}
}

func TestHTTPConditionalMutationAndAuditActor(t *testing.T) {
	api, _ := testAPI(t)
	track := api.store.Tracks()[0]
	request := httptest.NewRequest(http.MethodPatch, "/api/v2/tracks/"+track.ID, strings.NewReader(`{"title":"Changed"}`))
	authorizeTestAdmin(api, request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 428 {
		t.Fatalf("missing version accepted: %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPatch, "/api/v2/tracks/"+track.ID, strings.NewReader(`{"title":"Changed"}`))
	authorizeTestAdmin(api, request)
	request.Header.Set("If-Match", fmt.Sprint(track.Version))
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("conditional write failed: %d %s", response.Code, response.Body)
	}
	request = httptest.NewRequest(http.MethodPatch, "/api/v2/tracks/"+track.ID, strings.NewReader(`{"title":"Stale"}`))
	authorizeTestAdmin(api, request)
	request.Header.Set("If-Match", fmt.Sprint(track.Version))
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 409 {
		t.Fatalf("stale write accepted: %d %s", response.Code, response.Body)
	}
	entries, err := api.store.Audit(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Actor != "admin" || entries[0].ActorType != "session" || entries[0].UserID == "" || entries[0].Action != "track.update" {
		t.Fatalf("actor not audited: %+v", entries)
	}
}

func TestDownloadDoesNotConsumeMetadataConcurrency(t *testing.T) {
	api, _ := testAPI(t)
	track := api.store.Tracks()[0]
	_, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "independent", AllowedPlaylistIDs: []string{"library"}, MaxConcurrency: 1, MaxDownloads: 1})
	writer := &blockingWriter{header: make(http.Header), started: make(chan struct{}), release: make(chan struct{})}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/media/"+track.ID, nil)
	request.Header.Set("X-API-Key", secret)
	done := make(chan struct{})
	go func() { api.ServeHTTP(writer, request); close(done) }()
	defer func() { close(writer.release); <-done }()
	select {
	case <-writer.started:
	case <-time.After(3 * time.Second):
		t.Fatal("download not started")
	}
	response := callAPIWithKey(api, http.MethodGet, "/api/v2/playlists", "", secret)
	if response.Code != 200 {
		t.Fatalf("download blocked metadata: %d %s", response.Code, response.Body)
	}
	response = callAPIWithKey(api, http.MethodGet, "/api/v2/media/"+track.ID, "", secret)
	if response.Code != 429 {
		t.Fatalf("download limit not enforced: %d", response.Code)
	}
}

func TestBackupCanRestoreDatabase(t *testing.T) {
	api, root := testAPI(t)
	track := api.store.Tracks()[0]
	if err := api.store.UpdateTrack(track.ID, map[string]string{"title": "Backed up"}); err != nil {
		t.Fatal(err)
	}
	path, err := api.store.Backup()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewStore(path, root)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	after, ok := restored.Track(track.ID)
	if !ok || after.Title != "Backed up" {
		t.Fatal("backup lost track")
	}
	var integrity string
	if err := restored.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("backup damaged: %s %v", integrity, err)
	}
}

func TestAccountDeploymentAndLegacyKeyRejection(t *testing.T) {
	for _, item := range []struct {
		address, url string
		valid        bool
	}{
		{"127.0.0.1:8080", "", true}, {"0.0.0.0:8080", "", true}, {"0.0.0.0:8080", "http://music.example.com", false}, {"0.0.0.0:8080", "https://music.example.com", true},
		{"127.0.0.1:8080", "http://music.example.com", false}, {"127.0.0.1:8080", "http://localhost:8080", true},
	} {
		if _, err := NewAuthConfig(item.address, item.url); (err == nil) != item.valid {
			t.Fatalf("deployment policy: %+v, %v", item, err)
		}
	}
	config, err := NewAuthConfig("127.0.0.1:8080", "https://Music.Example:443/")
	if err != nil || config.Origin != "https://music.example" || !config.Secure {
		t.Fatalf("public origin normalization: %+v, %v", config, err)
	}
	api, _ := testAPI(t)
	request := httptest.NewRequest("GET", "/api/v2/tracks", nil)
	request.Header.Set("X-API-Key", "secret")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("old startup key accepted")
	}
}

func TestLegacyRoutesAreNotServed(t *testing.T) {
	api, _ := testAPI(t)
	if response := callAPI(api, "GET", "/api/v1/tracks", ""); response.Code != 404 {
		t.Fatalf("legacy route remains: %d", response.Code)
	}
}

func TestRestoreHTTPRequiresLibraryWrite(t *testing.T) {
	api, _ := testAPI(t)
	track := api.store.Tracks()[0]
	if err := api.store.DeleteTrack(track.ID); err != nil {
		t.Fatal(err)
	}
	_, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "reader-admin", Scopes: []string{ScopeLibraryRead}})
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/removed/"+track.ID, nil)
	request.Header.Set("X-API-Key", secret)
	request.Header.Set("If-Match", fmt.Sprint(track.Version+1))
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatalf("restore scope bypass: %d", response.Code)
	}
}

func TestSchemaUUIDConstraint(t *testing.T) {
	api, _ := testAPI(t)
	err := api.store.transaction(func(tx *sql.Tx) error { return writeTrack(tx, Track{ID: "old-path-hash", Path: "x.mp3"}) })
	if err == nil {
		t.Fatal("legacy identity accepted")
	}
}

func TestSyncJSONHasExplicitDeletionArrays(t *testing.T) {
	api, _ := testAPI(t)
	response := adminSync(t, api.store, "", 100)
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"deleted_tracks":[]`)) || !bytes.Contains(raw, []byte(`"deleted_playlists":[]`)) {
		t.Fatal("deletion arrays missing")
	}
}

func TestPlaylistCreateCannotOverwriteAndOrderConflicts(t *testing.T) {
	api, _ := testAPI(t)
	if err := api.store.PutPlaylist(Playlist{ID: "existing", Name: "Original"}); err != nil {
		t.Fatal(err)
	}
	response := callAPI(api, http.MethodPost, "/api/v2/playlists", `{"id":"existing","name":"Overwritten"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate creation accepted: %d %s", response.Code, response.Body)
	}
	playlist, _ := api.store.Playlist("existing")
	if playlist.Name != "Original" {
		t.Fatal("duplicate creation overwrote playlist")
	}
	response = callAPI(api, http.MethodPut, "/api/v2/playlists/order", `{"order":["existing","library"],"expected_order":["library"]}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale order returned %d: %s", response.Code, response.Body)
	}
}

func TestFailedScanReportsNoCommittedChanges(t *testing.T) {
	api, root := testAPI(t)
	if err := os.WriteFile(filepath.Join(root, "0-New.wav"), validTestAudio(t), 0600); err != nil {
		t.Fatal(err)
	}
	revision := api.store.Revision()
	if _, err := api.store.db.Exec(`CREATE TRIGGER fail_scan BEFORE UPDATE ON scan_stamps BEGIN SELECT RAISE(ABORT,'rollback scan'); END`); err != nil {
		t.Fatal(err)
	}
	changed, err := api.store.runScan(context.Background(), true, nil)
	if err == nil || changed != 0 || api.store.Revision() != revision {
		t.Fatalf("rolled-back scan reported changes: %d, %v", changed, err)
	}
}

func TestUploadRechecksDuplicateInsideCommit(t *testing.T) {
	api, root := testAPI(t)
	preview, err := api.store.StageAudio("race.wav", bytes.NewReader(validTestAudio(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	locked := false
	lockReady := make(chan struct{})
	var once sync.Once
	api.store.spaceProbe = func(string) (diskspace.Space, error) {
		once.Do(func() { api.store.writeMu.Lock(); close(lockReady) })
		return diskspace.Space{Total: 1 << 40, Available: 1 << 40}, nil
	}
	defer func() {
		if locked {
			api.store.writeMu.Unlock()
		}
	}()
	type outcome struct {
		track     Track
		duplicate bool
		err       error
	}
	done := make(chan outcome, 1)
	go func() {
		track, duplicate, err := api.store.CommitUploadStage(preview.ID)
		done <- outcome{track, duplicate, err}
	}()
	select {
	case <-lockReady:
		locked = true
	case result := <-done:
		t.Fatalf("upload stopped before publication: %v", result.err)
	case <-time.After(3 * time.Second):
		t.Fatal("publication did not reach reserve check")
	}
	finalPath := filepath.Join(root, "uploads", preview.ID+".wav")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(finalPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upload was not prepared")
		}
		time.Sleep(time.Millisecond)
	}
	existing := preview.Track
	existing.OwnerID, existing.UploadedBy = api.store.Tracks()[0].OwnerID, api.store.Tracks()[0].UploadedBy
	existing.ID, existing.Path, existing.Version = uuid.NewString(), "external.wav", 1
	if err := os.WriteFile(filepath.Join(root, existing.Path), validTestAudio(t), 0600); err != nil {
		t.Fatal(err)
	}
	tx, err := api.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := writeTrack(tx, existing); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	api.store.writeMu.Unlock()
	locked = false
	select {
	case result := <-done:
		if result.err != nil || !result.duplicate || result.track.ID != existing.ID {
			t.Fatalf("commit missed concurrent duplicate: %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not complete")
	}
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("duplicate left published file: %v", err)
	}
}

func TestUploadAndScanPreserveOneTrackIdentity(t *testing.T) {
	api, _ := testAPI(t)
	preview, err := api.store.StageAudio("concurrent.wav", bytes.NewReader(validTestAudio(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.StartScan(true); err != nil {
		t.Fatal(err)
	}
	track, duplicate, err := api.store.CommitUploadStage(preview.ID)
	if err != nil || duplicate {
		t.Fatalf("concurrent commit failed: %v, %t", err, duplicate)
	}
	if status := waitScan(t, api.store); status.State != "completed" {
		t.Fatalf("concurrent scan failed: %+v", status)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	after, ok := api.store.Track(track.ID)
	if !ok || after.Missing || len(api.store.Tracks()) != 2 {
		t.Fatalf("scan changed upload identity: %+v", after)
	}
}

func TestUploadWithoutProbeIsExplicitlyRejected(t *testing.T) {
	api, _ := testAPI(t)
	t.Setenv("PATH", "")
	t.Setenv("MUSIC_SERVER_FFPROBE", "")
	_, err := api.store.StageAudio("unverified.wav", bytes.NewReader(validTestAudio(t)), nil)
	if err == nil || !strings.Contains(err.Error(), "FFPROBE_REQUIRED") {
		t.Fatalf("missing probe was not reported: %v", err)
	}
}
