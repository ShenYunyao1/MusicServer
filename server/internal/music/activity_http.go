package music

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"musicserver/internal/activity"
)

type countedUploadBody struct {
	io.ReadCloser
	bytes int64
}

func (body *countedUploadBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	body.bytes += int64(n)
	return n, err
}

func (s *Store) SetActivity(journal *activity.Journal) { s.activity = journal }
func (a *API) systemLogs(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	if r.Method != "GET" {
		writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	values := r.URL.Query()
	q := activity.Query{Limit: 100, Before: values.Get("before"), After: values.Get("after"), Level: values.Get("level"), Category: values.Get("category"), Search: values.Get("q")}
	if raw := values.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			writeError(w, 400, "INVALID_LIMIT", "limit must be 1-200")
			return
		}
		q.Limit = n
	}
	if len(q.Search) > 200 || q.Before != "" && q.After != "" {
		writeError(w, 400, "INVALID_FILTER", "invalid log filter")
		return
	}
	for _, cursor := range []string{q.Before, q.After} {
		if cursor != "" {
			if _, err := strconv.ParseInt(cursor, 10, 64); err != nil {
				writeError(w, 400, "INVALID_CURSOR", "invalid log cursor")
				return
			}
		}
	}
	if q.Level != "" && q.Level != "info" && q.Level != "warn" && q.Level != "error" {
		writeError(w, 400, "INVALID_LEVEL", "invalid log level")
		return
	}
	categories := map[string]bool{"server": true, "http": true, "upload": true, "scan": true, "update": true}
	if q.Category != "" && !categories[q.Category] {
		writeError(w, 400, "INVALID_CATEGORY", "invalid log category")
		return
	}
	writeJSON(w, 200, a.store.activity.Query(q))
}
func activityFields(r *http.Request) activity.Fields {
	return activity.Fields{RequestID: r.Header.Get("X-Music-Request-ID"), UserID: authFromRequest(r).userID, Method: r.Method, Route: r.URL.Path}
}
func emitActivity(j *activity.Journal, code string, r *http.Request, start time.Time, bytes int64) {
	f := activityFields(r)
	f.DurationMS = time.Since(start).Milliseconds()
	f.Bytes = bytes
	_ = j.Emit(code, f)
}
