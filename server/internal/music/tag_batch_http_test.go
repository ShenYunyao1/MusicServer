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

func TestTagBatchHTTPPreviewDraftOwnershipAndIdempotentStart(t *testing.T) {
	a, admin := tagHTTPAPI(t)
	if _, err := a.store.db.Exec(tagBatchSchema); err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
	_, alice := readyMember(t, a, "batch-alice")
	_, bob := readyMember(t, a, "batch-bob")
	tag := createHTTPTag(t, a, alice, "personal", "Only Alice batch")
	track := a.store.Tracks()[0]
	w := mailRequest(a, alice, "POST", "/api/v2/tag-batches", fmt.Sprintf(`{"scope":"personal","track_ids":[%q]}`, track.ID), 0)
	var job TagBatch
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &job) != nil || job.Total != 1 || job.State != "preview" {
		t.Fatal(w.Code, w.Body)
	}
	path := "/api/v2/tag-batches/" + job.ID
	for _, token := range []string{admin, bob} {
		w = mailRequest(a, token, "GET", path, "", 0)
		if w.Code != 404 {
			t.Fatal(w.Code, w.Body)
		}
		w = mailRequest(a, token, "PATCH", path, fmt.Sprintf(`{"add_ids":[%q]}`, tag.ID), 999)
		if w.Code != 404 {
			t.Fatal(w.Code, w.Body)
		}
	}
	w = mailRequest(a, alice, "PATCH", path, fmt.Sprintf(`{"add_ids":[%q]}`, tag.ID), job.Version)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &job) != nil || job.Items[0].Version != 1 {
		t.Fatal(w.Code, w.Body)
	}
	version := job.Version
	w = mailRequest(a, alice, "POST", path+"/start", "{}", version)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &job) != nil || job.Completed != 1 || job.State != "completed" {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, alice, "POST", path+"/start", "{}", version)
	if w.Code != 200 {
		t.Fatal("idempotent start failed", w.Code, w.Body)
	}
	view := mailRequest(a, alice, "GET", "/api/v2/tracks/"+track.ID+"/tags", "", 0)
	var tags TrackTagsView
	if json.Unmarshal(view.Body.Bytes(), &tags) != nil || tags.PersonalVersion != 2 || len(tags.Personal) != 1 {
		t.Fatal(view.Code, view.Body)
	}
}

func TestTagBatchHTTPStrictRequestsAndFeatureGate(t *testing.T) {
	a, token := tagHTTPAPI(t)
	if _, err := a.store.db.Exec(tagBatchSchema); err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
	id := a.store.Tracks()[0].ID
	for _, body := range []string{`null`, `{"scope":"personal","user_id":"other","track_ids":[]}`, `{"scope":"personal","query":{"q":"","injected":true}}`, `{"scope":"personal","track_ids":[]} {}`, `{"scope":"personal","track_ids":["bad"]}`} {
		w := mailRequest(a, token, "POST", "/api/v2/tag-batches", body, 0)
		if w.Code != 400 {
			t.Fatal(body, w.Code, w.Body)
		}
	}
	w := mailRequest(a, token, "POST", "/api/v2/tag-batches", fmt.Sprintf(`{"scope":"personal","track_ids":[%q]}`, id), 0)
	var job TagBatch
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &job) != nil {
		t.Fatal(w.Code, w.Body)
	}
	for _, action := range []string{"start", "cancel", "retry"} {
		w = mailRequest(a, token, "POST", "/api/v2/tag-batches/"+job.ID+"/"+action, `{"user_id":"other"}`, job.Version)
		if w.Code != 400 {
			t.Fatal(action, w.Code, w.Body)
		}
	}
	r := httptest.NewRequest("POST", "http://example.com/api/v2/tag-batches", strings.NewReader(`{}`))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF accepted", w.Code, w.Body)
	}
	old, _ := testAPIForSchema(t, 7)
	_, _, oldToken := loginTestAccount(t, old.store, "admin")
	w = mailRequest(old, oldToken, "POST", "/api/v2/tag-batches", `{}`, 0)
	if w.Code != 503 {
		t.Fatal("schema7 enabled batch", w.Code, w.Body)
	}
}

func TestTagBatchHTTPRevocationDuringBody(t *testing.T) {
	a, token := tagHTTPAPI(t)
	if _, err := a.store.db.Exec(tagBatchSchema); err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
	u, session, ok := a.store.sessionIdentity(token)
	if !ok {
		t.Fatal("inactive session")
	}
	body := &resumeReadHook{Reader: strings.NewReader(fmt.Sprintf(`{"scope":"personal","track_ids":[%q]}`, a.store.Tracks()[0].ID)), after: func() {
		if err := a.store.RevokeSession(u.ID, session.ID); err != nil {
			t.Fatal(err)
		}
	}}
	r := httptest.NewRequest("POST", "http://example.com/api/v2/tag-batches", io.NopCloser(body))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("X-CSRF-Token", csrfForToken(token))
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
	var count int
	if err := a.store.db.QueryRow("SELECT count(*) FROM tag_batches").Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked body created preview", count, err)
	}
}

func TestTagBatchHTTPRecentSummariesRecoverOwnedTaskWithoutPrivateSets(t *testing.T) {
	a, admin := tagHTTPAPI(t)
	_, alice := readyMember(t, a, "batch-history-alice")
	_, bob := readyMember(t, a, "batch-history-bob")
	tag := createHTTPTag(t, a, alice, "personal", "History private tag")
	track := a.store.Tracks()[0]
	w := mailRequest(a, alice, "POST", "/api/v2/tag-batches", fmt.Sprintf(`{"scope":"personal","track_ids":[%q],"add_ids":[%q]}`, track.ID, tag.ID), 0)
	var job TagBatch
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &job) != nil {
		t.Fatal(w.Code, w.Body)
	}
	if _, err := a.store.db.Exec("UPDATE tag_batches SET payload=json_set(payload,'$.state','running') WHERE id=?", job.ID); err != nil {
		t.Fatal(err)
	}
	w = mailRequest(a, alice, "GET", "/api/v2/tag-batches", "", 0)
	var summaries []map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &summaries) != nil || len(summaries) != 1 {
		t.Fatal(w.Code, w.Body)
	}
	if summaries[0]["id"] != job.ID || summaries[0]["state"] != "interrupted" || summaries[0]["version"] != float64(job.Version+1) {
		t.Fatal(w.Body)
	}
	for _, private := range []string{track.ID, tag.ID, tag.Name, `"items"`, `"add_ids"`, `"remove_ids"`, `"common_tag_ids"`, `"partial_tag_ids"`} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("summary exposed private sets", private, w.Body)
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}
	for _, token := range []string{admin, bob} {
		w = mailRequest(a, token, "GET", "/api/v2/tag-batches", "", 0)
		if w.Code != 200 || strings.Contains(w.Body.String(), job.ID) {
			t.Fatal("foreign task summary leaked", w.Code, w.Body)
		}
	}
	w = mailRequest(a, alice, "GET", "/api/v2/tag-batches?user_id=other", "", 0)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	old, _ := testAPIForSchema(t, 7)
	_, _, token := loginTestAccount(t, old.store, "admin")
	w = mailRequest(old, token, "GET", "/api/v2/tag-batches", "", 0)
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body)
	}
}
