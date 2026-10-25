package music

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"musicserver/internal/diskspace"
)

func resumeCall(api *API, user User, method, path string, body []byte, offset string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/v2/uploads/resumable"+path, bytes.NewReader(body))
	r = r.WithContext(withAuth(r.Context(), authContext{session: true, userID: user.ID, authVersion: user.AuthVersion, role: user.Role}))
	if method == "PATCH" {
		hash := sha256.Sum256(body)
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("Upload-Offset", offset)
		r.Header.Set("Upload-Checksum", "sha256 "+hex.EncodeToString(hash[:]))
	}
	w := httptest.NewRecorder()
	api.resumableUploads(w, r)
	return w
}

func resumeCreate(t *testing.T, api *API, user User, size int) resumableUpload {
	t.Helper()
	w := resumeCall(api, user, "POST", "", []byte(fmt.Sprintf(`{"name":"resume.wav","size":%d}`, size)), "")
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var task resumableUpload
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestResumableCheckpointReplayAndRecovery(t *testing.T) {
	api, root := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	task := resumeCreate(t, api, user, 12)
	for i := 0; i < 2; i++ {
		w := resumeCall(api, user, "PATCH", "/"+task.ID, []byte("first!"), "0")
		if w.Code != 200 || w.Header().Get("Upload-Offset") != "6" {
			t.Fatalf("chunk/replay: %d %s", w.Code, w.Body)
		}
	}
	for _, input := range []struct{ offset, body string }{{"0", "wrong!"}, {"7", "last!!"}, {"-1", "last!!"}, {"6", "oversize!"}} {
		if w := resumeCall(api, user, "PATCH", "/"+task.ID, []byte(input.body), input.offset); w.Code < 400 {
			t.Fatalf("accepted invalid chunk: %+v", input)
		}
	}
	file := filepath.Join(root, ".staging", "resume", task.ID+".part")
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("unconfirmed"))
	_ = f.Close()
	// A fresh Store has no in-memory knowledge of the previous checkpoint.
	reopened, err := NewStore(api.store.dataPath, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	api = NewAPI(reopened)
	if w := resumeCall(api, user, "GET", "/"+task.ID, nil, ""); w.Code != 200 {
		t.Fatalf("recover: %d %s", w.Code, w.Body)
	}
	if info, err := os.Stat(file); err != nil || info.Size() != 6 {
		t.Fatalf("tail not rolled back: %v %v", info, err)
	}
	if w := resumeCall(api, user, "PATCH", "/"+task.ID, []byte("last!!"), "6"); w.Code != 200 {
		t.Fatalf("continue: %d %s", w.Code, w.Body)
	}
	if content, _ := os.ReadFile(file); string(content) != "first!last!!" {
		t.Fatalf("content %q", content)
	}
}

func TestResumableOwnerSpaceAndCancellation(t *testing.T) {
	api, root := testAPI(t)
	admin, _, _ := loginTestAccount(t, api.store, "admin")
	member, _ := readyMember(t, api, "resume-member")
	task := resumeCreate(t, api, member, 9)
	for _, method := range []string{"GET", "PATCH", "DELETE", "POST"} {
		path := "/" + task.ID
		if method == "POST" {
			path += "/finish"
		}
		if w := resumeCall(api, admin, method, path, []byte("audio"), "0"); w.Code != 404 {
			t.Fatalf("cross-owner %s: %d %s", method, w.Code, w.Body)
		}
	}
	for _, lowPath := range []string{api.store.mediaRoot, filepath.Dir(api.store.dataPath)} {
		api.store.spaceProbe = func(path string) (diskspace.Space, error) {
			if filepath.Clean(path) == filepath.Clean(lowPath) {
				return diskspace.Space{Available: 1}, nil
			}
			return diskspace.Space{Available: 1 << 40}, nil
		}
		if w := resumeCall(api, member, "PATCH", "/"+task.ID, []byte("123"), "0"); w.Code != 507 {
			t.Fatalf("low space %s: %d %s", lowPath, w.Code, w.Body)
		}
	}
	api.store.spaceProbe = nil
	for i := 0; i < 2; i++ {
		if w := resumeCall(api, member, "DELETE", "/"+task.ID, nil, ""); w.Code != 204 {
			t.Fatalf("cancel %d %s", w.Code, w.Body)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".staging", "resume", task.ID+".part")); !os.IsNotExist(err) {
		t.Fatal("canceled file retained")
	}
}

func TestResumableFinishUsesExistingPreview(t *testing.T) {
	api, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	audio := validTestAudio(t)
	task := resumeCreate(t, api, user, len(audio))
	if w := resumeCall(api, user, "PATCH", "/"+task.ID, audio, "0"); w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	var id string
	for i := 0; i < 2; i++ {
		w := resumeCall(api, user, "POST", "/"+task.ID+"/finish", []byte(`{}`), "")
		if w.Code != 200 {
			t.Fatalf("finish: %d %s", w.Code, w.Body)
		}
		var preview UploadPreview
		_ = json.Unmarshal(w.Body.Bytes(), &preview)
		if preview.Validation != "audio_stream_verified" || preview.ID == "" || (id != "" && id != preview.ID) {
			t.Fatalf("bad/repeated preview: %+v", preview)
		}
		id = preview.ID
	}
	var count int
	_ = api.store.db.QueryRow("SELECT COUNT(*) FROM upload_staging").Scan(&count)
	if count != 1 || len(api.store.Tracks()) != 1 {
		t.Fatalf("finish repeated/committed: %d", count)
	}
}

func TestResumableFinishRecoversStageBeforeCheckpoint(t *testing.T) {
	api, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	audio := validTestAudio(t)
	task := resumeCreate(t, api, user, len(audio))
	if w := resumeCall(api, user, "PATCH", "/"+task.ID, audio, "0"); w.Code != 200 {
		t.Fatal(w.Body)
	}
	// Simulate a stop after staging persisted but before finish saved its task.
	preview, err := api.store.stageAudioWithAssetsContext(context.WithValue(context.Background(), resumableStageIDKey{}, task.ID), task.Name, bytes.NewReader(audio), nil, uploadAssets{}, MutationOptions{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	if preview.ID != task.ID {
		t.Fatalf("resumable stage ID must be deterministic: %s != %s", preview.ID, task.ID)
	}
	w := resumeCall(api, user, "POST", "/"+task.ID+"/finish", []byte(`{}`), "")
	if w.Code != 200 {
		t.Fatalf("recovery finish: %d %s", w.Code, w.Body)
	}
	var count int
	_ = api.store.db.QueryRow("SELECT COUNT(*) FROM upload_staging").Scan(&count)
	if count != 1 {
		t.Fatalf("finish created another preview: %d", count)
	}
	if w := resumeCall(api, user, "DELETE", "/"+task.ID, nil, ""); w.Code != 204 {
		t.Fatalf("cancel ready: %d %s", w.Code, w.Body)
	}
	if w := resumeCall(api, user, "DELETE", "/"+task.ID, nil, ""); w.Code != 204 {
		t.Fatalf("repeat ready cancel: %d %s", w.Code, w.Body)
	}
}

func TestResumableChecksumAndSizeDoNotAdvanceCheckpoint(t *testing.T) {
	api, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	task := resumeCreate(t, api, user, 2*resumableChunkSize)
	for _, test := range []struct {
		offset, checksum string
		body             []byte
	}{
		{"0", "sha256 " + strings.Repeat("0", 64), []byte("wrong digest")},
		{"0", "md5 abc", []byte("algorithm")},
		{"0", "", []byte("missing")},
		{"1", "sha256 " + hashSecret("jump"), []byte("jump")},
		{"0", "sha256 " + hashSecret(""), nil},
		{"0", "sha256 " + hashSecret("too large"), bytes.Repeat([]byte{1}, resumableChunkSize+1)},
	} {
		r := httptest.NewRequest("PATCH", resumableAPIPath+"/"+task.ID, bytes.NewReader(test.body))
		r = r.WithContext(withAuth(r.Context(), authContext{session: true, userID: user.ID, authVersion: user.AuthVersion}))
		r.Header.Set("Upload-Offset", test.offset)
		r.Header.Set("Upload-Checksum", test.checksum)
		w := httptest.NewRecorder()
		api.resumableUploads(w, r)
		if w.Code < 400 {
			t.Fatalf("invalid chunk accepted: %d %s", w.Code, w.Body)
		}
		checkpoint, err := readResume(api.store.db, task.ID)
		if err != nil || checkpoint.Offset != 0 || len(checkpoint.Chunks) != 0 {
			t.Fatalf("invalid chunk advanced: %+v %v", checkpoint, err)
		}
	}
}

func TestResumableStartupRejectsDamagedPrefix(t *testing.T) {
	for _, damage := range []string{"modified", "short", "missing"} {
		t.Run(damage, func(t *testing.T) {
			api, root := testAPI(t)
			user, _, _ := loginTestAccount(t, api.store, "admin")
			task := resumeCreate(t, api, user, 6)
			if w := resumeCall(api, user, "PATCH", "/"+task.ID, []byte("abc"), "0"); w.Code != 200 {
				t.Fatal(w.Body)
			}
			path := filepath.Join(root, filepath.FromSlash(resumeFilePath(task.ID)))
			switch damage {
			case "modified":
				_ = os.WriteFile(path, []byte("xyz"), 0600)
			case "short":
				_ = os.Truncate(path, 2)
			case "missing":
				_ = os.Remove(path)
			}
			if err := api.store.InitializeResumableUploads(); err != nil {
				t.Fatal(err)
			}
			if w := resumeCall(api, user, "GET", "/"+task.ID, nil, ""); w.Code != 409 || !strings.Contains(w.Body.String(), "UPLOAD_CORRUPT") {
				t.Fatalf("damaged prefix advertised: %d %s", w.Code, w.Body)
			}
			if w := resumeCall(api, user, "DELETE", "/"+task.ID, nil, ""); w.Code != 204 {
				t.Fatalf("cannot cancel corrupt: %d %s", w.Code, w.Body)
			}
		})
	}
}

type resumeReadHook struct {
	io.Reader
	after func()
}

func (r *resumeReadHook) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if r.after != nil {
		f := r.after
		r.after = nil
		f()
	}
	return n, err
}

func TestResumableActorRevocationAndCancellationDuringBody(t *testing.T) {
	for _, action := range []string{"disable", "delete", "force_password", "auth_version", "cancel_context"} {
		t.Run(action, func(t *testing.T) {
			api, root := testAPI(t)
			user, _, _ := loginTestAccount(t, api.store, "admin")
			task := resumeCreate(t, api, user, 6)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &resumeReadHook{Reader: bytes.NewReader([]byte("abc")), after: func() {
				var err error
				switch action {
				case "disable":
					_, err = api.store.db.Exec("UPDATE users SET enabled=0 WHERE id=?", user.ID)
				case "delete":
					_, err = api.store.db.Exec("INSERT INTO meta VALUES(?,?)", "deleted_user/"+user.ID, "now")
				case "force_password":
					_, err = api.store.db.Exec("UPDATE users SET must_change_password=1 WHERE id=?", user.ID)
				case "auth_version":
					_, err = api.store.db.Exec("UPDATE users SET auth_version=auth_version+1 WHERE id=?", user.ID)
				case "cancel_context":
					cancel()
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			r := httptest.NewRequest("PATCH", resumableAPIPath+"/"+task.ID, body)
			r = r.WithContext(withAuth(ctx, authContext{session: true, userID: user.ID, authVersion: user.AuthVersion}))
			r.Header.Set("Upload-Offset", "0")
			r.Header.Set("Upload-Checksum", "sha256 "+hashSecret("abc"))
			w := httptest.NewRecorder()
			api.resumableUploads(w, r)
			if w.Code < 400 {
				t.Fatalf("revoked actor/canceled context advanced: %d %s", w.Code, w.Body)
			}
			checkpoint, err := readResume(api.store.db, task.ID)
			if err != nil || checkpoint.Offset != 0 {
				t.Fatalf("checkpoint advanced: %+v %v", checkpoint, err)
			}
			if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(resumeFilePath(task.ID)))); err != nil || info.Size() != 0 {
				t.Fatalf("unconfirmed data retained: %v %v", info, err)
			}
		})
	}
}

func TestResumableCleanupSkipsActiveAndExpiresTasks(t *testing.T) {
	api, root := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	task := resumeCreate(t, api, user, 3)
	checkpoint, _ := readResume(api.store.db, task.ID)
	checkpoint.ExpiresAt = time.Now().Add(-time.Minute)
	if err := api.store.transaction(func(tx *sql.Tx) error { return saveResume(tx, checkpoint) }); err != nil {
		t.Fatal(err)
	}
	lock := resumableLock(task.ID)
	lock.Lock()
	err := api.store.CleanupResumableUploads()
	lock.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(resumeFilePath(task.ID)))); err != nil {
		t.Fatal("cleanup removed active chunk")
	}
	if err := api.store.CleanupResumableUploads(); err != nil {
		t.Fatal(err)
	}
	if _, err := readResume(api.store.db, task.ID); !errors.Is(err, errResourceHidden) {
		t.Fatalf("expired checkpoint retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(resumeFilePath(task.ID)))); !os.IsNotExist(err) {
		t.Fatal("expired file retained")
	}
}

func TestResumableLimitsAndOwnerBoundTombstones(t *testing.T) {
	api, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	for i := 0; i < 16; i++ {
		resumeCreate(t, api, user, 10)
	}
	w := resumeCall(api, user, "POST", "", []byte(`{"name":"overflow.wav","size":10}`), "")
	if w.Code != 429 {
		t.Fatalf("per-account bound: %d %s", w.Code, w.Body)
	}
	var count int
	_ = api.store.db.QueryRow("SELECT COUNT(*) FROM meta WHERE key LIKE 'upload_resume/%'").Scan(&count)
	if count != 16 {
		t.Fatalf("failed create retained checkpoint: %d", count)
	}
	ids, _ := api.store.resumeIDs()
	if w := resumeCall(api, user, "DELETE", "/"+ids[0], nil, ""); w.Code != 204 {
		t.Fatal(w.Body)
	}
	resumeCreate(t, api, user, 10)
	if w := resumeCall(api, user, "DELETE", "/"+strings.Repeat("a", 32), nil, ""); w.Code != 404 {
		t.Fatalf("nonexistent differs from foreign task: %d", w.Code)
	}
	// Fill bounded global slots with checkpoints for other valid owners; the
	// global limit counts tasks independently of the requesting account.
	other, _ := readyMember(t, api, "resume-bound")
	if err := api.store.transaction(func(tx *sql.Tx) error {
		for i := 0; i < 240; i++ {
			id := fmt.Sprintf("%032x", i)
			task := resumableUpload{ID: id, OwnerID: other.ID, Size: 10, State: "uploading", ExpiresAt: time.Now().Add(time.Hour)}
			if err := saveResume(tx, task); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w = resumeCall(api, other, "POST", "", []byte(`{"name":"global.wav","size":10}`), "")
	if w.Code != 429 {
		t.Fatalf("global bound: %d %s", w.Code, w.Body)
	}
}

func TestResumableReadyPreviewExpiryAndInvalidAudio(t *testing.T) {
	api, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	task := resumeCreate(t, api, user, 3)
	if w := resumeCall(api, user, "PATCH", "/"+task.ID, []byte("bad"), "0"); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := resumeCall(api, user, "POST", "/"+task.ID+"/finish", []byte(`{}`), ""); w.Code != 400 {
		t.Fatalf("invalid audio accepted: %d %s", w.Code, w.Body)
	}
	audio := validTestAudio(t)
	task = resumeCreate(t, api, user, len(audio))
	if w := resumeCall(api, user, "PATCH", "/"+task.ID, audio, "0"); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := resumeCall(api, user, "POST", "/"+task.ID+"/finish", []byte(`{}`), ""); w.Code != 200 {
		t.Fatal(w.Body)
	}
	checkpoint, _ := readResume(api.store.db, task.ID)
	if err := api.store.DeleteUploadStage(checkpoint.StageID); err != nil {
		t.Fatal(err)
	}
	if w := resumeCall(api, user, "POST", "/"+task.ID+"/finish", []byte(`{}`), ""); w.Code != 410 {
		t.Fatalf("missing preview advertised as complete: %d %s", w.Code, w.Body)
	}
}

func TestResumableHTTPAuthenticationCSRFAndFinish(t *testing.T) {
	api, _ := testAPI(t)
	_, _, token := loginTestAccount(t, api.store, "admin")
	audio := validTestAudio(t)
	created := sessionRequest(api, token, "POST", resumableAPIPath, fmt.Sprintf(`{"name":"integrated.wav","size":%d,"attachments":{"lyrics":"integrated.lrc"}}`, len(audio)))
	if created.Code != 201 {
		t.Fatalf("route create: %d %s", created.Code, created.Body)
	}
	var task resumableUpload
	_ = json.Unmarshal(created.Body.Bytes(), &task)
	status := sessionRequest(api, token, "GET", resumableAPIPath+"/"+task.ID, "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), "integrated.lrc") {
		t.Fatalf("attachments/status: %d %s", status.Code, status.Body)
	}
	patch := func(csrf, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", "http://example.com"+resumableAPIPath+"/"+task.ID, bytes.NewReader(audio))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("Upload-Offset", "0")
		hash := sha256.Sum256(audio)
		r.Header.Set("Upload-Checksum", "sha256 "+hex.EncodeToString(hash[:]))
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	if w := patch("bad", "http://example.com"); w.Code != 403 {
		t.Fatalf("bad CSRF accepted: %d %s", w.Code, w.Body)
	}
	if w := patch(csrfForToken(token), "http://other.example"); w.Code != 403 {
		t.Fatalf("foreign origin accepted: %d %s", w.Code, w.Body)
	}
	if w := patch(csrfForToken(token), "http://example.com"); w.Code != 200 {
		t.Fatalf("route patch: %d %s", w.Code, w.Body)
	}
	if w := sessionRequest(api, token, "POST", resumableAPIPath+"/"+task.ID+"/finish", `{"lyrics":"WzAwOjAwXUJyb3dzZXIgYXR0YWNobWVudA=="}`); w.Code != 200 || !strings.Contains(w.Body.String(), "Browser attachment") {
		t.Fatalf("route finish: %d %s", w.Code, w.Body)
	}
	_, memberToken := readyMember(t, api, "resume-route-member")
	if w := sessionRequest(api, memberToken, "GET", resumableAPIPath+"/"+task.ID, ""); w.Code != 404 {
		t.Fatalf("foreign route read: %d %s", w.Code, w.Body)
	}
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "resume-read", AllowedPlaylistIDs: []string{"library"}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://example.com"+resumableAPIPath, nil)
	r.Header.Set("X-API-Key", key.Secret)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("API key accepted for uploads: %d %s", w.Code, w.Body)
	}
}
