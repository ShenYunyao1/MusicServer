package music

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func tagSyncRequest(a *API, token, conditional string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "http://example.com/api/v2/tag-sync", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("If-None-Match", conditional)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func tagSyncSnapshot(t *testing.T, a *API, token string) map[string]any {
	t.Helper()
	w := tagSyncRequest(a, token, "")
	var result map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result) != 5 {
		t.Fatal(w.Code, w.Body)
	}
	return result
}

func TestTagSyncPrivateChangesAndConditionalResponses(t *testing.T) {
	a, adminToken := tagHTTPAPI(t)
	member, memberToken := readyMember(t, a, "tag-sync-owner")
	initial := tagSyncSnapshot(t, a, memberToken)
	adminBefore := tagSyncSnapshot(t, a, adminToken)
	if initial["account_id"] != member.ID {
		t.Fatal(initial)
	}
	w := tagSyncRequest(a, memberToken, "")
	etag := w.Header().Get("ETag")
	if etag == "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}
	if w = tagSyncRequest(a, memberToken, "W/"+etag); w.Code != 304 || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Body)
	}
	stop := a.store.watchCommunity()
	defer stop()
	signal := a.store.communitySignal()
	created := mailRequest(a, memberToken, "POST", "/api/v2/tags", `{"scope":"personal","group_id":"genre","name":"Private secret name"}`, 0)
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body)
	}
	afterTag := tagSyncSnapshot(t, a, memberToken)
	if afterTag["personal"] == initial["personal"] || afterTag["public"] != initial["public"] || afterTag["library"] != initial["library"] || afterTag["saved"] != initial["saved"] {
		t.Fatal(initial, afterTag)
	}
	if other := tagSyncSnapshot(t, a, adminToken); !reflect.DeepEqual(other, adminBefore) {
		t.Fatal("foreign private change leaked", adminBefore, other)
	}
	create := mailRequest(a, memberToken, "POST", "/api/v2/saved-track-filters", `{"name":"Secret filter","filter":{"version":1}}`, 0)
	if create.Code != 201 {
		t.Fatal(create.Code, create.Body)
	}
	var filter SavedTrackFilter
	if err := json.Unmarshal(create.Body.Bytes(), &filter); err != nil {
		t.Fatal(err)
	}
	afterSaved := tagSyncSnapshot(t, a, memberToken)
	if afterSaved["saved"] == afterTag["saved"] || afterSaved["personal"] != afterTag["personal"] {
		t.Fatal(afterTag, afterSaved)
	}
	if other := tagSyncSnapshot(t, a, adminToken); !reflect.DeepEqual(other, adminBefore) {
		t.Fatal("foreign saved change leaked", other)
	}
	if strings.Contains(tagSyncRequest(a, memberToken, "").Body.String(), filter.ID) {
		t.Fatal("saved ID in invalidation")
	}
	if update := mailRequest(a, memberToken, "PATCH", "/api/v2/saved-track-filters/"+filter.ID, `{"name":"Secret filter"}`, 1); update.Code != 200 {
		t.Fatal(update.Code, update.Body)
	}
	if unchanged := tagSyncSnapshot(t, a, memberToken); !reflect.DeepEqual(unchanged, afterSaved) {
		t.Fatal("no-op invalidated", unchanged)
	}
	if update := mailRequest(a, memberToken, "PATCH", "/api/v2/saved-track-filters/"+filter.ID, `{"name":"Renamed"}`, 1); update.Code != 200 {
		t.Fatal(update.Code, update.Body)
	}
	if renamed := tagSyncSnapshot(t, a, memberToken); renamed["saved"] == afterSaved["saved"] {
		t.Fatal("rename did not invalidate")
	}
	if deleted := mailRequest(a, memberToken, "DELETE", "/api/v2/saved-track-filters/"+filter.ID, "", 2); deleted.Code != 204 {
		t.Fatal(deleted.Code, deleted.Body)
	}
	if deleted := tagSyncSnapshot(t, a, memberToken); deleted["saved"] != initial["saved"] {
		t.Fatal("empty content digest", initial, deleted)
	}
	select {
	case <-signal:
		t.Fatal("private mutations woke public stream")
	default:
	}
	if w = tagSyncRequest(a, memberToken, etag); w.Code != 200 {
		t.Fatal("stale etag", w.Code)
	}
	if w = tagSyncRequest(a, adminToken, etag); w.Code != 200 {
		t.Fatal("foreign etag accepted", w.Code)
	}
}

