package music

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func communityConditional(api *API, token, path, etag string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "http://example.com/api/v2/"+path, nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}
func TestCommunityCatalogAndConditionalPermissions(t *testing.T) {
	api, a, b, p := communityFixture(t)
	at := communityLogin(t, api.store, a.Username)
	bt := communityLogin(t, api.store, b.Username)
	first := communityConditional(api, bt, "public/playlists?limit=1", "")
	if first.Code != 200 || first.Header().Get("ETag") == "" {
		t.Fatalf("catalog: %d %s", first.Code, first.Body)
	}
	var page struct {
		Items []struct {
			ID, Name   string
			TrackCount int `json:"track_count"`
		} `json:"items"`
		Total int `json:"total"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &page)
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("private playlist in catalog")
	}
	published := memberVersionRequest(api, at, "PUT", "/api/v2/playlists/"+p.ID+"/visibility", `{"visibility":"public"}`, p.Version)
	if published.Code != 200 {
		t.Fatalf("publish: %d %s", published.Code, published.Body)
	}
	visible := communityConditional(api, bt, "public/playlists?limit=1", first.Header().Get("ETag"))
	_ = json.Unmarshal(visible.Body.Bytes(), &page)
	if visible.Code != 200 || page.Total != 1 || len(page.Items) != 1 || page.Items[0].TrackCount != 1 || strings.Contains(visible.Body.String(), "author private note") {
		t.Fatalf("unsafe summary: %d %s", visible.Code, visible.Body)
	}
	etag := visible.Header().Get("ETag")
	unchanged := communityConditional(api, bt, "public/playlists?limit=1", "W/"+etag)
	if unchanged.Code != 304 || unchanged.Body.Len() != 0 {
		t.Fatal("unchanged catalog did not return 304")
	}
	ownPublic := sessionRequest(api, at, "GET", "/api/v2/public/playlists/"+p.ID, "")
	if ownPublic.Code != 200 || strings.Contains(ownPublic.Body.String(), "author private note") {
		t.Fatal("public detail exposed author personal notes")
	}
	sub := sessionRequest(api, bt, "PUT", "/api/v2/subscriptions/"+p.ID, "")
	if sub.Code != 200 {
		t.Fatalf("subscribe: %d %s", sub.Code, sub.Body)
	}
	var view Playlist
	_ = json.Unmarshal(sub.Body.Bytes(), &view)
	updated := memberVersionRequest(api, bt, "PATCH", "/api/v2/subscriptions/"+p.ID+"/tracks/"+p.TrackIDs[0], `{"note":"reader secret"}`, view.SubscriptionVersion)
	if updated.Code != 200 {
		t.Fatalf("note: %d %s", updated.Code, updated.Body)
	}
	if r := communityConditional(api, bt, "public/playlists?limit=1", etag); r.Code != 304 {
		t.Fatal("personal subscription actions invalidated shared catalog")
	}
	c, ct := readyMember(t, api, "public-third")
	_ = c
	reader := sessionRequest(api, bt, "GET", "/api/v2/public/playlists/"+p.ID, "")
	third := sessionRequest(api, ct, "GET", "/api/v2/public/playlists/"+p.ID, "")
	if !strings.Contains(reader.Body.String(), "reader secret") || strings.Contains(third.Body.String(), "reader secret") {
		t.Fatal("subscription notes crossed accounts")
	}
	p, _ = api.store.Playlist(p.ID)
	private := memberVersionRequest(api, at, "PUT", "/api/v2/playlists/"+p.ID+"/visibility", `{"visibility":"private"}`, p.Version)
	if private.Code != 200 {
		t.Fatal(private.Body.String())
	}
	if r := communityConditional(api, bt, "public/playlists/"+p.ID, etag); r.Code != 404 {
		t.Fatal("stale ETag bypassed privacy")
	}
	hidden := communityConditional(api, bt, "public/playlists?limit=1", etag)
	_ = json.Unmarshal(hidden.Body.Bytes(), &page)
	if hidden.Code != 200 || page.Total != 0 {
		t.Fatal("catalog did not withdraw private playlist")
	}
	subscribed := sessionRequest(api, bt, "GET", "/api/v2/subscriptions", "")
	if subscribed.Code != 200 || !strings.Contains(subscribed.Body.String(), `"subscription_reason":"private"`) {
		t.Fatal("inactive subscription lost")
	}
	if r := sessionRequest(api, bt, "DELETE", "/api/v2/subscriptions/"+p.ID, ""); r.Code != 204 {
		t.Fatal("unsubscribe failed")
	}
	for _, query := range []string{"limit=0", "limit=101", "offset=-1"} {
		if r := communityConditional(api, bt, "public/playlists?"+query, ""); r.Code != 400 {
			t.Fatal("invalid pagination accepted")
		}
	}
}

func TestCommunityCatalogPagingAndAuthorChanges(t *testing.T) {
	api, a, b, p := communityFixture(t)
	s := api.store
	token := communityLogin(t, s, b.Username)
	_, _ = s.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err := s.PutPlaylist(Playlist{ID: "second-public", Name: "Second title"}, MutationOptions{UserID: a.ID}); err != nil {
		t.Fatal(err)
	}
	second, _ := s.Playlist("second-public")
	_, _ = s.SetPlaylistVisibility(second.ID, "public", MutationOptions{UserID: a.ID, ExpectedVersion: second.Version})
	first := communityConditional(api, token, "public/playlists?limit=1", "")
	next := communityConditional(api, token, "public/playlists?limit=1&offset=1", "")
	var one, two PublicCatalog
	_ = json.Unmarshal(first.Body.Bytes(), &one)
	_ = json.Unmarshal(next.Body.Bytes(), &two)
	if one.Total != 2 || len(one.Items) != 1 || len(two.Items) != 1 || one.Items[0].ID == two.Items[0].ID {
		t.Fatal("catalog did not paginate summaries")
	}
	search := communityConditional(api, token, "public/playlists?q=Second", "")
	var matches PublicCatalog
	_ = json.Unmarshal(search.Body.Bytes(), &matches)
	if matches.Total != 1 || matches.Items[0].ID != second.ID {
		t.Fatal("catalog search failed")
	}
	users, _ := s.Users()
	admin := ""
	for _, u := range users {
		if u.Role == "admin" {
			admin = u.ID
		}
	}
	display := "Renamed publisher"
	author, err := s.UpdateUser(a.ID, AccountPatch{DisplayName: &display}, MutationOptions{UserID: admin, ExpectedVersion: a.Version})
	if err != nil {
		t.Fatal(err)
	}
	renamed := communityConditional(api, token, "public/playlists?limit=1", first.Header().Get("ETag"))
	if renamed.Code != 200 || !strings.Contains(renamed.Body.String(), display) {
		t.Fatal("author display change left cached catalog")
	}
	off := false
	if _, err := s.UpdateUser(a.ID, AccountPatch{Enabled: &off}, MutationOptions{UserID: admin, ExpectedVersion: author.Version}); err != nil {
		t.Fatal(err)
	}
	disabled := communityConditional(api, token, "public/playlists?limit=1", renamed.Header().Get("ETag"))
	var hidden PublicCatalog
	_ = json.Unmarshal(disabled.Body.Bytes(), &hidden)
	if disabled.Code != 200 || hidden.Total != 0 {
		t.Fatal("disabled author's public catalog remained cached")
	}
	unauth := httptest.NewRecorder()
	api.ServeHTTP(unauth, httptest.NewRequest("GET", "/api/v2/public/playlists", nil))
	if unauth.Code != 401 {
		t.Fatal("anonymous discovery allowed")
	}
}
