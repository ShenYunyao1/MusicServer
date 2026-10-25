package music

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func browseTestRequest(api *API, user User, method, path, body string, version int64) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	var sessionID string
	_ = api.store.db.QueryRow("SELECT id FROM sessions WHERE user_id=? ORDER BY last_active_at DESC LIMIT 1", user.ID).Scan(&sessionID)
	r = r.WithContext(withAuth(r.Context(), authContext{session: true, userID: user.ID, keyID: sessionID, role: user.Role, authVersion: user.AuthVersion}))
	if version > 0 {
		r.Header.Set("If-Match", fmt.Sprint(version))
	}
	w := httptest.NewRecorder()
	api.browse(w, r)
	return w
}

func browseTestPage(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("browse status %d: %s", w.Code, w.Body)
	}
	var page map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func browseSeedTracks(t *testing.T, api *API, owner, uploader string, count int) []string {
	t.Helper()
	ids := []string{}
	err := api.store.transaction(func(tx *sql.Tx) error {
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("10000000-0000-4000-8000-%012d", i)
			track := Track{ID: id, OwnerID: owner, UploadedBy: uploader, Title: fmt.Sprintf("Page %03d", i), Artist: "Search Artist", Lyrics: "full private lyrics", Path: fmt.Sprintf("page-%d.mp3", i), Version: 1}
			if err := writeTrack(tx, track); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return recordChange(tx, "track", ids[0], false)
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestBrowseTracksPagingFiltersAndRevision(t *testing.T) {
	api, _ := testAPI(t)
	alice, _ := readyMember(t, api, "browse-alice")
	bob, _ := readyMember(t, api, "browse-bob")
	ids := browseSeedTracks(t, api, alice.ID, bob.ID, 105)
	if err := api.store.PutPlaylist(Playlist{ID: "mine", Name: "Mine", TrackIDs: ids[:2]}, MutationOptions{UserID: alice.ID}); err != nil {
		t.Fatal(err)
	}
	page := browseTestPage(t, browseTestRequest(api, alice, "GET", "/api/v2/browse/tracks?q=search%20artist&filter=managed&offset=100&limit=10", "", 0))
	if page["total"] != float64(105) || len(page["items"].([]any)) != 5 {
		t.Fatal(page)
	}
	item := page["items"].([]any)[0].(map[string]any)
	if item["id"] != ids[100] || item["detail_loaded"] != false || item["path"] != "" || item["lyrics"] != nil {
		t.Fatal(item)
	}
	for _, entry := range []struct {
		user   User
		filter string
		total  int
	}{{alice, "mine", 0}, {bob, "mine", 105}, {alice, "orphan", 104}, {bob, "orphan", 106}} {
		p := browseTestPage(t, browseTestRequest(api, entry.user, "GET", "/api/v2/browse/tracks?filter="+entry.filter+"&limit=1000", "", 0))
		if p["total"] != float64(entry.total) || p["limit"] != float64(100) || len(p["items"].([]any)) > 100 {
			t.Fatal(entry.filter, p)
		}
	}
	revision := int64(page["revision"].(float64))
	if err := api.store.UpdateTrack(ids[0], map[string]string{"title": "Changed"}); err != nil {
		t.Fatal(err)
	}
	w := browseTestRequest(api, alice, "GET", fmt.Sprintf("/api/v2/browse/tracks?revision=%d", revision), "", 0)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "BROWSE_RESTART") {
		t.Fatal(w.Code, w.Body)
	}
	for _, query := range []string{"offset=-1", "limit=-1", "revision=bad", "filter=unknown"} {
		if w := browseTestRequest(api, alice, "GET", "/api/v2/browse/tracks?"+query, "", 0); w.Code != 400 {
			t.Fatal(query, w.Code, w.Body)
		}
	}
	page = browseTestPage(t, browseTestRequest(api, alice, "GET", "/api/v2/browse/tracks?offset=9999&limit=1", "", 0))
	if len(page["items"].([]any)) != 0 {
		t.Fatal(page)
	}
}

func TestBrowsePlaylistSummaryPrivacyAndUnavailableMembers(t *testing.T) {
	api, _ := testAPI(t)
	alice, _ := readyMember(t, api, "browse-owner")
	bob, _ := readyMember(t, api, "browse-other")
	ids := browseSeedTracks(t, api, alice.ID, alice.ID, 105)
	for _, p := range []Playlist{{ID: "large", Name: "Large", TrackIDs: ids, TrackNotes: map[string]string{ids[0]: "own note"}}, {ID: "small", Name: "Small", TrackIDs: ids[:2]}} {
		if err := api.store.PutPlaylist(p, MutationOptions{UserID: alice.ID}); err != nil {
			t.Fatal(err)
		}
	}
	page := browseTestPage(t, browseTestRequest(api, alice, "GET", "/api/v2/browse/playlists?limit=1", "", 0))
	item := page["items"].([]any)[0].(map[string]any)
	if page["total"] != float64(2) || item["id"] != "large" || item["members_complete"] != false || item["track_ids"] != nil || item["track_notes"] != nil || item["track_count"] != float64(105) {
		t.Fatal(page)
	}
	page = browseTestPage(t, browseTestRequest(api, alice, "GET", "/api/v2/browse/playlists?q=Small", "", 0))
	item = page["items"].([]any)[0].(map[string]any)
	if item["members_complete"] != true || len(item["track_ids"].([]any)) != 2 {
		t.Fatal(item)
	}
	page = browseTestPage(t, browseTestRequest(api, bob, "GET", "/api/v2/browse/playlists", "", 0))
	if page["total"] != float64(0) {
		t.Fatal(page)
	}
	if w := browseTestRequest(api, bob, "GET", "/api/v2/browse/playlists/large/tracks", "", 0); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	admin, err := readUser(api.store.db, api.store.Tracks()[0].OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	page = browseTestPage(t, browseTestRequest(api, admin, "GET", "/api/v2/browse/playlists", "", 0))
	if page["total"] != float64(0) {
		t.Fatal("admin list exposed another owner's private collection", page)
	}
	browseTestPage(t, browseTestRequest(api, admin, "GET", "/api/v2/browse/playlists/large/tracks?limit=1", "", 0))
	var audited int
	if err := api.store.db.QueryRow("SELECT count(*) FROM audit WHERE action='playlist.admin.read' AND entity_id='large' AND user_id=?", admin.ID).Scan(&audited); err != nil || audited != 1 {
		t.Fatal("management read was not audited", audited, err)
	}
	if err := api.store.DeleteTrack(ids[0]); err != nil {
		t.Fatal(err)
	}
	page = browseTestPage(t, browseTestRequest(api, alice, "GET", "/api/v2/browse/playlists/large/tracks?limit=1", "", 0))
	item = page["items"].([]any)[0].(map[string]any)
	if item["id"] != ids[0] || item["title"] != "Unavailable" || item["playlist_note"] != "own note" || item["artist"] != nil || item["owner_id"] != nil {
		t.Fatal(item)
	}
	page = browseTestPage(t, browseTestRequest(api, alice, "GET", "/api/v2/browse/playlists/large/tracks?q=full%20private%20lyrics", "", 0))
	if page["total"] != float64(0) {
		t.Fatal(page)
	}
	page = browseTestPage(t, browseTestRequest(api, alice, "GET", "/api/v2/browse/summary", "", 0))
	if page["total"] != float64(105) || page["owned"] != float64(104) || page["missing"] != float64(0) || page["playlist_count"] != float64(2) {
		t.Fatal(page)
	}
	r := httptest.NewRequest("GET", "/api/v2/browse/tracks", nil)
	w := httptest.NewRecorder()
	api.browse(w, r)
	if w.Code != 403 {
		t.Fatal("non-session browse allowed", w.Code, w.Body)
	}
}

func TestBrowseRelativeMovePreservesOutsidePageAndChecksBaseline(t *testing.T) {
	api, _ := testAPI(t)
	alice, _ := readyMember(t, api, "browse-sort")
	ids := browseSeedTracks(t, api, alice.ID, alice.ID, 6)
	if err := api.store.PutPlaylist(Playlist{ID: "sort", Name: "Sort", TrackIDs: ids}, MutationOptions{UserID: alice.ID}); err != nil {
		t.Fatal(err)
	}
	p, _ := api.store.Playlist("sort")
	body := fmt.Sprintf(`{"track_id":%q,"before_id":%q,"offset":2,"limit":2,"expected_track_ids":[%q,%q]}`, ids[3], ids[2], ids[2], ids[3])
	page := browseTestPage(t, browseTestRequest(api, alice, "POST", "/api/v2/browse/playlists/sort/tracks/move", body, p.Version))
	after, _ := api.store.Playlist("sort")
	want := []string{ids[0], ids[1], ids[3], ids[2], ids[4], ids[5]}
	if !slices.Equal(after.TrackIDs, want) || page["total"] != float64(6) || after.Version != p.Version+1 {
		t.Fatal(after, page)
	}
	for _, entry := range []struct {
		body    string
		version int64
		code    int
	}{{body, p.Version, 409}, {body, after.Version, 409}, {body, 0, 428}, {fmt.Sprintf(`{"track_id":%q,"before_id":"missing"}`, ids[3]), after.Version, 404}} {
		w := browseTestRequest(api, alice, "POST", "/api/v2/browse/playlists/sort/tracks/move", entry.body, entry.version)
		if w.Code != entry.code {
			t.Fatal(w.Code, w.Body)
		}
	}
	unchanged, _ := api.store.Playlist("sort")
	if !slices.Equal(unchanged.TrackIDs, want) || unchanged.Version != after.Version {
		t.Fatal(unchanged)
	}
	end := fmt.Sprintf(`{"track_id":%q,"before_id":""}`, ids[0])
	browseTestPage(t, browseTestRequest(api, alice, "POST", "/api/v2/browse/playlists/sort/tracks/move", end, after.Version))
	final, _ := api.store.Playlist("sort")
	if !slices.Equal(final.TrackIDs, []string{ids[1], ids[3], ids[2], ids[4], ids[5], ids[0]}) {
		t.Fatal(final)
	}
}

func TestBrowseRelativePlaylistMoveAndLightweightPicker(t *testing.T) {
	api, _ := testAPI(t)
	alice, _ := readyMember(t, api, "browse-cards")
	bob, _ := readyMember(t, api, "browse-cards-other")
	tracks := browseSeedTracks(t, api, alice.ID, alice.ID, 105)
	ids := []string{}
	if err := api.store.transaction(func(tx *sql.Tx) error {
		for i := 0; i < 105; i++ {
			id := fmt.Sprintf("card-%03d", i)
			ids = append(ids, id)
			if err := writePlaylist(tx, Playlist{ID: id, Name: id, OwnerID: alice.ID, SortOrder: i + 1, Version: 1, TrackIDs: tracks[:2]}); err != nil {
				return err
			}
		}
		return recordChange(tx, "playlist", ids[0], false)
	}); err != nil {
		t.Fatal(err)
	}
	baseline := api.store.Revision()
	body, _ := json.Marshal(map[string]any{"playlist_id": ids[99], "before_id": ids[50], "expected_order": ids[50:100], "offset": 50, "limit": 50, "revision": baseline})
	page := browseTestPage(t, browseTestRequest(api, alice, "POST", "/api/v2/browse/playlists/move", string(body), 0))
	if page["total"] != float64(105) || len(page["items"].([]any)) != 50 || page["items"].([]any)[0].(map[string]any)["id"] != ids[99] {
		t.Fatal(page)
	}
	want := append(cloneStrings(ids[:50]), ids[99])
	want = append(want, ids[50:99]...)
	want = append(want, ids[100:]...)
	rows, err := api.store.db.Query("SELECT id FROM playlists WHERE owner_id=? ORDER BY sort_order,id", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	actual := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, id)
	}
	rows.Close()
	if !slices.Equal(actual, want) {
		t.Fatal(actual)
	}
	if w := browseTestRequest(api, alice, "POST", "/api/v2/browse/playlists/move", string(body), 0); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	page = browseTestPage(t, browseTestRequest(api, alice, "GET", "/api/v2/browse/playlists?summary_only=1&limit=1", "", 0))
	item := page["items"].([]any)[0].(map[string]any)
	if item["track_ids"] != nil || item["track_notes"] != nil || item["members_complete"] != false || item["track_count"] != float64(2) {
		t.Fatal(item)
	}
	page = browseTestPage(t, browseTestRequest(api, alice, "GET", "/api/v2/browse/tracks?exclude_playlist_id="+ids[0], "", 0))
	if page["total"] != float64(104) {
		t.Fatal(page)
	}
	if w := browseTestRequest(api, bob, "GET", "/api/v2/browse/tracks?exclude_playlist_id="+ids[0], "", 0); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestBrowseMoveRechecksRevokedSessionAfterBody(t *testing.T) {
	for _, kind := range []string{"cards", "members"} {
		t.Run(kind, func(t *testing.T) {
			api, _ := testAPI(t)
			user, session, token := loginTestAccount(t, api.store, "admin")
			ids := browseSeedTracks(t, api, user.ID, user.ID, 2)
			for _, id := range []string{"browse-first", "browse-second"} {
				if err := api.store.PutPlaylist(Playlist{ID: id, Name: id, TrackIDs: ids}, MutationOptions{UserID: user.ID}); err != nil {
					t.Fatal(err)
				}
			}
			path := "/api/v2/browse/playlists/move"
			body := `{"playlist_id":"browse-second","before_id":"browse-first","expected_order":["browse-first","browse-second"],"limit":50}`
			if kind == "members" {
				path = "/api/v2/browse/playlists/browse-first/tracks/move"
				body = fmt.Sprintf(`{"track_id":%q,"before_id":%q,"expected_track_ids":[%q,%q],"limit":50}`, ids[1], ids[0], ids[0], ids[1])
			}
			before := api.store.Revision()
			reader := &resumeReadHook{Reader: strings.NewReader(body), after: func() {
				if err := api.store.RevokeSession(user.ID, session.ID); err != nil {
					t.Fatal(err)
				}
			}}
			r := httptest.NewRequest("POST", "http://example.com"+path, reader)
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			r.Header.Set("Origin", "http://example.com")
			r.Header.Set("X-CSRF-Token", csrfForToken(token))
			r.Header.Set("If-Match", "1")
			w := httptest.NewRecorder()
			api.ServeHTTP(w, r)
			if w.Code < 400 || api.store.Revision() != before {
				t.Fatalf("revoked session mutated %s: %d %s", kind, w.Code, w.Body)
			}
		})
	}
}

func TestBrowseSessionAndMissingBoundary(t *testing.T) {
	api, _ := testAPI(t)
	member, token := readyMember(t, api, "browse-session")
	track := api.store.Tracks()[0]
	track.Missing = true
	if err := api.store.transaction(func(tx *sql.Tx) error {
		if err := writeTrack(tx, track); err != nil {
			return err
		}
		return recordChange(tx, "track", track.ID, false)
	}); err != nil {
		t.Fatal(err)
	}
	page := browseTestPage(t, callAPI(api, "GET", "/api/v2/browse/tracks?filter=missing&limit=1", ""))
	if page["total"] != float64(1) || page["items"].([]any)[0].(map[string]any)["missing"] != true {
		t.Fatal(page)
	}
	if w := sessionRequest(api, token, "GET", "/api/v2/browse/tracks?filter=missing", ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	page = browseTestPage(t, sessionRequest(api, token, "GET", "/api/v2/browse/tracks?limit=1", ""))
	if page["total"] != float64(1) {
		t.Fatal(page)
	}
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "browse cannot use a player key", AllowedPlaylistIDs: []string{"library"}}, MutationOptions{UserID: member.ID})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/v2/browse/tracks", nil)
	r.Header.Set("X-API-Key", key.Secret)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "SESSION_REQUIRED") {
		t.Fatal(w.Code, w.Body)
	}
}
