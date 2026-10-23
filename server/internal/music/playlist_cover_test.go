package music

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func playlistImage(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, c)
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func editorRequest(t *testing.T, api *API, id, token string, version int64, name string, data []byte, remove bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("name", name)
	_ = form.WriteField("description", "New description")
	if remove {
		_ = form.WriteField("remove_cover", "true")
	}
	if data != nil {
		part, _ := form.CreateFormFile("cover", "cover.png")
		_, _ = part.Write(data)
	}
	_ = form.Close()
	r := httptest.NewRequest("PUT", "http://example.com/api/v2/playlists/"+id+"/editor", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("If-Match", fmt.Sprint(version))
	if token == "" {
		authorizeTestAdmin(api, r)
	} else {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("X-CSRF-Token", csrfForToken(token))
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}
func TestPlaylistEditorAtomicCoverAndConflict(t *testing.T) {
	api, _ := testAPI(t)
	if err := api.store.PutPlaylist(Playlist{ID: "cover-list", Name: "Original"}); err != nil {
		t.Fatal(err)
	}
	original, _ := api.store.Playlist("cover-list")
	data := playlistImage(t, 32, 32, color.White)
	w := editorRequest(t, api, original.ID, "", original.Version, "Saved", data, false)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	var saved map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	if saved["name"] != "Saved" || saved["cover_id"] != hashBytes(data) || saved["version"] != float64(original.Version+1) {
		t.Fatalf("incomplete editor save: %s", w.Body)
	}
	stale := editorRequest(t, api, original.ID, "", original.Version, "Stale", playlistImage(t, 32, 32, color.Black), false)
	if stale.Code != 409 {
		t.Fatalf("stale save: %d %s", stale.Code, stale.Body)
	}
	current, _ := api.store.Playlist(original.ID)
	if current.Name != "Saved" {
		t.Fatal("stale mutation applied")
	}
	var count int
	_ = api.store.db.QueryRow("SELECT COUNT(*) FROM cover_assets").Scan(&count)
	if count != 1 {
		t.Fatal("failed save left an asset")
	}
	if _, err := api.store.db.Exec("CREATE TRIGGER reject_playlist_cover BEFORE INSERT ON audit WHEN NEW.action='playlist.editor' BEGIN SELECT RAISE(ABORT,'audit fixture'); END"); err != nil {
		t.Fatal(err)
	}
	failed := editorRequest(t, api, original.ID, "", current.Version, "Must rollback", playlistImage(t, 32, 32, color.Black), false)
	if failed.Code == 200 {
		t.Fatal("ignored audit failure")
	}
	after, _ := api.store.Playlist(original.ID)
	if after.Name != "Saved" || after.Version != current.Version {
		t.Fatal("partial editor write")
	}
	_ = api.store.db.QueryRow("SELECT COUNT(*) FROM cover_assets").Scan(&count)
	if count != 1 {
		t.Fatal("asset not rolled back")
	}
	_, _ = api.store.db.Exec("DROP TRIGGER reject_playlist_cover")
	removed := editorRequest(t, api, original.ID, "", current.Version, "Saved", nil, true)
	if removed.Code != 200 {
		t.Fatalf("remove: %d %s", removed.Code, removed.Body)
	}
	if bytes.Contains(removed.Body.Bytes(), []byte(`"cover_id"`)) {
		t.Fatal("cover removal failed")
	}
}
func TestPlaylistEditorRejectsInvalidCoversWithoutChanges(t *testing.T) {
	api, _ := testAPI(t)
	_ = api.store.PutPlaylist(Playlist{ID: "invalid-cover", Name: "Original"})
	p, _ := api.store.Playlist("invalid-cover")
	animation := make([]byte, 20)
	binary.BigEndian.PutUint32(animation, 8)
	copy(animation[4:], "acTL")
	binary.BigEndian.PutUint32(animation[8:], 2)
	binary.BigEndian.PutUint32(animation[16:], crc32.ChecksumIEEE(animation[4:16]))
	base := playlistImage(t, 20, 20, color.White)
	animated := append(append(append([]byte{}, base[:33]...), animation...), base[33:]...)
	for name, data := range map[string][]byte{"animation": animated, "fake": []byte("not an image"), "gif": []byte("GIF89a"), "svg": []byte("<svg></svg>"), "rectangle": playlistImage(t, 20, 10, color.White), "oversized dimensions": playlistImage(t, 1025, 1025, color.White), "truncated": playlistImage(t, 20, 20, color.White)[:40], "body limit": make([]byte, (8<<20)+1)} {
		t.Run(name, func(t *testing.T) {
			w := editorRequest(t, api, p.ID, "", p.Version, "Must not apply", data, false)
			if w.Code != 400 && w.Code != 413 {
				t.Fatalf("accepted invalid image: %d %s", w.Code, w.Body)
			}
			after, _ := api.store.Playlist(p.ID)
			if after.Name != p.Name || after.Version != p.Version {
				t.Fatal("invalid image changed metadata")
			}
		})
	}
}
func TestPlaylistCoverVisibilityAndInjection(t *testing.T) {
	api, _ := testAPI(t)
	alice, token := readyMember(t, api, "cover-alice")
	_, other := readyMember(t, api, "cover-bob")
	_ = api.store.PutPlaylist(Playlist{ID: "private-cover", Name: "Private"}, MutationOptions{UserID: alice.ID})
	p, _ := api.store.Playlist("private-cover")
	data := playlistImage(t, 16, 16, color.White)
	w := editorRequest(t, api, p.ID, token, p.Version, "Private", data, false)
	if w.Code != 200 {
		t.Fatalf("member save: %d %s", w.Code, w.Body)
	}
	p, _ = api.store.Playlist(p.ID)
	for credential, status := range map[string]int{token: 200, other: 404} {
		r := httptest.NewRequest("GET", "http://example.com/api/v2/covers/"+hashBytes(data), nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: credential})
		out := httptest.NewRecorder()
		api.ServeHTTP(out, r)
		if out.Code != status {
			t.Fatalf("cover permission: %d want %d", out.Code, status)
		}
	}
	if hidden := editorRequest(t, api, p.ID, other, p.Version, "Hijack", data, false); hidden.Code != 404 {
		t.Fatal("cross-account editor write")
	}
	injected := sessionRequest(api, other, "POST", "/api/v2/playlists", fmt.Sprintf(`{"id":"injected-cover","name":"Injected","cover_id":%q,"cover_mime":"image/png"}`, hashBytes(data)))
	if injected.Code != 201 {
		t.Fatalf("create: %d %s", injected.Code, injected.Body)
	}
	if bytes.Contains(injected.Body.Bytes(), []byte(`"cover_id"`)) {
		t.Fatal("raw asset ID was attached without upload")
	}
	createdKey := sessionRequest(api, token, "POST", "/api/v2/auth/keys", `{"name":"cover-key","allowed_playlist_ids":["private-cover"],"scopes":["library:read","media:read"]}`)
	if createdKey.Code != 201 {
		t.Fatalf("key: %d %s", createdKey.Code, createdKey.Body)
	}
	var keyView APIKeyView
	_ = json.Unmarshal(createdKey.Body.Bytes(), &keyView)
	key := keyView.Secret
	r := httptest.NewRequest("GET", "/api/v2/covers/"+hashBytes(data), nil)
	r.Header.Set("X-API-Key", key)
	out := httptest.NewRecorder()
	api.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatalf("granted key cannot read cover: %d %s", out.Code, out.Body)
	}
}
