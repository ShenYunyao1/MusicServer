package music

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func readyMember(t *testing.T, api *API, name string) (User, string) {
	t.Helper()
	m := createMemberFixture(t, api, name)
	password := "personal password for " + name
	if err := api.store.ChangePassword(m.User, m.Password, password); err != nil {
		t.Fatal(err)
	}
	u, _, token, err := api.store.Login(name, password, "member-"+name)
	if err != nil {
		t.Fatal(err)
	}
	return u, token
}
func memberVersionRequest(api *API, token, method, path, body string, version int64) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.com"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("X-CSRF-Token", csrfForToken(token))
	r.Header.Set("If-Match", fmt.Sprint(version))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}
func TestMultiuserObjectOwnershipCannotBeBypassed(t *testing.T) {
	api, _ := testAPI(t)
	alice, token := readyMember(t, api, "alice")
	bob, _ := readyMember(t, api, "bob")
	track := api.store.Tracks()[0]
	if err := api.store.PutPlaylist(Playlist{ID: "bob-private", Name: "Bob private", TrackIDs: []string{track.ID}}, MutationOptions{UserID: bob.ID}); err != nil {
		t.Fatal(err)
	}
	playlist, _ := api.store.Playlist("bob-private")
	for _, method := range []string{"GET", "PATCH", "PUT", "DELETE"} {
		w := memberVersionRequest(api, token, method, "/api/v2/playlists/bob-private", `{"name":"hijack"}`, playlist.Version)
		if w.Code != 404 {
			t.Fatalf("private %s leaked: %d %s", method, w.Code, w.Body)
		}
	}
	if w := memberVersionRequest(api, token, "POST", "/api/v2/playlists/bob-private/tracks", `{"track_ids":[]}`, playlist.Version); w.Code != 404 {
		t.Fatalf("private members leaked: %d", w.Code)
	}
	options := MutationOptions{UserID: alice.ID, ExpectedVersion: track.Version}
	if err := api.store.UpdateTrack(track.ID, map[string]string{"title": "hijack"}, options); err == nil {
		t.Fatal("store ignored track owner")
	}
	if err := api.store.SetTrackCover(track.ID, tinyPNG(), options); err == nil {
		t.Fatal("store ignored cover owner")
	}
	options.ExpectedVersion = playlist.Version
	if err := api.store.DeletePlaylist(playlist.ID, options); err == nil {
		t.Fatal("store ignored playlist owner")
	}
	if _, err := api.store.AddPlaylistTracks(playlist.ID, []string{}, options); err == nil {
		t.Fatal("store ignored playlist membership owner")
	}
	if err := api.store.PutPlaylist(playlist, options); err == nil {
		t.Fatal("store allowed playlist replacement")
	}
}
