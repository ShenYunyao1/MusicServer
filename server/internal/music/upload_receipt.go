package music

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const uploadReceiptPrefix = "upload_receipt/"

type uploadCommitReceipt struct {
	Owner     string `json:"owner"`
	TrackID   string `json:"track_id"`
	Duplicate bool   `json:"duplicate"`
	CreatedAt string `json:"created_at"`
}

func recordUploadReceipt(tx *sql.Tx, id, owner, trackID string, duplicate bool) error {
	raw, err := json.Marshal(uploadCommitReceipt{owner, trackID, duplicate, persistedTime(time.Now())})
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?)", uploadReceiptPrefix+id, string(raw))
	return err
}

// Receipts acknowledge an already completed transaction. They intentionally
// do not reapply metadata/tags or require the upload-email policy again.
func (a *API) uploadReceipt(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/api/v2/uploads/receipts/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if !a.requireAccount(w, r) {
		return true
	}
	auth := authFromRequest(r)
	if err := resumeActor(a.store.db, auth); err != nil {
		writeResumeError(w, err)
		return true
	}
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		return true
	}
	id := strings.TrimPrefix(r.URL.Path, prefix)
	if !validResumeID(id) {
		writeResumeError(w, errResourceHidden)
		return true
	}
	var raw string
	if err := a.store.db.QueryRow("SELECT value FROM meta WHERE key=?", uploadReceiptPrefix+id).Scan(&raw); err != nil {
		writeResumeError(w, errResourceHidden)
		return true
	}
	var receipt uploadCommitReceipt
	if json.Unmarshal([]byte(raw), &receipt) != nil || receipt.Owner != auth.userID {
		writeResumeError(w, errResourceHidden)
		return true
	}
	created, err := time.Parse(time.RFC3339Nano, receipt.CreatedAt)
	if err != nil || !time.Now().Before(created.Add(24*time.Hour)) {
		writeResumeError(w, errResourceHidden)
		return true
	}
	track, err := readTrackFrom(a.store.db, receipt.TrackID, false)
	if err != nil {
		writeResumeError(w, errResourceHidden)
		return true
	}
	writeJSON(w, 200, map[string]any{"track": publicTrack(track, auth), "duplicate": receipt.Duplicate})
	return true
}

func (s *Store) cleanupUploadReceipts() error {
	_, err := s.db.Exec("DELETE FROM meta WHERE key LIKE 'upload_receipt/%' AND json_extract(value,'$.created_at')<?", persistedTime(time.Now().Add(-24*time.Hour)))
	return err
}
