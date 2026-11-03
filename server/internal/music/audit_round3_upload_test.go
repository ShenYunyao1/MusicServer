package music

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRound3DiscardRollbackKeepsPreviewFile(t *testing.T) {
	a, root := testAPI(t)
	s := a.store
	u, _ := readyMember(t, a, "discard-rollback-r3")
	admin, _, _ := loginTestAccount(t, s, "admin")
	p, err := s.StageAudio("rollback.wav", bytes.NewReader(validTestAudio(t)), nil, MutationOptions{UserID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	var path string
	if err := s.db.QueryRow("SELECT temp_path FROM upload_staging WHERE id=?", p.ID).Scan(&path); err != nil {
		t.Fatal(err)
	}
	s.SetUserEnabled(u.ID, false, u.Version)
	u, _ = readUser(s.db, u.ID)
	if _, err := s.db.Exec("CREATE TRIGGER reject_discard BEFORE DELETE ON upload_staging BEGIN SELECT RAISE(ABORT,'discard rollback'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeUser(u.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: u.Version}); err == nil {
		t.Fatal("expected rollback")
	}
	if _, err := os.Stat(filepath.Join(root, path)); err != nil {
		t.Fatal("SQL rollback destroyed preview file", err)
	}
}

func TestRound3PurgeRetainsCheckpointWhenFileCleanupFails(t *testing.T) {
	a, root := testAPI(t)
	s := a.store
	u, _ := readyMember(t, a, "failed-cleanup-r3")
	admin, _, _ := loginTestAccount(t, s, "admin")
	task := resumeCreate(t, a, u, 100)
	path := filepath.Join(root, filepath.FromSlash(resumeFilePath(task.ID)))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("block deletion"), 0600); err != nil {
		t.Fatal(err)
	}
	s.SetUserEnabled(u.ID, false, u.Version)
	u, _ = readUser(s.db, u.ID)
	if err := s.PurgeUser(u.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: u.Version}); err == nil {
		t.Fatal("purge ignored failed resume cleanup")
	}
	if _, err := readResume(s.db, task.ID); err != nil {
		t.Fatal("checkpoint was lost after file cleanup failed", err)
	}
	if _, err := readUser(s.db, u.ID); err != nil {
		t.Fatal("account removed before cleanup", err)
	}
}

func TestRound3ReadyCheckpointCompactsRecoverableMetadata(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	u, token := readyMember(t, a, "metadata-budget-r3")
	task := resumeCreate(t, a, u, 100)
	task.OwnerID = u.ID
	task.State = "ready"
	task.Fields = map[string]string{"lyrics": strings.Repeat("lyrics", 10000)}
	task.Chunks = []resumableChunk{{Offset: 0, Size: 100, SHA256: strings.Repeat("a", 64)}}
	task.StageID = task.ID
	expires := time.Now().Add(time.Hour)
	task.Preview = &UploadPreview{ID: task.ID, ExpiresAt: expires, Track: Track{Lyrics: strings.Repeat("lyrics", 10000)}}
	if err := s.transaction(func(tx *sql.Tx) error { return saveResume(tx, task) }); err != nil {
		t.Fatal(err)
	}
	stored, err := readResume(s.db, task.ID)
	if err != nil || len(stored.Chunks) != 0 || len(stored.Fields) != 0 || stored.Preview == nil || stored.Preview.Track.Lyrics != "" || !stored.Preview.ExpiresAt.Equal(expires) {
		t.Fatal("ready checkpoint retained duplicate payload", err)
	}
	w := sessionRequest(a, token, "GET", "/api/v2/uploads/preflight", "")
	var response struct {
		Budget temporaryMetadataBudget `json:"metadata_budget"`
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["metadata_budget"]; !ok {
		t.Fatal("metadata budget missing from preflight", w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Budget.Used <= 0 || response.Budget.Used != response.Budget.Retained || response.Budget.Active != 0 || response.Budget.Limit <= 0 {
		t.Fatal("metadata classes inconsistent", response, w.Body, err)
	}
}

func TestRound3MetadataLimitIsDistinctFromAudioTemporaryLimit(t *testing.T) {
	w := httptest.NewRecorder()
	if !writeStorageError(w, errUploadMetadataLimit) || w.Code != 429 || w.Header().Get("Retry-After") != "60" || !strings.Contains(w.Body.String(), "UPLOAD_METADATA_LIMIT") {
		t.Fatal(w.Code, w.Body)
	}
}
