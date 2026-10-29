package music

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
)

func avatarPNG(t *testing.T, size int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, size, size))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func avatarPut(a *API, token string, version int64, data []byte, remove bool) *httptest.ResponseRecorder {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if remove {
		_ = w.WriteField("remove", "true")
	} else {
		f, _ := w.CreateFormFile("avatar", "avatar.png")
		_, _ = f.Write(data)
	}
	_ = w.Close()
	r := httptest.NewRequest("PUT", "/api/v2/auth/avatar", &b)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("X-CSRF-Token", csrfForToken(token))
	r.Header.Set("Content-Type", w.FormDataContentType())
	if version > 0 {
		r.Header.Set("If-Match", strconv.FormatInt(version, 10))
	}
	out := httptest.NewRecorder()
	a.ServeHTTP(out, r)
	return out
}
func TestAvatarHTTPAndBackup(t *testing.T) {
	a, _ := testAPI(t)
	alice, token := readyMember(t, a, "avatar-alice")
	_, bob := readyMember(t, a, "avatar-bob")
	if w := sessionRequest(a, token, "GET", "/api/v2/auth/avatar", ""); w.Code != 200 {
		t.Fatalf("avatar metadata: %d %s", w.Code, w.Body)
	}
	data := avatarPNG(t, 256)
	w := avatarPut(a, token, 1, data, false)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	var info struct {
		Version   int64 `json:"version"`
		HasAvatar bool  `json:"has_avatar"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &info)
	if info.Version != 2 || !info.HasAvatar {
		t.Fatal(info)
	}
	if w := avatarPut(a, token, 1, data, false); w.Code != 409 {
		t.Fatal("stale", w.Code)
	}
	if w := avatarPut(a, token, 0, data, false); w.Code != 428 {
		t.Fatal("missing version", w.Code)
	}
	if w := sessionRequest(a, bob, "GET", "/api/v2/auth/avatar/content", ""); w.Code != 404 {
		t.Fatal("cross-account image", w.Code)
	}
	w = sessionRequest(a, token, "GET", "/api/v2/auth/avatar/content", "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("ETag") == "" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("content", w.Code, w.Header())
	}
	archive, err := a.store.CreateBackupBundle(context.Background(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restore")
	_, err = RestoreBackup(context.Background(), filepath.Join(a.store.backupDirectory(), archive.Name), dest)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewStore(filepath.Join(dest, "data/library.sqlite"), dest)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var value string
	if err := restored.db.QueryRow("SELECT value FROM meta WHERE key=?", "user_avatar/"+alice.ID).Scan(&value); err != nil || !bytes.Contains([]byte(value), []byte(`"version":2`)) {
		t.Fatal("avatar lost in restore", err)
	}
	var restoredRecord avatarRecord
	if err := json.Unmarshal([]byte(value), &restoredRecord); err != nil || !bytes.Equal(restoredRecord.Data, w.Body.Bytes()) {
		t.Fatal("restored avatar bytes differ", err)
	}
	if w := avatarPut(a, token, 2, nil, true); w.Code != 200 {
		t.Fatal("remove", w.Code)
	}
	if w := sessionRequest(a, token, "GET", "/api/v2/auth/avatar/content", ""); w.Code != 404 {
		t.Fatal("removed", w.Code)
	}
	key := communityKey(t, a, token, "library")
	if w := callAPIWithKey(a, "GET", "/api/v2/auth/avatar", "", key); w.Code != 403 {
		t.Fatal("key", w.Code)
	}
}
func TestAvatarInvalidImageAndRevokedSession(t *testing.T) {
	a, _ := testAPI(t)
	_, token := readyMember(t, a, "avatar-invalid")
	for _, data := range [][]byte{[]byte("<svg/>"), avatarPNG(t, 257), avatarPNG(t, 128), bytes.Repeat([]byte("x"), 512*1024+1)} {
		if w := avatarPut(a, token, 1, data, false); w.Code != 400 && w.Code != 413 {
			t.Fatal("invalid image accepted", w.Code)
		}
	}
	if w := sessionRequest(a, token, "POST", "/api/v2/auth/logout", ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := avatarPut(a, token, 1, avatarPNG(t, 256), false); w.Code != 401 {
		t.Fatal("revoked", w.Code)
	}
}

func TestAvatarAnimationJPEGAndStoredFailure(t *testing.T) {
	a, _ := testAPI(t)
	user, token := readyMember(t, a, "avatar-formats")
	raw := avatarPNG(t, 256)
	chunk := make([]byte, 20)
	binary.BigEndian.PutUint32(chunk, 8)
	copy(chunk[4:], "acTL")
	binary.BigEndian.PutUint32(chunk[8:], 2)
	binary.BigEndian.PutUint32(chunk[16:], crc32.ChecksumIEEE(chunk[4:16]))
	animated := append(append(append([]byte{}, raw[:33]...), chunk...), raw[33:]...)
	if w := avatarPut(a, token, 1, animated, false); w.Code != 400 {
		t.Fatal("APNG allowed", w.Code)
	}
	var jpegData bytes.Buffer
	_ = jpeg.Encode(&jpegData, image.NewRGBA(image.Rect(0, 0, 256, 256)), nil)
	if w := avatarPut(a, token, 1, jpegData.Bytes(), false); w.Code != 200 {
		t.Fatal("JPEG", w.Code, w.Body)
	}
	w := sessionRequest(a, token, "GET", "/api/v2/auth/avatar/content", "")
	if !bytes.HasPrefix(w.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("JPEG not normalized to PNG")
	}
	etag := w.Header().Get("ETag")
	r := httptest.NewRequest("GET", "/api/v2/auth/avatar/content?v=2", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("If-None-Match", etag)
	out := httptest.NewRecorder()
	a.ServeHTTP(out, r)
	if out.Code != 304 || out.Body.Len() != 0 {
		t.Fatal("conditional image", out.Code)
	}
	if _, err := a.store.db.Exec("UPDATE meta SET value='broken' WHERE key=?", "user_avatar/"+user.ID); err != nil {
		t.Fatal(err)
	}
	if w := sessionRequest(a, token, "GET", "/api/v2/auth/avatar", ""); w.Code != 500 {
		t.Fatal("broken record hidden", w.Code)
	}
}

func TestAvatarSecurityAndAccountCleanup(t *testing.T) {
	a, _ := testAPI(t)
	user, token := readyMember(t, a, "avatar-cleanup")
	if w := sessionRequest(a, "", "GET", "/api/v2/auth/avatar", ""); w.Code != 401 {
		t.Fatal("anonymous", w.Code)
	}
	if w := sessionRequest(a, token, "GET", "/api/v2/auth/avatar?user_id=another", ""); w.Code != 400 {
		t.Fatal("target override", w.Code)
	}
	r := httptest.NewRequest("PUT", "/api/v2/auth/avatar", bytes.NewReader(nil))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("Origin", "http://example.com")
	out := httptest.NewRecorder()
	a.ServeHTTP(out, r)
	if out.Code != 403 {
		t.Fatal("missing csrf", out.Code)
	}
	if w := avatarPut(a, token, 1, avatarPNG(t, 256), false); w.Code != 200 {
		t.Fatal(w.Code)
	}
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	if err := a.store.SetUserEnabled(user.ID, false, user.Version); err != nil {
		t.Fatal(err)
	}
	user, _ = readUser(a.store.db, user.ID)
	if err := a.store.DeleteUser(user.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: user.Version}); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = a.store.db.QueryRow("SELECT count(*) FROM meta WHERE key=?", "user_avatar/"+user.ID).Scan(&count)
	if count != 0 {
		t.Fatal("deleted account avatar retained")
	}
}

func TestAvatarTransactionSessionAndWriteFailure(t *testing.T) {
	a, _ := testAPI(t)
	user, token := readyMember(t, a, "avatar-transaction")
	if w := avatarPut(a, token, 1, avatarPNG(t, 256), false); w.Code != 200 {
		t.Fatal(w.Code)
	}
	_, err := a.store.db.Exec("CREATE TRIGGER reject_avatar BEFORE UPDATE ON meta WHEN OLD.key LIKE 'user_avatar/%' BEGIN SELECT RAISE(ABORT,'avatar unavailable'); END")
	if err != nil {
		t.Fatal(err)
	}
	if w := avatarPut(a, token, 2, nil, true); w.Code != 500 {
		t.Fatal("write failure hidden", w.Code)
	}
	record, err := readAvatar(a.store.db, user.ID)
	if err != nil || record.Version != 2 || len(record.Data) == 0 {
		t.Fatal("failed mutation changed old avatar", record.Version, err)
	}
	r := httptest.NewRequest("GET", "/api/v2/auth/avatar", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	auth, ok := a.authenticate(r)
	if !ok {
		t.Fatal("authentication")
	}
	if w := sessionRequest(a, token, "POST", "/api/v2/auth/logout", ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if _, err := a.store.saveAvatar(auth, avatarPNG(t, 256), false, 2); err != errTagSession {
		t.Fatal("transaction accepted revoked identity", err)
	}
}
