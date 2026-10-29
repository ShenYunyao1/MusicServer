package music

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func combinedProfileRequest(token string, version int64, fields map[string]string, data []byte) *http.Request {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	if data != nil {
		f, _ := w.CreateFormFile("avatar", "avatar.png")
		_, _ = f.Write(data)
	}
	_ = w.Close()
	r := httptest.NewRequest("PUT", "/api/v2/auth/profile", &b)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("X-CSRF-Token", csrfForToken(token))
	r.Header.Set("Content-Type", w.FormDataContentType())
	if version > 0 {
		r.Header.Set("If-Match", strconv.FormatInt(version, 10))
	}
	return r
}
func profileCombinedCall(a *API, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func TestCombinedProfileAtomicAndConflicts(t *testing.T) {
	a, _ := testAPI(t)
	u, token := readyMember(t, a, "combined-member")
	png := avatarPNG(t, 256)
	call := func(version int64, name, av string, data []byte) *httptest.ResponseRecorder {
		return profileCombinedCall(a, combinedProfileRequest(token, version, map[string]string{"display_name": name, "avatar_version": av}, data))
	}
	w := call(u.Version, " New name ", "1", png)
	if w.Code != 200 {
		t.Fatalf("combined: %d %s", w.Code, w.Body)
	}
	var result struct {
		User   User       `json:"user"`
		Avatar avatarInfo `json:"avatar"`
		CSRF   string     `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.User.DisplayName != "New name" || result.User.ID != u.ID || result.Avatar.Version != 2 || !result.Avatar.HasAvatar || result.CSRF == "" {
		t.Fatal(result)
	}
	before, _ := readAvatar(a.store.db, u.ID)
	current, _ := readUser(a.store.db, u.ID)
	if current.AuthVersion != u.AuthVersion {
		t.Fatal("profile rotated authentication")
	}
	for _, c := range []struct {
		version int64
		av      string
		data    []byte
		want    int
	}{{current.Version, "1", png, 409}, {u.Version, "2", png, 409}, {current.Version, "2", []byte("bad image"), 400}} {
		w = call(c.version, "Must not apply", c.av, c.data)
		if w.Code != c.want {
			t.Fatalf("conflict %d %s", w.Code, w.Body)
		}
		afterUser, _ := readUser(a.store.db, u.ID)
		after, _ := readAvatar(a.store.db, u.ID)
		if afterUser.Version != current.Version || afterUser.DisplayName != current.DisplayName || after.Version != before.Version || !bytes.Equal(after.Data, before.Data) {
			t.Fatal("partial profile write")
		}
	}
	_, err := a.store.db.Exec("CREATE TRIGGER reject_profile_avatar BEFORE UPDATE ON meta WHEN NEW.key LIKE 'user_avatar/%' BEGIN SELECT RAISE(ABORT,'test avatar failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	w = call(current.Version, "Rollback name", "2", png)
	if w.Code != 500 {
		t.Fatalf("write failure: %d %s", w.Code, w.Body)
	}
	afterUser, _ := readUser(a.store.db, u.ID)
	if afterUser.Version != current.Version || afterUser.DisplayName != current.DisplayName {
		t.Fatal("name escaped rollback")
	}
	_, _ = a.store.db.Exec("DROP TRIGGER reject_profile_avatar")
	w = call(current.Version, "Nickname only", "", nil)
	if w.Code != 200 {
		t.Fatalf("nickname only: %d %s", w.Code, w.Body)
	}
	after, _ := readAvatar(a.store.db, u.ID)
	if !bytes.Equal(after.Data, before.Data) || after.Version != before.Version {
		t.Fatal("nickname touched avatar")
	}
	afterUser, _ = readUser(a.store.db, u.ID)
	r := combinedProfileRequest(token, afterUser.Version, map[string]string{"display_name": "Nickname only", "avatar_version": "2", "remove_avatar": "true"}, nil)
	w = profileCombinedCall(a, r)
	if w.Code != 200 {
		t.Fatalf("remove %d %s", w.Code, w.Body)
	}
	after, _ = readAvatar(a.store.db, u.ID)
	if len(after.Data) != 0 || after.Version != 3 {
		t.Fatal("remove", after)
	}
}

func TestCombinedProfileSecurityAndValidation(t *testing.T) {
	a, _ := testAPI(t)
	u, token := readyMember(t, a, "combined-security")
	png := avatarPNG(t, 256)
	for _, c := range []struct {
		fields  map[string]string
		data    []byte
		version int64
		want    int
	}{
		{map[string]string{"display_name": "x"}, png, u.Version, 428},
		{map[string]string{"display_name": "x"}, nil, 0, 428},
		{map[string]string{"display_name": " "}, nil, u.Version, 400},
		{map[string]string{"display_name": "x", "user_id": "other"}, nil, u.Version, 400},
		{map[string]string{"display_name": "x", "remove_avatar": "true", "avatar_version": "1"}, png, u.Version, 400},
	} {
		w := profileCombinedCall(a, combinedProfileRequest(token, c.version, c.fields, c.data))
		if w.Code != c.want {
			t.Fatalf("form %v: %d %s", c.fields, w.Code, w.Body)
		}
	}
	r := combinedProfileRequest(token, u.Version, map[string]string{"display_name": "x"}, nil)
	r.Header.Del("X-CSRF-Token")
	if w := profileCombinedCall(a, r); w.Code != 403 {
		t.Fatal("CSRF", w.Code)
	}
	r = combinedProfileRequest("", u.Version, map[string]string{"display_name": "x"}, nil)
	if w := profileCombinedCall(a, r); w.Code != 401 {
		t.Fatal("anonymous", w.Code)
	}
	key := communityKey(t, a, token, "library")
	if w := callAPIWithKey(a, "PUT", "/api/v2/auth/profile", "", key); w.Code != 403 {
		t.Fatal("key", w.Code)
	}
	var sessionID string
	if err := a.store.db.QueryRow("SELECT id FROM sessions WHERE user_id=? AND revoked_at=''", u.ID).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	auth := authContext{session: true, userID: u.ID, keyID: sessionID, authVersion: u.AuthVersion, role: u.Role}
	_, _ = a.store.db.Exec("UPDATE sessions SET revoked_at='revoked' WHERE id=?", auth.keyID)
	if _, _, err := a.store.saveCombinedProfile(auth, "Revoked", nil, false, false, u.Version, 0); err != errTagSession {
		t.Fatal("revoked actor", err)
	}
	current, _ := readUser(a.store.db, u.ID)
	if current.DisplayName != u.DisplayName || current.Version != u.Version {
		t.Fatal("revoked actor mutated user")
	}
	temporary := createMemberFixture(t, a, "combined-pending")
	_, _, pending, _ := a.store.Login(temporary.User.Username, temporary.Password, "combined-first")
	if w := profileCombinedCall(a, combinedProfileRequest(pending, temporary.User.Version, map[string]string{"display_name": "blocked"}, nil)); w.Code != 403 || !strings.Contains(w.Body.String(), "PASSWORD_CHANGE_REQUIRED") {
		t.Fatal("first password gate", w.Code, w.Body)
	}
	disabled, _ := readyMember(t, a, "combined-disabled")
	var disabledSession string
	_ = a.store.db.QueryRow("SELECT id FROM sessions WHERE user_id=? AND revoked_at=''", disabled.ID).Scan(&disabledSession)
	_, _ = a.store.db.Exec("UPDATE users SET enabled=0 WHERE id=?", disabled.ID)
	if _, _, err := a.store.saveCombinedProfile(authContext{session: true, userID: disabled.ID, keyID: disabledSession, authVersion: disabled.AuthVersion}, "blocked", nil, false, false, disabled.Version, 0); err != errTagSession {
		t.Fatal("disabled actor", err)
	}
}

func TestCombinedProfileAuditFailureRollsBackBoth(t *testing.T) {
	a, _ := testAPI(t)
	u, token := readyMember(t, a, "combined-audit")
	_, err := a.store.db.Exec("CREATE TRIGGER reject_combined_audit BEFORE INSERT ON audit BEGIN SELECT RAISE(ABORT,'test audit failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	w := profileCombinedCall(a, combinedProfileRequest(token, u.Version, map[string]string{"display_name": "Must rollback", "avatar_version": "1"}, avatarPNG(t, 256)))
	if w.Code != 500 {
		t.Fatal("audit failure", w.Code, w.Body)
	}
	after, _ := readUser(a.store.db, u.ID)
	av, _ := readAvatar(a.store.db, u.ID)
	if after.Version != u.Version || after.DisplayName != u.DisplayName || av.Version != 1 || len(av.Data) > 0 {
		t.Fatal("partial commit after audit failure", after, av.Version)
	}
}

func TestCombinedProfileAuthorProjectionAndAudit(t *testing.T) {
	a, author, reader, p := communityFixture(t)
	at := communityLogin(t, a.store, author.Username)
	bt := communityLogin(t, a.store, reader.Username)
	if w := memberVersionRequest(a, at, "PUT", "/api/v2/playlists/"+p.ID+"/visibility", `{"visibility":"public"}`, p.Version); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	before := communityConditional(a, bt, "public/playlists", "")
	w := profileCombinedCall(a, combinedProfileRequest(at, author.Version, map[string]string{"display_name": "Joint author", "avatar_version": "1"}, avatarPNG(t, 256)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	after := communityConditional(a, bt, "public/playlists", before.Header().Get("ETag"))
	if after.Code != 200 || !strings.Contains(after.Body.String(), "Joint author") {
		t.Fatal("stale author", after.Code, after.Body)
	}
	var changes string
	if err := a.store.db.QueryRow("SELECT changes FROM audit WHERE action='account.profile' AND entity_id=? ORDER BY id DESC LIMIT 1", author.ID).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(changes, `"data"`) || strings.Contains(changes, "iVBOR") {
		t.Fatal("image leaked to audit")
	}
}
