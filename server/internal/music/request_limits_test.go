package music

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUploadBodyClassificationCoversSlowMediaWritesOnly(t *testing.T) {
	for _, item := range []struct {
		method, path, content string
		upload                bool
	}{
		{"POST", "/api/v2/uploads/stage", "multipart/form-data", true},
		{"PUT", "/api/v2/tracks/id/cover", "multipart/form-data", true},
		{"PUT", "/api/v2/tracks/id/lyrics", "multipart/form-data", true},
		{"PUT", "/api/v2/playlists/id/editor", "multipart/form-data", true},
		{"POST", "/api/v2/playlists", "multipart/form-data", true},
		{"PATCH", resumableAPIPath + "/" + strings.Repeat("a", 32), "application/octet-stream", true},
		{"POST", resumableAPIPath + "/" + strings.Repeat("a", 32) + "/finish", "application/json", true},
		{"POST", "/api/v2/uploads-unknown", "application/json", false},
		{"POST", resumableAPIPath, "application/json", false},
		{"DELETE", "/api/v2/uploads/stage/id", "", false},
	} {
		r := httptest.NewRequest(item.method, item.path, nil)
		r.Header.Set("Content-Type", item.content)
		if uploadBodyRequest(r) != item.upload {
			t.Error(item)
		}
	}
}

func TestSessionConcurrencyUsesAccountNotCookieAndUploadBudgetIsShared(t *testing.T) {
	a := NewAPI(nil)
	r := httptest.NewRequest("GET", "/api/v2/auth/me", nil)
	leases := []func(){}
	defer func() {
		for _, release := range leases {
			release()
		}
	}()
	for i := 0; i < 24; i++ {
		release, ok := a.acquireSessionRequest(httptest.NewRecorder(), r, authContext{userID: "alice", keyID: "cookie-a"})
		if !ok {
			t.Fatal("normal quota too small", i)
		}
		leases = append(leases, release)
	}
	w := httptest.NewRecorder()
	if release, ok := a.acquireSessionRequest(w, r, authContext{userID: "alice", keyID: "cookie-b"}); ok {
		release()
		t.Fatal("new cookie bypassed account quota")
	}
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal(w.Code, w.Header())
	}
	if release, ok := a.acquireSessionRequest(httptest.NewRecorder(), r, authContext{userID: "bob"}); !ok {
		t.Fatal("another account was limited")
	} else {
		release()
	}
	for _, release := range leases {
		release()
	}
	leases = nil
	upload := httptest.NewRequest("PUT", "/api/v2/tracks/id/cover", nil)
	for i := 0; i < 2; i++ {
		release, ok := a.acquireSessionRequest(httptest.NewRecorder(), upload, authContext{userID: "alice"})
		if !ok {
			t.Fatal(i)
		}
		leases = append(leases, release)
	}
	w = httptest.NewRecorder()
	if release, ok := a.acquireSessionRequest(w, upload, authContext{userID: "alice", keyID: "different-cookie"}); ok {
		release()
		t.Fatal("upload quota bypassed")
	}
}

type deadlineResponse struct {
	*httptest.ResponseRecorder
	read, write time.Time
}

func (w *deadlineResponse) SetReadDeadline(deadline time.Time) error  { w.read = deadline; return nil }
func (w *deadlineResponse) SetWriteDeadline(deadline time.Time) error { w.write = deadline; return nil }
func TestMediaTransferProgressRenewsDeadlines(t *testing.T) {
	w := &deadlineResponse{ResponseRecorder: httptest.NewRecorder()}
	controller := http.NewResponseController(w)
	body := &progressBody{io.NopCloser(strings.NewReader("chunk")), controller}
	data, err := io.ReadAll(body)
	if err != nil || string(data) != "chunk" {
		t.Fatal(string(data), err)
	}
	response := &progressResponse{w, controller}
	if _, err := response.Write([]byte("response")); err != nil {
		t.Fatal(err)
	}
	for _, deadline := range []time.Time{w.read, w.write} {
		left := time.Until(deadline)
		if left < 119*time.Second || left > 121*time.Second {
			t.Fatal("wrong idle deadline", left)
		}
	}
}
