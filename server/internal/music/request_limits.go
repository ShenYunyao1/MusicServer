package music

import (
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Classify actual body-bearing routes rather than exempting an entire prefix.
func uploadBodyRequest(r *http.Request) bool {
	if r.Method == "GET" || r.Method == "HEAD" || r.Method == "DELETE" {
		return false
	}
	p := r.URL.Path
	if p == "/api/v2/uploads" || p == "/api/v2/uploads/stage" {
		return r.Method == "POST"
	}
	if strings.HasPrefix(p, resumableAPIPath+"/") {
		parts := strings.Split(strings.TrimPrefix(p, resumableAPIPath+"/"), "/")
		return len(parts) == 1 && validResumeID(parts[0]) && r.Method == "PATCH" || len(parts) == 2 && validResumeID(parts[0]) && parts[1] == "finish" && r.Method == "POST"
	}
	if strings.HasPrefix(p, "/api/v2/tracks/") && (strings.HasSuffix(p, "/cover") || strings.HasSuffix(p, "/lyrics")) {
		return r.Method == "PUT"
	}
	if strings.HasPrefix(p, "/api/v2/uploads/stage/") && strings.HasSuffix(p, "/cover") {
		return r.Method == "PUT"
	}
	if p == "/api/v2/auth/avatar" {
		return r.Method == "PUT"
	}
	return (p == "/api/v2/playlists" || strings.HasPrefix(p, "/api/v2/playlists/") && strings.HasSuffix(p, "/editor")) && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data")
}

type progressBody struct {
	io.ReadCloser
	controller *http.ResponseController
	started    time.Time
	now        func() time.Time
}

const maxUploadRequestDuration = 30 * time.Minute

func (b *progressBody) Read(data []byte) (int, error) {
	now := time.Now()
	if b.now != nil {
		now = b.now()
	}
	if b.started.IsZero() {
		b.started = now
	}
	end := b.started.Add(maxUploadRequestDuration)
	if !now.Before(end) {
		return 0, os.ErrDeadlineExceeded
	}
	deadline := now.Add(2 * time.Minute)
	if deadline.After(end) {
		deadline = end
	}
	_ = b.controller.SetReadDeadline(deadline)
	return b.ReadCloser.Read(data)
}

type progressResponse struct {
	http.ResponseWriter
	controller *http.ResponseController
}

func (w *progressResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *progressResponse) Write(data []byte) (int, error) {
	_ = w.controller.SetWriteDeadline(time.Now().Add(2 * time.Minute))
	return w.ResponseWriter.Write(data)
}

func (a *API) acquireSessionRequest(w http.ResponseWriter, r *http.Request, auth authContext) (func(), bool) {
	category, limit := "requests", 24
	if strings.HasPrefix(r.URL.Path, "/api/v2/media/") || strings.HasPrefix(r.URL.Path, "/api/v2/covers/") {
		category, limit = "downloads", 8
	}
	upload := uploadBodyRequest(r)
	if upload {
		category, limit = "uploads", 2
	}
	lease := authContext{keyID: "account/" + auth.userID + "/" + category, maxConcurrency: limit}
	if !a.acquire(lease) {
		w.Header().Set("Retry-After", "1")
		writeError(w, 429, "ACCOUNT_CONCURRENCY_LIMIT", "account request limit reached; retry shortly")
		return nil, false
	}
	release := func() { a.release(lease) }
	if upload {
		select {
		case a.uploadRequests <- struct{}{}:
			return func() { <-a.uploadRequests; release() }, true
		default:
			release()
			w.Header().Set("Retry-After", "1")
			writeError(w, 429, "UPLOAD_BUSY", "too many active upload requests; retry shortly")
			return nil, false
		}
	}
	return release, true
}
