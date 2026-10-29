package music

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tagHTTPAPI(t *testing.T) (*API, string) {
	t.Helper()
	a, _ := testAPI(t)
	assertMigrationSchema(t, a.store.db, 8)
	_, _, token := loginTestAccount(t, a.store, "admin")
	return a, token
}

func createHTTPTag(t *testing.T, a *API, token, scope, name string) Tag {
	t.Helper()
	w := mailRequest(a, token, "POST", "/api/v2/tags", fmt.Sprintf(`{"scope":%q,"group_id":"genre","name":%q}`, scope, name), 0)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var tag Tag
	if err := json.Unmarshal(w.Body.Bytes(), &tag); err != nil {
		t.Fatal(err)
	}
	return tag
}

func TestTrackTagsHTTPSchemaGate(t *testing.T) {
	a, _ := testAPIForSchema(t, 7)
	_, _, token := loginTestAccount(t, a.store, "admin")
	for _, path := range []string{"/api/v2/tags", "/api/v2/tag-groups", "/api/v2/tracks/" + a.store.Tracks()[0].ID + "/tags"} {
		w := mailRequest(a, token, "GET", path, "", 0)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "TAGS_UNAVAILABLE") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	var schema, tables int
	if err := a.store.db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if err := a.store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='tags'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if schema != 7 || tables != 0 {
		t.Fatal(schema, tables)
	}
}

func TestTrackTagsHTTPDictionaryPermissionsAndMerge(t *testing.T) {
	a, admin := tagHTTPAPI(t)
	_, alice := readyMember(t, a, "tag-alice")
	_, bob := readyMember(t, a, "tag-bob")
	w := mailRequest(a, alice, "GET", "/api/v2/tag-groups", "", 0)
	var groups []TagGroup
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &groups) != nil || len(groups) != 6 {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, alice, "POST", "/api/v2/tag-groups", `{"name":"Other"}`, 0)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, alice, "POST", "/api/v2/tags", `{"scope":"public","group_id":"genre","name":"No"}`, 0)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	private := createHTTPTag(t, a, alice, "personal", "only-alice-secret")
	for _, token := range []string{admin, bob} {
		w = mailRequest(a, token, "GET", "/api/v2/tags/"+private.ID, "", 0)
		if w.Code != 404 {
			t.Fatal(w.Code, w.Body)
		}
		w = mailRequest(a, token, "PATCH", "/api/v2/tags/"+private.ID, `{"name":"leak"}`, 999)
		if w.Code != 404 {
			t.Fatal(w.Code, w.Body)
		}
		w = mailRequest(a, token, "GET", "/api/v2/tags?scope=personal", "", 0)
		if w.Code != 200 || strings.Contains(w.Body.String(), "only-alice-secret") {
			t.Fatal(w.Code, w.Body)
		}
	}
	public := createHTTPTag(t, a, admin, "public", "Rock")
	target := createHTTPTag(t, a, admin, "public", "Alternative")
	w = mailRequest(a, admin, "POST", "/api/v2/tags/"+public.ID+"/merge", fmt.Sprintf(`{"target_id":%q}`, target.ID), 1)
	if w.Code != 428 {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, admin, "POST", "/api/v2/tags/"+public.ID+"/merge", fmt.Sprintf(`{"target_id":%q,"target_version":1}`, target.ID), 1)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, alice, "GET", "/api/v2/tags/"+public.ID, "", 0)
	var tombstone Tag
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &tombstone) != nil || !tombstone.Deleted || tombstone.MergedInto != target.ID {
		t.Fatal(w.Code, w.Body)
	}
}

