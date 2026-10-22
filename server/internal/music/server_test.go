package music

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func testAPI(t *testing.T) (*API, string) {
	t.Helper()
	mediaRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(mediaRoot, "Artist - Song.mp3"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(filepath.Join(t.TempDir(), "library.sqlite"), mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.InitializeAdmin("admin", "test administrator password"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	return NewAPI(store), mediaRoot
}

func TestHealthAndPlaylist(t *testing.T) {
	api, _ := testAPI(t)
	server := httptest.NewServer(api)
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v2/playlists", nil)
	authorizeTestAdmin(api, request)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "All music") {
		t.Fatalf("playlist response = %s", body)
	}
}

func TestMediaSupportsRange(t *testing.T) {
	api, _ := testAPI(t)
	track := api.store.Tracks()[0]
	server := httptest.NewServer(api)
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v2/media/"+track.ID, nil)
	authorizeTestAdmin(api, request)
	request.Header.Set("Range", "bytes=2-5")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if string(body) != "2345" {
		t.Fatalf("body = %q", body)
	}
}

func TestAPIKeyRequired(t *testing.T) {
	api, _ := testAPI(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/playlists", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}

func callAPI(api *API, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	authorizeTestAdmin(api, request)
	addTestVersion(api, request)
	addTestVersion(api, request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func callAPIWithKey(api *API, method, path, body, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if key == "secret" {
		authorizeTestAdmin(api, request)
	} else if key != "" {
		request.Header.Set("X-API-Key", key)
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

var testSessions sync.Map

func authorizeTestAdmin(api *API, request *http.Request) {
	value, ok := testSessions.Load(api)
	if !ok {
		_, _, token, err := api.store.Login("admin", "test administrator password", "test-fixture")
		if err != nil {
			panic(err)
		}
		value = token
		testSessions.Store(api, token)
	}
	token := value.(string)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("X-CSRF-Token", csrfForToken(token))
}

func createPersistentKey(t *testing.T, api *API, input APIKeyCreateRequest) (APIKeyView, string) {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response := callAPI(api, http.MethodPost, "/api/v2/auth/keys", string(body))
	if response.Code != http.StatusCreated {
		t.Fatalf("create key: %d %s", response.Code, response.Body.String())
	}
	var view APIKeyView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Secret == "" || view.SecretHash != "" {
		t.Fatalf("unexpected key response: %+v", view)
	}
	return view, view.Secret
}

func TestAPIKeyAuthorizationAndRedaction(t *testing.T) {
	api, _ := testAPI(t)
	track := api.store.Tracks()[0]
	playlist := Playlist{ID: "favorites", Name: "Favorites", TrackIDs: []string{track.ID}}
	playlistBody, _ := json.Marshal(playlist)
	if response := callAPI(api, http.MethodPost, "/api/v2/playlists", string(playlistBody)); response.Code != http.StatusCreated {
		t.Fatalf("create playlist: %d %s", response.Code, response.Body.String())
	}
	view, playerSecret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "Player"})

	response := callAPIWithKey(api, http.MethodGet, "/api/v2/auth/keys", "", "secret")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "secret_hash") {
		t.Fatalf("key list leaked hash or failed: %d %s", response.Code, response.Body.String())
	}
	response = callAPIWithKey(api, http.MethodGet, "/api/v2/playlists", "", playerSecret)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "Favorites") {
		t.Fatalf("unassigned player can read playlist: %d %s", response.Code, response.Body.String())
	}
	response = callAPIWithKey(api, http.MethodGet, "/api/v2/media/"+track.ID, "", playerSecret)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "TRACK_NOT_FOUND") {
		t.Fatalf("unassigned player media access: %d %s", response.Code, response.Body.String())
	}

	authorized := []string{playlist.ID}
	updateBody, _ := json.Marshal(APIKeyUpdateRequest{AllowedPlaylistIDs: &authorized})
	response = callAPIWithKey(api, http.MethodPatch, "/api/v2/auth/keys/"+view.ID, string(updateBody), "secret")
	if response.Code != http.StatusOK {
		t.Fatalf("authorize key: %d %s", response.Code, response.Body.String())
	}
	response = callAPIWithKey(api, http.MethodGet, "/api/v2/playlists/"+playlist.ID, "", playerSecret)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Favorites") {
		t.Fatalf("authorized playlist access: %d %s", response.Code, response.Body.String())
	}
	response = callAPIWithKey(api, http.MethodGet, "/api/v2/media/"+track.ID, "", playerSecret)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized media access: %d %s", response.Code, response.Body.String())
	}

	response = callAPIWithKey(api, http.MethodDelete, "/api/v2/auth/keys/"+view.ID, "", "secret")
	if response.Code != http.StatusNoContent {
		t.Fatalf("revoke key: %d %s", response.Code, response.Body.String())
	}
	response = callAPIWithKey(api, http.MethodGet, "/api/v2/playlists", "", playerSecret)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key status: %d %s", response.Code, response.Body.String())
	}
	var errorResponse struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &errorResponse); err != nil {
		t.Fatal(err)
	}
	if errorResponse.Error.Code != "AUTH_INVALID" || errorResponse.Error.Message == "" || errorResponse.Error.RequestID == "" {
		t.Fatalf("structured auth error missing fields: %s", response.Body.String())
	}
}

