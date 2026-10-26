package music

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setEmailTestRequirement(t *testing.T, s *Store, id string, required bool) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"required": required, "quota_source": "legacy"})
	if _, err := s.db.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "account_email/"+id, string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestUploadEmailPolicyBlocksBeforeReadingAndKeepsReadOnly(t *testing.T) {
	api, root := testAPI(t)
	user, _, token := loginTestAccount(t, api.store, "admin")
	setEmailTestRequirement(t, api.store, user.ID, true)
	for _, path := range []string{"/api/v2/uploads", "/api/v2/uploads/stage", "/api/v2/uploads/resumable"} {
		t.Run(path, func(t *testing.T) {
			read := false
			request := httptest.NewRequest("POST", "http://example.com"+path, &resumeReadHook{Reader: strings.NewReader(`{"name":"blocked.wav","size":6}`), after: func() { read = true }})
			authorizeTestAdmin(api, request)
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			if response.Code != 403 || !strings.Contains(response.Body.String(), "EMAIL_VERIFICATION_REQUIRED") || read {
				t.Fatalf("upload not rejected before body: %d %s read=%v", response.Code, response.Body, read)
			}
		})
	}
	if w := sessionRequest(api, token, "GET", "/api/v2/auth/me", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := sessionRequest(api, token, "GET", "/api/v2/browse/tracks", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := sessionRequest(api, token, "GET", "/api/v2/admin/settings", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(root, ".staging")); !os.IsNotExist(err) {
		t.Fatalf("blocked upload created staging: %v", err)
	}
}

func TestUploadEmailPolicyRetainsTasksAndRejectsInFlightChange(t *testing.T) {
	api, root := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	setEmailTestRequirement(t, api.store, user.ID, false)
	task := resumeCreate(t, api, user, 6)
	var sessionID string
	if err := api.store.db.QueryRow("SELECT id FROM sessions WHERE user_id=? ORDER BY last_active_at DESC LIMIT 1", user.ID).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	auth := authContext{session: true, userID: user.ID, keyID: sessionID, authVersion: user.AuthVersion, role: user.Role}
	request := httptest.NewRequest("PATCH", resumableAPIPath+"/"+task.ID, &resumeReadHook{Reader: bytes.NewReader([]byte("abc")), after: func() { setEmailTestRequirement(t, api.store, user.ID, true) }})
	request = request.WithContext(withAuth(request.Context(), auth))
	request.Header.Set("Upload-Offset", "0")
	request.Header.Set("Upload-Checksum", "sha256 "+hashSecret("abc"))
	response := httptest.NewRecorder()
	api.resumableUploads(response, request)
	if response.Code != 403 || !strings.Contains(response.Body.String(), "EMAIL_VERIFICATION_REQUIRED") {
		t.Fatalf("in-flight policy ignored: %d %s", response.Code, response.Body)
	}
	checkpoint, err := readResume(api.store.db, task.ID)
	if err != nil || checkpoint.Offset != 0 {
		t.Fatalf("checkpoint advanced: %+v %v", checkpoint, err)
	}
	if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(resumeFilePath(task.ID)))); err != nil || info.Size() != 0 {
		t.Fatalf("unconfirmed bytes retained: %v %v", info, err)
	}
	if w := resumeCall(api, user, "GET", "/"+task.ID, nil, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := resumeCall(api, user, "DELETE", "/"+task.ID, nil, ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestUploadEmailPolicyCannotCommitPreviousStageOrDuplicate(t *testing.T) {
	api, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	setEmailTestRequirement(t, api.store, user.ID, false)
	audio := validTestAudio(t)
	preview, err := api.store.StageAudio("mail.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	setEmailTestRequirement(t, api.store, user.ID, true)
	if _, _, err = api.store.CommitUploadStage(preview.ID, MutationOptions{UserID: user.ID}); err == nil {
		t.Fatal("unverified uploader committed an old preview")
	}
	var count int
	if err := api.store.db.QueryRow("SELECT count(*) FROM upload_staging WHERE id=?", preview.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("denied commit lost cancellable preview", err, count)
	}
	setEmailTestRequirement(t, api.store, user.ID, false)
	if _, _, err = api.store.CommitUploadStage(preview.ID, MutationOptions{UserID: user.ID}); err != nil {
		t.Fatal(err)
	}
	preview, err = api.store.StageAudio("again.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	setEmailTestRequirement(t, api.store, user.ID, true)
	if _, _, err = api.store.CommitUploadStage(preview.ID, MutationOptions{UserID: user.ID}); err == nil {
		t.Fatal("dedup bypassed email requirement")
	}
}

func TestUploadEmailPolicyLegacyReadRevocation(t *testing.T) {
	api, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	setEmailTestRequirement(t, api.store, user.ID, false)
	reader := &resumeReadHook{Reader: bytes.NewReader(validTestAudio(t)), after: func() { setEmailTestRequirement(t, api.store, user.ID, true) }}
	_, err := api.store.StageAudio("during.wav", reader, nil, MutationOptions{UserID: user.ID})
	if err == nil {
		t.Fatal("legacy stage ignored verification requirement change")
	}
	var count int
	_ = api.store.db.QueryRow("SELECT count(*) FROM upload_staging").Scan(&count)
	if count != 0 {
		t.Fatal("denied legacy stage persisted")
	}
}

func TestUploadEmailPolicyResumeReadyCannotFinishAgain(t *testing.T) {
	api, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	setEmailTestRequirement(t, api.store, user.ID, false)
	audio := validTestAudio(t)
	task := resumeCreate(t, api, user, len(audio))
	if w := resumeCall(api, user, "PATCH", "/"+task.ID, audio, "0"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := resumeCall(api, user, "POST", "/"+task.ID+"/finish", []byte(`{}`), ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	setEmailTestRequirement(t, api.store, user.ID, true)
	if w := resumeCall(api, user, "POST", "/"+task.ID+"/finish", []byte(`{}`), ""); w.Code != 403 {
		t.Fatal("ready finish escaped policy", w.Code, w.Body)
	}
	if w := resumeCall(api, user, "DELETE", "/"+task.ID, nil, ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
}
