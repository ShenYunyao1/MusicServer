package music

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRound2HTTPMethodsAndMissingResources(t *testing.T) {
	a, _ := testAPI(t)
	u, _, token := loginTestAccount(t, a.store, "admin")
	for _, item := range []struct{ method, path, allow string }{
		{"OPTIONS", "/api/v2/setup", "GET, POST"}, {"POST", "/api/v2/auth/keys/stats", "GET"},
		{"GET", "/api/v2/admin/users/unknown/release-email", "POST"}, {"PUT", "/api/v2/admin/users/" + u.ID, "PATCH, DELETE"},
		{"PUT", "/api/v2/admin/backups/", "GET, POST"},
	} {
		w := memberVersionRequest(a, token, item.method, item.path, "{}", u.Version)
		if w.Code != 405 || w.Header().Get("Allow") != item.allow {
			t.Errorf("%s %s: %d %q %s", item.method, item.path, w.Code, w.Header().Get("Allow"), w.Body)
		}
	}
	for _, item := range []struct{ method, path, body string }{
		{"DELETE", "/api/v2/playlists/missing", ""}, {"POST", "/api/v2/admin/removed/missing", ""}, {"PUT", "/api/v2/admin/storage/quotas/missing", `{"limit_bytes":100,"version":0}`},
	} {
		w := memberVersionRequest(a, token, item.method, item.path, item.body, 1)
		if w.Code != 404 || w.Header().Get("X-Error-Code") != "NOT_FOUND" || strings.Contains(w.Body.String(), "sql:") {
			t.Errorf("missing %s: %d %s", item.path, w.Code, w.Body)
		}
	}
}

func TestRound2UploadDeadlineMapsToInterrupted(t *testing.T) {
	for _, write := range []func(*httptest.ResponseRecorder){func(w *httptest.ResponseRecorder) { writeResumeError(w, os.ErrDeadlineExceeded) }, func(w *httptest.ResponseRecorder) {
		writeUploadBodyError(w, os.ErrDeadlineExceeded, "UPLOAD_INVALID", "invalid upload")
	}} {
		w := httptest.NewRecorder()
		write(w)
		if w.Code != 408 || w.Header().Get("X-Error-Code") != "UPLOAD_INTERRUPTED" {
			t.Fatal(w.Code, w.Body)
		}
	}
}
