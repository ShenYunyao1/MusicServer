package music

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRound3MethodAndSlashMatrix(t *testing.T) {
	a, _ := testAPI(t)
	u, _, token := loginTestAccount(t, a.store, "admin")
	for _, item := range []struct{ method, path, allow string }{
		{"DELETE", "/api/v2/subscriptions/unknown/tracks/unknown/", "PATCH"},
		{"GET", "/api/v2/admin/storage/quotas/" + u.ID + "/", "PUT"},
		{"PUT", "/api/v2/tag-groups/", "GET, POST"},
		{"DELETE", "/api/v2/saved-track-filters/", "GET, POST"},
		{"GET", "/api/v2/playlists/unknown/editor/", "PUT"},
		{"DELETE", "/api/v2/playlists/unknown/tracks/order/", "PUT"},
	} {
		w := mailRequest(a, token, item.method, item.path, "not-json", 0)
		if w.Code != 405 || w.Header().Get("Allow") != item.allow {
			t.Errorf("%s %s: %d %q %s", item.method, item.path, w.Code, w.Header().Get("Allow"), w.Body)
		}
	}
	for _, path := range []string{"/api/v2/auth/keys/", "/api/v2/browse/summary/", "/api/v2/tag-groups/", "/api/v2/auth/me/"} {
		w := mailRequest(a, token, "GET", path, "", 0)
		if w.Code != 200 {
			t.Errorf("GET %s: %d %s", path, w.Code, w.Body)
		}
	}
	for _, path := range []string{"/api/v2/auth/keys/id/unknown", "/api/v2/auth/keys/id/extra/deep", "/api/v2/admin/storage/quotas/id/extra"} {
		if routeMethods(path) != "" {
			t.Fatal("unknown suffix registered", path)
		}
		w := mailRequest(a, token, "DELETE", path, "", 1)
		if w.Code != 404 {
			t.Errorf("unknown %s: %d %s", path, w.Code, w.Body)
		}
	}
	// Canonicalization must not erase repeated separators or invent a valid route.
	if routeMethods("/api/v2/tracks//") != "" {
		t.Fatal("double slash accepted")
	}
	r := httptest.NewRequest("GET", "http://example.com/api/v2/admin/storage/quotas/"+u.ID+"/", nil)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("method leaked before authentication", w.Code)
	}
	_, memberToken := readyMember(t, a, "method-member-r3")
	w = mailRequest(a, memberToken, "GET", "/api/v2/admin/storage/quotas/"+u.ID+"/", "", 0)
	if w.Code != http.StatusForbidden {
		t.Fatal("admin method leaked to member", w.Code, w.Body)
	}
}
