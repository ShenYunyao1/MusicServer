package music

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUploadPipelineLinkRecoveryAndCopyFallback(t *testing.T) {
	a, dir := testAPI(t)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	data := []byte("immutable bytes")
	if err := os.WriteFile(filepath.Join(dir, "source"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		n, err := a.store.materializeUpload(context.WithValue(context.Background(), uploadSourceKey{}, "source"), root, "target", bytes.NewReader(data), 100)
		if err != nil || n != int64(len(data)) {
			t.Fatal("link/recovery", n, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "collision"), []byte("do not overwrite"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.materializeUpload(context.WithValue(context.Background(), uploadSourceKey{}, "source"), root, "collision", bytes.NewReader(data), 100); err == nil {
		t.Fatal("unrelated target overwritten")
	}
	n, err := a.store.materializeUpload(context.WithValue(context.Background(), uploadSourceKey{}, "nonexistent"), root, "fallback", bytes.NewReader(data), 100)
	if err != nil || n != int64(len(data)) {
		t.Fatal("copy fallback", n, err)
	}
}

func TestUploadDuplicateStillValidatesUnverifiedScan(t *testing.T) {
	a, _ := testAPI(t)
	if _, err := a.store.StageAudio("duplicate.mp3", bytes.NewReader([]byte("0123456789")), nil); err == nil {
		t.Fatal("unvalidated scanned duplicate bypassed audio validation")
	}
}

func TestUploadStatusDoesNotWaitForFinishLock(t *testing.T) {
	a, _ := testAPI(t)
	u, _, _ := loginTestAccount(t, a.store, "admin")
	task := resumeCreate(t, a, u, 12)
	lock := resumableLock(task.ID)
	lock.Lock()
	defer lock.Unlock()
	uploadPhases.Store(uploadPhaseIdentity{a.store, task.ID}, "probing")
	defer uploadPhases.Delete(uploadPhaseIdentity{a.store, task.ID})
	done := make(chan int, 1)
	go func() { done <- resumeCall(a, u, "GET", "/"+task.ID+"/status", nil, "").Code }()
	select {
	case code := <-done:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("status blocked on finish lock")
	}
	other, _ := readyMember(t, a, "phase-foreign")
	var status map[string]any
	json.Unmarshal(resumeCall(a, u, "GET", "/"+task.ID+"/status", nil, "").Body.Bytes(), &status)
	if status["phase"] != "probing" {
		t.Fatal(status)
	}
	if w := resumeCall(a, other, "GET", "/"+task.ID+"/status", nil, ""); w.Code != 404 {
		t.Fatal("foreign", w.Code)
	}
}

func TestUploadPipelineReusesFiles(t *testing.T) {
	a, root := testAPI(t)
	u, _, _ := loginTestAccount(t, a.store, "admin")
	data := validTestAudio(t)
	task := resumeCreate(t, a, u, len(data))
	if w := resumeCall(a, u, "PATCH", "/"+task.ID, data, "0"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	source, err := os.Open(filepath.Join(root, filepath.FromSlash(resumeFilePath(task.ID))))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	original, err := source.Stat()
	if err != nil {
		t.Fatal(err)
	}
	w := resumeCall(a, u, "POST", "/"+task.ID+"/finish", []byte(`{}`), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var preview UploadPreview
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	stage, err := os.Open(filepath.Join(root, ".staging", task.ID+".wav"))
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	staged, err := stage.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(original, staged) {
		t.Fatal("resume staging copied audio instead of sharing its immutable inode")
	}
	_ = source.Close()
	_ = stage.Close()
	track, _, err := a.store.CommitUploadStage(preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	published, err := os.Open(filepath.Join(root, filepath.FromSlash(track.Path)))
	if err != nil {
		t.Fatal(err)
	}
	defer published.Close()
	final, err := published.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(original, final) {
		t.Fatal("commit copied audio")
	}
}

func TestUploadPreflightOwnAccount(t *testing.T) {
	a, _ := testAPI(t)
	u, _, _ := loginTestAccount(t, a.store, "admin")
	r := httptest.NewRequest("GET", "/api/v2/uploads/preflight", bytes.NewReader(nil))
	var sid string
	a.store.db.QueryRow("SELECT id FROM sessions WHERE user_id=? LIMIT 1", u.ID).Scan(&sid)
	r = r.WithContext(withAuth(r.Context(), authContext{session: true, userID: u.ID, keyID: sid, authVersion: u.AuthVersion, role: u.Role}))
	w := httptest.NewRecorder()
	if !a.uploadPreflight(w, r) || w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	if out["max_upload_bytes"] == nil || out["used_bytes"] == nil {
		t.Fatal(out)
	}
}

func TestUploadCollisionNeverDeletesUnrelatedFile(t *testing.T) {
	a, dir := testAPI(t)
	data := validTestAudio(t)
	id := "0123456789abcdef0123456789abcdef"
	stage := filepath.Join(dir, ".staging", id+".wav")
	if err := os.MkdirAll(filepath.Dir(stage), 0700); err != nil {
		t.Fatal(err)
	}
	keep := []byte("unrelated collision")
	if err := os.WriteFile(stage, keep, 0600); err != nil {
		t.Fatal(err)
	}
	a.store.activeStages[".staging/"+id+".wav"] = true
	ctx := context.WithValue(context.Background(), resumableStageIDKey{}, id)
	if _, err := a.store.stageAudioWithAssetsContext(ctx, "song.wav", bytes.NewReader(data), nil, uploadAssets{}); err == nil {
		t.Fatal("stage collision accepted")
	}
	if got, err := os.ReadFile(stage); err != nil || !bytes.Equal(got, keep) {
		t.Fatal("stage collision was deleted", err)
	}
	preview, err := a.store.StageAudio("song.wav", bytes.NewReader(data), nil)
	if err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(dir, "uploads", preview.ID+".wav")
	if err := os.MkdirAll(filepath.Dir(final), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, keep, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.store.CommitUploadStage(preview.ID); err == nil {
		t.Fatal("final collision accepted")
	}
	if got, err := os.ReadFile(final); err != nil || !bytes.Equal(got, keep) {
		t.Fatal("final collision was deleted", err)
	}
	if _, ok, err := a.store.UploadStage(preview.ID); err != nil || !ok {
		t.Fatal("collision lost preview", err)
	}
}

func TestUploadPipelineRejectsSymlinkSource(t *testing.T) {
	a, dir := testAPI(t)
	outside := filepath.Join(t.TempDir(), "outside.wav")
	if err := os.WriteFile(outside, []byte("do not publish"), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source-link")
	if err := os.Symlink(outside, source); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := a.store.materializeUpload(context.WithValue(context.Background(), uploadSourceKey{}, "source-link"), root, "target", bytes.NewReader([]byte("fallback")), 100); err == nil {
		t.Fatal("symlink source published")
	}
	if _, err := os.Lstat(filepath.Join(dir, "target")); !os.IsNotExist(err) {
		t.Fatal("target retained", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "do not publish" {
		t.Fatal("outside source changed", err)
	}
}
