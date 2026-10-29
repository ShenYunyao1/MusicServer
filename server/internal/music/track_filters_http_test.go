package music

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTrackFilterHTTPSchemaAndStrictBodies(t *testing.T) {
	a, _ := testAPIForSchema(t, 7)
	_, _, token := loginTestAccount(t, a.store, "admin")
	for _, path := range []string{"track-query", "track-facets", "saved-track-filters"} {
		method := "POST"
		if path == "saved-track-filters" {
			method = "GET"
		}
		w := mailRequest(a, token, method, "/api/v2/"+path, `{"filter":{"version":1}}`, 0)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "TAGS_UNAVAILABLE") {
			t.Fatal(path, w.Code, w.Body)
		}
	}
	a, token = tagHTTPAPI(t)
	for _, body := range []string{`null`, `{"filter":{"version":1,"user_id":"other"}}`, `{"filter":{"version":1}} {}`, `{"filter":{"version":1},"sort":"title;DROP TABLE tracks"}`, `{"filter":{"version":1,"tags":[{"scope":"public","group_id":"genre","include_ids":[],"exclude_ids":[]}]}}`} {
		w := mailRequest(a, token, "POST", "/api/v2/track-query", body, 0)
		if w.Code != 400 {
			t.Fatal(body, w.Code, w.Body)
		}
	}
	w := mailRequest(a, token, "POST", "/api/v2/track-query?user_id=other", `{"filter":{"version":1}}`, 0)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	r := httptest.NewRequest("POST", "http://example.com/api/v2/track-query", strings.NewReader(`{"filter":{"version":1}}`))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestTrackFilterHTTPQueriesAndSavedLifecycle(t *testing.T) {
	a, token := tagHTTPAPI(t)
	_, member := readyMember(t, a, "filter-private")
	create := mailRequest(a, member, "POST", "/api/v2/saved-track-filters", `{"name":"My rules","filter":{"version":1,"has_cover":false}}`, 0)
	if create.Code != 201 {
		t.Fatal(create.Code, create.Body)
	}
	var saved struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	path := "/api/v2/saved-track-filters/" + saved.ID
	for _, method := range []string{"GET", "PATCH", "DELETE"} {
		w := mailRequest(a, token, method, path, `{"name":"stolen"}`, 999)
		if w.Code != 404 {
			t.Fatal(method, w.Code, w.Body)
		}
	}
	w := mailRequest(a, member, "PATCH", path, `{"name":"Renamed"}`, 0)
	if w.Code != 428 {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, member, "PATCH", path, `{"name":"Renamed"}`, 1)
	if w.Code != 200 || w.Header().Get("ETag") != `"2"` {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, member, "DELETE", path, "", 1)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, member, "POST", "/api/v2/track-query", `{"filter":{"version":1,"has_cover":false},"sort":"title","direction":"asc","limit":1}`, 0)
	var page struct {
		Items    []any           `json:"items"`
		Total    int             `json:"total"`
		Baseline json.RawMessage `json:"baseline"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total < 1 || len(page.Items) != 1 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, member, "POST", "/api/v2/track-query", fmt.Sprintf(`{"filter":{"version":1},"baseline":%s}`, page.Baseline), 0)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, member, "DELETE", path, "", 2)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestTrackFilterHTTPRevalidatesSessionAfterBody(t *testing.T) {
	for _, endpoint := range []string{"saved-track-filters", "track-query", "track-facets"} {
		t.Run(endpoint, func(t *testing.T) {
			a, token := tagHTTPAPI(t)
			user, session, _ := a.store.sessionIdentity(token)
			body := `{"name":"Never saved","filter":{"version":1}}`
			if endpoint == "track-query" {
				body = `{"filter":{"version":1}}`
			}
			if endpoint == "track-facets" {
				body = `{"filter":{"version":1},"scope":"public","group_id":"genre"}`
			}
			reader := &resumeReadHook{Reader: strings.NewReader(body), after: func() {
				if err := a.store.RevokeSession(user.ID, session.ID); err != nil {
					t.Fatal(err)
				}
			}}
			r := httptest.NewRequest("POST", "http://example.com/api/v2/"+endpoint, reader)
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			r.Header.Set("Origin", "http://example.com")
			r.Header.Set("X-CSRF-Token", csrfForToken(token))
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatal(w.Code, w.Body)
			}
			var count int
			if err := a.store.db.QueryRow("SELECT count(*) FROM saved_track_filters").Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestTrackFilterHTTPRejectsPlayerKeys(t *testing.T) {
	a, token := tagHTTPAPI(t)
	user, _, _ := a.store.sessionIdentity(token)
	key, err := a.store.CreateAPIKey(APIKeyCreateRequest{Name: "Filter denial", AllowedPlaylistIDs: []string{"library"}}, MutationOptions{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"track-query", "track-facets", "saved-track-filters"} {
		method := "POST"
		if endpoint == "saved-track-filters" {
			method = "GET"
		}
		r := httptest.NewRequest(method, "http://example.com/api/v2/"+endpoint, strings.NewReader(`{"filter":{"version":1}}`))
		r.Header.Set("X-API-Key", key.Secret)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "SESSION_REQUIRED") {
			t.Fatal(endpoint, w.Code, w.Body)
		}
	}
}

func TestTrackFilterHTTPFacetsPrivateIsolationAndRedirects(t *testing.T) {
	a, auth, ids, rock, personal := filterIntegrationFixture(t)
	_, _, adminToken := loginTestAccount(t, a.store, "admin")
	member, memberToken := readyMember(t, a, "facet-member")
	memberAuth := tagMemberAuth(t, a.store, member.Username)
	private := mustLinkTag(t, a.store, memberAuth, "personal", "member-secret-filter")
	if _, err := a.store.ChangeTrackTags(memberAuth, ids[2], "personal", 1, []string{private.ID}, nil); err != nil {
		t.Fatal(err)
	}
	w := mailRequest(a, memberToken, "POST", "/api/v2/track-query", fmt.Sprintf(`{"filter":{"version":1,"tags":[{"scope":"personal","group_id":"genre","include_ids":[%q]}]}}`, personal.ID), 0)
	if w.Code != 404 || strings.Contains(w.Body.String(), personal.Name) {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, adminToken, "POST", "/api/v2/track-facets", `{"filter":{"version":1},"scope":"personal","group_id":"genre"}`, 0)
	if w.Code != 200 || strings.Contains(w.Body.String(), private.ID) || strings.Contains(w.Body.String(), private.Name) {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, adminToken, "POST", "/api/v2/track-facets", fmt.Sprintf(`{"filter":{"version":1,"duration_max_ms":2000,"tags":[{"scope":"public","group_id":"genre","include_ids":[%q]}]},"scope":"public","group_id":"genre"}`, rock.ID), 0)
	var facets struct {
		Items []struct {
			Tag   Tag `json:"tag"`
			Count int `json:"count"`
		} `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &facets) != nil || len(facets.Items) != 2 {
		t.Fatal(w.Code, w.Body)
	}
	for _, item := range facets.Items {
		if item.Count != 1 {
			t.Fatal("facet retained its own group or dropped another condition", w.Body)
		}
	}
	jazz := facets.Items[0].Tag
	if jazz.ID == rock.ID {
		jazz = facets.Items[1].Tag
	}
	if _, err := a.store.MergeTags(auth, rock.ID, jazz.ID, rock.Version, jazz.Version); err != nil {
		t.Fatal(err)
	}
	w = mailRequest(a, adminToken, "POST", "/api/v2/track-query", fmt.Sprintf(`{"filter":{"version":1,"tags":[{"scope":"public","group_id":"genre","include_ids":[%q]}]}}`, rock.ID), 0)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"from":"`+rock.ID+`"`) {
		t.Fatal(w.Code, w.Body)
	}
	unused := mustLinkTag(t, a.store, auth, "public", "Removed filter reference")
	if err := a.store.DeleteTag(auth, unused.ID, unused.Version); err != nil {
		t.Fatal(err)
	}
	w = mailRequest(a, adminToken, "POST", "/api/v2/track-query", fmt.Sprintf(`{"filter":{"version":1,"tags":[{"scope":"public","group_id":"genre","include_ids":[%q]}]}}`, unused.ID), 0)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "FILTER_REFERENCE_INVALID") {
		t.Fatal(w.Code, w.Body)
	}
}
