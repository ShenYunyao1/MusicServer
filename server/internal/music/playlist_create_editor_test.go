package music

import (
	"bytes"
	"encoding/json"
	"image/color"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createEditorRequest(t *testing.T, api *API, token, id, name, visibility string, cover []byte, modify ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for key, value := range map[string]string{"id": id, "name": name, "description": "Created with artwork", "visibility": visibility} {
		_ = form.WriteField(key, value)
	}
	if cover != nil {
		part, _ := form.CreateFormFile("cover", "cover.png")
		_, _ = part.Write(cover)
	}
	_ = form.Close()
	r := httptest.NewRequest("POST", "http://example.com/api/v2/playlists", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("X-CSRF-Token", csrfForToken(token))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	for _, change := range modify {
		change(r)
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}

func TestPlaylistCreateEditorAtomicAndCreateOnly(t *testing.T) {
	api, _ := testAPI(t)
	user, token := readyMember(t, api, "create-editor-user")
	cover := playlistImage(t, 16, 16, color.White)
	w := createEditorRequest(t, api, token, "new-with-artwork", "New playlist", "public", cover)
	var saved Playlist
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &saved) != nil {
		t.Fatalf("create editor: %d %s", w.Code, w.Body)
	}
	if saved.OwnerID != user.ID || saved.Version != 1 || saved.CoverID != hashBytes(cover) || saved.Visibility != "public" || saved.PublishedAt.IsZero() || saved.Description != "Created with artwork" {
		t.Fatalf("incomplete creation: %+v", saved)
	}
	if w := createEditorRequest(t, api, token, saved.ID, "Overwrite", "private", playlistImage(t, 16, 16, color.Black)); w.Code != 409 {
		t.Fatalf("duplicate create: %d %s", w.Code, w.Body)
	}
	current, _ := api.store.Playlist(saved.ID)
	if current.Name != saved.Name || current.CoverID != saved.CoverID || current.Version != 1 {
		t.Fatal("duplicate create changed the saved playlist")
	}
	var assets int
	_ = api.store.db.QueryRow("SELECT COUNT(*) FROM cover_assets").Scan(&assets)
	if assets != 1 {
		t.Fatal("failed create left artwork behind")
	}
	_, otherToken := readyMember(t, api, "create-other-user")
	if w := createEditorRequest(t, api, otherToken, saved.ID, "Other owner", "private", nil); w.Code != 404 {
		t.Fatalf("other owner creation collision: %d %s", w.Code, w.Body)
	}
	if err := api.store.DeletePlaylist(saved.ID, MutationOptions{UserID: user.ID, ExpectedVersion: saved.Version}); err != nil {
		t.Fatal(err)
	}
	if w := createEditorRequest(t, api, token, saved.ID, "Reused id", "private", nil); w.Code < 400 {
		t.Fatal("reused deleted playlist id")
	}
}

func TestPlaylistCreateEditorRequiresReadySessionAndCSRF(t *testing.T) {
	api, _ := testAPI(t)
	_, token := readyMember(t, api, "create-session-user")
	if w := createEditorRequest(t, api, token, "no-csrf", "Name", "private", nil, func(r *http.Request) { r.Header.Del("X-CSRF-Token") }); w.Code != 403 {
		t.Fatalf("missing CSRF: %d", w.Code)
	}
	key := communityKey(t, api, token, "library")
	if w := createEditorRequest(t, api, token, "key-create", "Name", "private", nil, func(r *http.Request) { r.Header.Del("Cookie"); r.Header.Set("X-API-Key", key) }); w.Code != 403 {
		t.Fatalf("player key creation: %d", w.Code)
	}
	temporary := createMemberFixture(t, api, "create-first-password")
	_, _, pending, err := api.store.Login(temporary.User.Username, temporary.Password, "create fixture")
	if err != nil {
		t.Fatal(err)
	}
	if w := createEditorRequest(t, api, pending, "pending-create", "Name", "private", nil); w.Code != 403 {
		t.Fatalf("first-password bypass: %d", w.Code)
	}
}

func TestPlaylistCreateEditorRejectsAndRollsBack(t *testing.T) {
	api, _ := testAPI(t)
	_, token := readyMember(t, api, "create-editor-invalid")
	for _, item := range []struct {
		id, name, visibility string
		cover                []byte
	}{
		{"empty-name", " ", "private", nil},
		{"invalid-cover", "Name", "private", []byte("not an image")},
		{"invalid-visibility", "Name", "invalid", nil},
		{"library", "Name", "private", nil},
	} {
		w := createEditorRequest(t, api, token, item.id, item.name, item.visibility, item.cover)
		if w.Code < 400 {
			t.Fatalf("accepted invalid creation %s: %d", item.id, w.Code)
		}
	}
	if _, err := api.store.db.Exec("CREATE TRIGGER reject_create_editor BEFORE INSERT ON audit WHEN NEW.action='playlist.create' BEGIN SELECT RAISE(ABORT,'audit fixture'); END"); err != nil {
		t.Fatal(err)
	}
	w := createEditorRequest(t, api, token, "rollback-create", "Rollback", "public", playlistImage(t, 16, 16, color.Black))
	if w.Code < 400 {
		t.Fatal("ignored audit failure")
	}
	var playlists, assets int
	_ = api.store.db.QueryRow("SELECT COUNT(*) FROM playlists WHERE system=0").Scan(&playlists)
	_ = api.store.db.QueryRow("SELECT COUNT(*) FROM cover_assets").Scan(&assets)
	if playlists != 0 || assets != 0 {
		t.Fatalf("failed creation persisted partial state: playlists=%d covers=%d", playlists, assets)
	}
}