func TestTrackTagsHTTPTrackScopesAndVersions(t *testing.T) {
	a, admin := tagHTTPAPI(t)
	u, alice := readyMember(t, a, "tag-editor")
	setEmailTestRequirement(t, a.store, u.ID, true)
	track := a.store.Tracks()[0]
	path := "/api/v2/tracks/" + track.ID + "/tags"
	private := createHTTPTag(t, a, alice, "personal", "My mood")
	public := createHTTPTag(t, a, admin, "public", "Jazz")
	revision := a.store.Revision()
	w := mailRequest(a, alice, "PATCH", path, fmt.Sprintf(`{"scope":"personal","add_ids":[%q]}`, private.ID), 1)
	var view TrackTagsView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.PersonalVersion != 2 || len(view.Personal) != 1 || view.PublicVersion != 1 {
		t.Fatal(w.Code, w.Body)
	}
	if a.store.Revision() != revision {
		t.Fatal("personal tag changed global revision")
	}
	after, _ := a.store.Track(track.ID)
	if after.Version != track.Version || after.Path != track.Path {
		t.Fatal("personal tag changed track")
	}
	w = mailRequest(a, alice, "PATCH", path, fmt.Sprintf(`{"scope":"public","add_ids":[%q]}`, public.ID), 1)
	if w.Code != 403 && w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, admin, "PATCH", path, fmt.Sprintf(`{"scope":"public","add_ids":[%q]}`, public.ID), 1)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, admin, "GET", path, "", 0)
	if w.Code != 200 || strings.Contains(w.Body.String(), private.Name) {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, alice, "PATCH", path, `{"scope":"personal","remove_ids":[]}`, 1)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestTrackTagsHTTPStrictRequestsAndCSRF(t *testing.T) {
	a, token := tagHTTPAPI(t)
	tag := createHTTPTag(t, a, token, "public", "Valid")
	path := "/api/v2/tags/" + tag.ID
	for _, query := range []string{"user_id=other", "scope=public&scope=personal", "limit=101", "offset=-1", "scope=other", "group_id=not-a-group", "q=" + strings.Repeat("a", 65)} {
		w := mailRequest(a, token, "GET", "/api/v2/tags?"+query, "", 0)
		if w.Code != 400 {
			t.Fatal(query, w.Code, w.Body)
		}
	}
	for _, body := range []string{`{"scope":"public","group_id":"genre","name":"x","user_id":"other"}`, `{"scope":"public","group_id":"genre","name":"x"} {}`, `null`, `{"group_id":"genre","name":"x"}`} {
		w := mailRequest(a, token, "POST", "/api/v2/tags", body, 0)
		if w.Code != 400 {
			t.Fatal(body, w.Code, w.Body)
		}
	}
	for _, version := range []string{"", "0", "-1", "*", `W/"1"`, "1,2", "bad"} {
		r := httptest.NewRequest("PATCH", "http://example.com"+path, strings.NewReader(`{"name":"New"}`))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("X-CSRF-Token", csrfForToken(token))
		if version != "" {
			r.Header.Set("If-Match", version)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		want := 400
		if version == "" {
			want = 428
		}
		if w.Code != want {
			t.Fatal(version, w.Code, w.Body)
		}
	}
	r := httptest.NewRequest("PATCH", "http://example.com"+path, strings.NewReader(`{"name":"New"}`))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("If-Match", "1")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestTrackTagsHTTPGroupCRUDAndPaging(t *testing.T) {
	a, token := tagHTTPAPI(t)
	w := mailRequest(a, token, "POST", "/api/v2/tag-groups", `{"name":"Custom group","sort_order":7}`, 0)
	var group TagGroup
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &group) != nil {
		t.Fatal(w.Code, w.Body)
	}
	path := "/api/v2/tag-groups/" + group.ID
	w = mailRequest(a, token, "PATCH", path, `{"name":"Updated group"}`, 1)
	if w.Code != 200 || w.Header().Get("ETag") != `"2"` {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, token, "DELETE", path, "", 1)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, token, "DELETE", path, "", 2)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	for i := 0; i < 3; i++ {
		createHTTPTag(t, a, token, "public", fmt.Sprintf("Page %d", i))
	}
	w = mailRequest(a, token, "GET", "/api/v2/tags?scope=public&group_id=genre&q=Page&limit=2&offset=1", "", 0)
	var page TagPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 3 || len(page.Items) != 2 || page.Offset != 1 || page.Limit != 2 || page.Revision != 6 {
		t.Fatal(w.Code, w.Body)
	}
	tag := page.Items[0]
	w = mailRequest(a, token, "DELETE", "/api/v2/tags/"+tag.ID, "", tag.Version)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestTrackTagsHTTPKeyDeniedAndRevokedDuringBody(t *testing.T) {
	a, token := tagHTTPAPI(t)
	user, session, ok := a.store.sessionIdentity(token)
	if !ok {
		t.Fatal("session not active")
	}
	key, err := a.store.CreateAPIKey(APIKeyCreateRequest{Name: "tag denied", AllowedPlaylistIDs: []string{"library"}}, MutationOptions{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v2/tags", "/api/v2/tag-groups", "/api/v2/tracks/" + a.store.Tracks()[0].ID + "/tags"} {
		r := httptest.NewRequest("GET", "http://example.com"+path, nil)
		r.Header.Set("X-API-Key", key.Secret)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(w.Code, w.Body)
		}
	}
	reader := &resumeReadHook{Reader: strings.NewReader(`{"scope":"personal","group_id":"genre","name":"Never created"}`), after: func() {
		if err := a.store.RevokeSession(user.ID, session.ID); err != nil {
			t.Fatal(err)
		}
	}}
	r := httptest.NewRequest("POST", "http://example.com/api/v2/tags", io.NopCloser(reader))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("X-CSRF-Token", csrfForToken(token))
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
	var count int
	if err := a.store.db.QueryRow("SELECT count(*) FROM tags").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestTrackTagsHTTPPrivateAuditHidesResourceIdentity(t *testing.T) {
	a, token := tagHTTPAPI(t)
	user, _, ok := a.store.sessionIdentity(token)
	if !ok {
		t.Fatal("inactive session")
	}
	tag := createHTTPTag(t, a, token, "personal", "Secret personal label")
	track := a.store.Tracks()[0]
	w := mailRequest(a, token, "PATCH", "/api/v2/tracks/"+track.ID+"/tags", fmt.Sprintf(`{"scope":"personal","add_ids":[%q]}`, tag.ID), 1)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	rows, err := a.store.db.Query("SELECT entity_id,changes FROM audit WHERE action IN ('tag.create','track.tags.update')")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, changes string
		if err := rows.Scan(&id, &changes); err != nil {
			t.Fatal(err)
		}
		if id != user.ID || strings.Contains(changes, track.ID) || strings.Contains(changes, tag.ID) || strings.Contains(changes, tag.Name) {
			t.Fatal("private resource exposed in audit", id, changes)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal(count)
	}
}