func TestMetadataPlaylistAndDeletionPersistAcrossScan(t *testing.T) {
	api, root := testAPI(t)
	track := api.store.Tracks()[0]
	if response := callAPI(api, "PATCH", "/api/v2/tracks/"+track.ID, `{"title":"Edited","lyrics":"[00:01]Words","comment":"Note","genre":"Pop"}`); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	playlist := Playlist{ID: "custom", Name: "Favorites", Description: "Playlist note", TrackIDs: []string{track.ID}, TrackNotes: map[string]string{track.ID: "Track in playlist note"}}
	content, _ := json.Marshal(playlist)
	if response := callAPI(api, "POST", "/api/v2/playlists", string(content)); response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(api.store.dataPath, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	updated, _ := reloaded.Track(track.ID)
	if updated.Title != "Edited" || updated.Lyrics != "[00:01]Words" || updated.Comment != "Note" {
		t.Fatalf("metadata lost: %+v", updated)
	}
	stored, _ := reloaded.Playlist("custom")
	if stored.TrackNotes[track.ID] != "Track in playlist note" {
		t.Fatal("playlist note lost")
	}
	stored.TrackNotes[track.ID] = "external mutation"
	storedAgain, _ := reloaded.Playlist("custom")
	if storedAgain.TrackNotes[track.ID] == "external mutation" {
		t.Fatal("playlist exposed mutable storage")
	}
	if response := callAPI(api, "DELETE", "/api/v2/tracks/"+track.ID, ""); response.Code != 204 {
		t.Fatal(response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, track.Path)); err != nil {
		t.Fatal("original file removed")
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	if _, exists := api.store.Track(track.ID); exists {
		t.Fatal("deleted track returned after scan")
	}
	snapshot, err := api.store.SyncPage(testSyncIdentity(api.store), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.FullSnapshot {
		t.Fatal("changed revision did not return snapshot")
	}
	current, err := api.store.SyncPage(testSyncIdentity(api.store), snapshot.Cursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if current.FullSnapshot {
		t.Fatal("unchanged revision returned snapshot")
	}
	if response := callAPI(api, "DELETE", "/api/v2/playlists/custom", ""); response.Code != 204 {
		t.Fatal(response.Body.String())
	}
}

func TestUploadExceedsMetadataBodyLimit(t *testing.T) {
	api, _ := testAPI(t)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "Uploaded.mp3")
	if err != nil {
		t.Fatal(err)
	}
	audio := bytes.Repeat(validTestAudio(t), 4000)
	_, _ = part.Write(audio)
	_ = form.Close()
	request := httptest.NewRequest("POST", "/api/v2/uploads", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	authorizeTestAdmin(api, request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 201 {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	var track Track
	if err := json.Unmarshal(response.Body.Bytes(), &track); err != nil {
		t.Fatal(err)
	}
	if track.Size != int64(len(audio)) || track.Title != "Uploaded" {
		t.Fatalf("upload metadata: %+v", track)
	}
	if len(api.store.Tracks()) != 2 {
		t.Fatal("upload missing from library")
	}
}

func TestFailedSaveRollsBackRevisionAndMetadata(t *testing.T) {
	api, _ := testAPI(t)
	track := api.store.Tracks()[0]
	revision := api.store.Revision()
	if _, err := api.store.db.Exec("CREATE TRIGGER reject_update BEFORE INSERT ON tracks BEGIN SELECT RAISE(ABORT,'reject'); END"); err != nil {
		t.Fatal(err)
	}
	if err := api.store.UpdateTrack(track.ID, map[string]string{"title": "Rejected"}); err == nil {
		t.Fatal("expected transaction failure")
	}
	actual, _ := api.store.Track(track.ID)
	if actual.Title != track.Title || api.store.Revision() != revision {
		t.Fatal("failed save changed data")
	}
}

func TestMediaCannotFollowExternalSymlink(t *testing.T) {
	api, root := testAPI(t)
	track := api.store.Tracks()[0]
	outside := filepath.Join(t.TempDir(), "private.mp3")
	if err := os.WriteFile(outside, []byte("private data"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.mp3")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	track.Path = "link.mp3"
	if err := api.store.transaction(func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	response := callAPI(api, "GET", "/api/v2/media/"+track.ID, "")
	if response.Code == 200 || strings.Contains(response.Body.String(), "private data") {
		t.Fatal("media escaped root")
	}
}

func TestLyricsAssociationAndContainment(t *testing.T) {
	api, root := testAPI(t)
	lyricsPath := filepath.Join(root, "Artist - Song.lrc")
	if err := os.WriteFile(lyricsPath, []byte("\xef\xbb\xbf[00:01]Words"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	if api.store.Tracks()[0].Lyrics != "[00:01]Words" {
		t.Fatal("matching UTF-8 lyrics not associated")
	}
	outside := filepath.Join(t.TempDir(), "private.lrc")
	if err := os.WriteFile(outside, []byte("private lyrics"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lyricsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, lyricsPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	if api.store.Tracks()[0].Lyrics != "" {
		t.Fatal("lyrics escaped root")
	}
}

func addTestVersion(api *API, request *http.Request) {
	if !needsVersion(request) {
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/api/v2/")
	parts := strings.Split(path, "/")
	if parts[0] == "tracks" && len(parts) > 1 {
		if track, ok := api.store.Track(parts[1]); ok {
			request.Header.Set("If-Match", fmt.Sprint(track.Version))
		}
	}
	if parts[0] == "playlists" && len(parts) > 1 {
		if playlist, ok := api.store.Playlist(parts[1]); ok {
			request.Header.Set("If-Match", fmt.Sprint(playlist.Version))
		}
	}
}