func TestTagSyncSchemaMethodQueryAndRevocation(t *testing.T) {
	a, _ := testAPIForSchema(t, 7)
	_, _, token := loginTestAccount(t, a.store, "admin")
	if w := tagSyncRequest(a, token, ""); w.Code != 503 || !strings.Contains(w.Body.String(), "TAGS_UNAVAILABLE") {
		t.Fatal(w.Code, w.Body)
	}
	a, token = tagHTTPAPI(t)
	for _, entry := range []struct {
		method, path string
		status       int
	}{{"POST", "/api/v2/tag-sync", 405}, {"GET", "/api/v2/tag-sync?user_id=someone", 400}} {
		if w := mailRequest(a, token, entry.method, entry.path, "", 0); w.Code != entry.status {
			t.Fatal(entry, w.Code, w.Body)
		}
	}
	first := tagSyncRequest(a, token, "")
	if _, err := a.store.db.Exec("UPDATE sessions SET revoked_at='revoked'"); err != nil {
		t.Fatal(err)
	}
	if w := tagSyncRequest(a, token, first.Header().Get("ETag")); w.Code != 401 {
		t.Fatal("conditional revoked", w.Code, w.Body)
	}
}

func TestTagSyncDictionaryLinksLifecycleAndPlayerKey(t *testing.T) {
	a, admin, alice, own, _, other := tagLinkFixture(t)
	s := a.store
	before, err := s.tagSyncState(own)
	if err != nil {
		t.Fatal(err)
	}
	otherBefore, err := s.tagSyncState(other)
	if err != nil {
		t.Fatal(err)
	}
	public := mustLinkTag(t, s, admin, "public", "Shared sync label")
	after, err := s.tagSyncState(own)
	if err != nil || after.Public <= before.Public || after.Library != before.Library {
		t.Fatal(before, after, err)
	}
	name := "Renamed sync label"
	if _, err = s.UpdateTag(admin, public.ID, TagPatch{Name: &name}, public.Version); err != nil {
		t.Fatal(err)
	}
	renamed, err := s.tagSyncState(own)
	if err != nil || renamed.Public <= after.Public {
		t.Fatal(after, renamed, err)
	}
	personal := mustLinkTag(t, s, own, "personal", "Private relation")
	before, err = s.tagSyncState(own)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeTrackTags(own, s.Tracks()[0].ID, "personal", 1, []string{personal.ID}, nil); err != nil {
		t.Fatal(err)
	}
	after, err = s.tagSyncState(own)
	if err != nil || after.Personal <= before.Personal || after.Library != before.Library || after.Public != before.Public {
		t.Fatal(before, after, err)
	}
	otherAfter, err := s.tagSyncState(other)
	if err != nil || otherAfter.Personal != otherBefore.Personal || otherAfter.Saved != otherBefore.Saved {
		t.Fatal(otherBefore, otherAfter, err)
	}
	if err = s.SetUserEnabled(alice.ID, false, alice.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.tagSyncState(own); !errors.Is(err, errTagSession) {
		t.Fatal("disabled", err)
	}
	alice, _ = readUser(s.db, alice.ID)
	if err = s.SetUserEnabled(alice.ID, true, alice.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.tagSyncState(own); !errors.Is(err, errTagSession) {
		t.Fatal("old session reactivated", err)
	}
	newAuth := tagMemberAuth(t, s, alice.Username)
	active, err := s.tagSyncState(newAuth)
	if err != nil || active.Personal != after.Personal || active.Saved != after.Saved {
		t.Fatal("reactivation lost state", active, after, err)
	}
	key, err := s.CreateAPIKey(APIKeyCreateRequest{Name: "No tag sync", AllowedPlaylistIDs: []string{"library"}}, MutationOptions{UserID: admin.userID})
	if err != nil {
		t.Fatal(err)
	}
	if w := callAPIWithKey(a, "GET", "/api/v2/tag-sync", "", key.Secret); w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
}
