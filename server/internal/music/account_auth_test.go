package music

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAccountBootstrapAndPasswordStorage(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "accounts.sqlite"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.InitializeAdmin("Admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "Admin" || user.Role != "admin" {
		t.Fatalf("bad admin: %+v", user)
	}
	if _, err := store.InitializeAdmin("other", "different password here"); err == nil {
		t.Fatal("second bootstrap allowed")
	}
	var raw string
	if err := store.db.QueryRow("SELECT password_hash FROM users WHERE id=?", user.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "$argon2id$") || strings.Contains(raw, "correct horse") {
		t.Fatal("password not safely hashed")
	}
}

func sessionRequest(api *API, token, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://example.com"+path, strings.NewReader(body))
	request.Header.Set("Origin", "http://example.com")
	request.Header.Set("X-CSRF-Token", csrfForToken(token))
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
func loginTestAccount(t *testing.T, store *Store, username string) (User, Session, string) {
	t.Helper()
	user, session, token, err := store.Login(username, "test administrator password", "test-account")
	if err != nil {
		t.Fatal(err)
	}
	return user, session, token
}

func TestSessionRevocationExpiryAndActivity(t *testing.T) {
	api, _ := testAPI(t)
	user, first, token := loginTestAccount(t, api.store, "admin")
	_, second, otherToken := loginTestAccount(t, api.store, "admin")
	if w := sessionRequest(api, token, "GET", "/api/v2/auth/sessions", ""); w.Code != 200 || !strings.Contains(w.Body.String(), second.ID) {
		t.Fatalf("session listing: %d %s", w.Code, w.Body)
	}
	if w := sessionRequest(api, token, "DELETE", "/api/v2/auth/sessions/"+second.ID, ""); w.Code != 204 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body)
	}
	if _, _, ok := api.store.sessionIdentity(otherToken); ok {
		t.Fatal("revoked session still valid")
	}
	old := time.Now().Add(-time.Hour)
	if _, err := api.store.db.Exec("UPDATE sessions SET last_active_at=? WHERE id=?", persistedTime(old), first.ID); err != nil {
		t.Fatal(err)
	}
	if w := sessionRequest(api, token, "GET", "/api/v2/auth/me", ""); w.Code != 200 {
		t.Fatal(w.Body)
	}
	_, after, _ := api.store.sessionIdentity(token)
	if !after.LastActiveAt.Equal(old.UTC()) {
		t.Fatal("background read extended idle timeout")
	}
	if w := sessionRequest(api, token, "POST", "/api/v2/auth/activity", ""); w.Code != 204 {
		t.Fatal(w.Body)
	}
	_, after, _ = api.store.sessionIdentity(token)
	if time.Since(after.LastActiveAt) > time.Minute {
		t.Fatal("user activity did not renew idle time")
	}
	if _, err := api.store.db.Exec("UPDATE sessions SET expires_at=? WHERE id=?", persistedTime(time.Now().Add(-time.Second)), first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := api.store.sessionIdentity(token); ok {
		t.Fatal("absolute expiry ignored")
	}
	_, idle, idleToken := loginTestAccount(t, api.store, user.Username)
	if _, err := api.store.db.Exec("UPDATE sessions SET last_active_at=? WHERE id=?", persistedTime(time.Now().Add(-3*time.Hour)), idle.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := api.store.sessionIdentity(idleToken); ok {
		t.Fatal("idle expiry ignored")
	}
	_, _, logoutToken := loginTestAccount(t, api.store, user.Username)
	if w := sessionRequest(api, logoutToken, "POST", "/api/v2/auth/logout", ""); w.Code != 204 {
		t.Fatal(w.Body)
	}
	if _, _, ok := api.store.sessionIdentity(logoutToken); ok {
		t.Fatal("logout did not revoke session")
	}
}

func TestPasswordChangeResetAndRestart(t *testing.T) {
	api, _ := testAPI(t)
	user, _, token := loginTestAccount(t, api.store, "admin")
	_, _, otherToken := loginTestAccount(t, api.store, "admin")
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "player", AllowedPlaylistIDs: []string{"library"}})
	if err != nil {
		t.Fatal(err)
	}
	w := sessionRequest(api, token, "PUT", "/api/v2/auth/password", `{"old_password":"wrong","new_password":"a different long password"}`)
	if w.Code != 400 {
		t.Fatal("wrong current password accepted")
	}
	w = sessionRequest(api, token, "PUT", "/api/v2/auth/password", `{"old_password":"test administrator password","new_password":"a different long password"}`)
	if w.Code != 204 {
		t.Fatalf("change: %d %s", w.Code, w.Body)
	}
	for _, value := range []string{token, otherToken} {
		if _, _, ok := api.store.sessionIdentity(value); ok {
			t.Fatal("password change left old session valid")
		}
	}
	if _, ok := api.store.AuthenticateSecret(key.Secret); !ok {
		t.Fatal("ordinary password change revoked player")
	}
	if err := api.store.ResetPassword(user.Username, "test administrator password", false); err != nil {
		t.Fatal(err)
	}
	_, _, newToken := loginTestAccount(t, api.store, user.Username)
	if err := api.store.BeginServer(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := api.store.sessionIdentity(newToken); ok {
		t.Fatal("restart retained web session")
	}
	if w := callAPIWithKey(api, "GET", "/api/v2/playlists", "", key.Secret); w.Code != 200 {
		t.Fatal("restart invalidated key")
	}
	if err := api.store.ResetPassword(user.Username, "another long password", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.store.AuthenticateSecret(key.Secret); ok {
		t.Fatal("explicit key reset did not revoke player")
	}
	entries, err := queryJSON[map[string]any](api.store.db, "SELECT changes FROM audit")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(entries)
	for _, sensitive := range []string{token, otherToken, key.Secret, "test administrator password", "another long password", "$argon2id$"} {
		if strings.Contains(string(raw), sensitive) {
			t.Fatalf("audit exposed %q", sensitive)
		}
	}
}

func TestAccountsIsolateKeysPlaylistsStagesAndMedia(t *testing.T) {
	api, _ := testAPI(t)
	admin, _, token := loginTestAccount(t, api.store, "admin")
	member, err := api.store.createUser("member", "test administrator password", "member", false)
	if err != nil {
		t.Fatal(err)
	}
	setEmailTestRequirement(t, api.store, member.ID, false)
	_, _, memberToken := loginTestAccount(t, api.store, "member")
	track := api.store.Tracks()[0]
	if err := api.store.SetTrackCover(track.ID, tinyPNG()); err != nil {
		t.Fatal(err)
	}
	if err := api.store.PutPlaylist(Playlist{ID: "private", Name: "Private", TrackIDs: []string{track.ID}}, MutationOptions{UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	own := Playlist{ID: "member-list", Name: "Member", TrackIDs: []string{track.ID}}
	if err := api.store.PutPlaylist(own, MutationOptions{UserID: member.ID}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v2/playlists/private", "/api/v2/admin/audit", "/api/v2/admin/removed"} {
		w := sessionRequest(api, memberToken, "GET", path, "")
		if w.Code < 400 {
			t.Fatalf("member accessed %s: %s", path, w.Body)
		}
	}
	if w := sessionRequest(api, memberToken, "GET", "/api/v2/playlists", ""); w.Code != 200 || strings.Contains(w.Body.String(), "Private") || !strings.Contains(w.Body.String(), "Member") {
		t.Fatalf("playlist isolation: %d %s", w.Code, w.Body)
	}
	if _, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "stolen", AllowedPlaylistIDs: []string{"private"}}, MutationOptions{UserID: member.ID}); err == nil {
		t.Fatal("member key granted other owner's playlist")
	}
	player, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "own", AllowedPlaylistIDs: []string{"member-list"}}, MutationOptions{UserID: member.ID})
	if err != nil {
		t.Fatal(err)
	}
	if w := callAPIWithKey(api, "GET", "/api/v2/tracks/"+track.ID, "", player.Secret); w.Code != 200 || strings.Contains(w.Body.String(), track.Path) {
		t.Fatalf("media metadata leaked: %d %s", w.Code, w.Body)
	}
	if w := callAPIWithKey(api, "GET", "/api/v2/playlists/member-list", "", player.Secret); w.Code != 200 || strings.Contains(w.Body.String(), track.Path) {
		t.Fatalf("playlist detail leaked path: %d %s", w.Code, w.Body)
	}
	stage, err := api.store.StageAudio("private.wav", strings.NewReader(string(validTestAudio(t))), nil, MutationOptions{UserID: member.ID})
	if err != nil {
		t.Fatal(err)
	}
	if w := sessionRequest(api, token, "GET", "/api/v2/uploads/stage/"+stage.ID, ""); w.Code != 404 {
		t.Fatalf("another account's stage visible: %d", w.Code)
	}
	if err := api.store.SetUserEnabled(member.ID, false, member.Version); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := api.store.sessionIdentity(memberToken); ok {
		t.Fatal("disabled account session works")
	}
	if w := callAPIWithKey(api, "GET", "/api/v2/playlists", "", player.Secret); w.Code != 401 {
		t.Fatal("disabled account key works")
	}
	if err := api.store.SetUserEnabled(admin.ID, false, admin.Version); err == nil {
		t.Fatal("last administrator disabled")
	}
}

func TestLoginLimitsOriginsAndCredentialHeaders(t *testing.T) {
	api, _ := testAPI(t)
	for i := 0; i < 5; i++ {
		if _, _, _, err := api.store.Login("admin", "wrong", "attacker"); err != errCredentials {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, _, _, err := api.store.Login("admin", "wrong", "another-ip"); err != errLoginLimited {
		t.Fatalf("account rate limit missing: %v", err)
	}
	request := httptest.NewRequest("POST", "http://example.com/api/v2/auth/login", strings.NewReader(`{"username":"admin","password":"test administrator password"}`))
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("cross origin login accepted")
	}
	if _, err := api.store.db.Exec("DELETE FROM login_limits"); err != nil {
		t.Fatal(err)
	}
	_, _, token := loginTestAccount(t, api.store, "admin")
	request = httptest.NewRequest("GET", "/api/v2/auth/me", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	request.Header.Set("X-API-Key", "")
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("explicit empty key fell back to cookie")
	}
	for _, body := range []string{`{"name":"bad","role":"admin"}`, `{"name":"bad","scopes":["settings:manage"]}`} {
		w := sessionRequest(api, token, "POST", "/api/v2/auth/keys", body)
		if w.Code != 400 {
			t.Fatalf("admin key accepted: %d %s", w.Code, w.Body)
		}
	}
	config, err := NewAuthConfig("0.0.0.0:8080", "https://music.example")
	if err != nil {
		t.Fatal(err)
	}
	api.SetAuthConfig(config)
	request = httptest.NewRequest("POST", "http://internal/api/v2/auth/login", strings.NewReader(`{"username":"admin","password":"test administrator password"}`))
	request.Header.Set("Origin", "https://music.example")
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 200 || !response.Result().Cookies()[0].Secure {
		t.Fatal("HTTPS deployment cookie missing Secure")
	}
}

func TestDatabaseProcessLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.sqlite")
	unlock, err := LockDatabaseProcess(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := LockDatabaseProcess(path); err == nil {
		second()
		unlock()
		t.Fatal("second process lock accepted")
	}
	unlock()
	second, err := LockDatabaseProcess(path)
	if err != nil {
		t.Fatal(err)
	}
	second()
}

func TestAccountBootstrapConcurrent(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "bootstrap.sqlite"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for _, username := range []string{"admin-one", "admin-two"} {
		workers.Add(1)
		go func(name string) {
			defer workers.Done()
			_, err := store.InitializeAdmin(name, "test administrator password")
			results <- err
		}(username)
	}
	workers.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("bootstrap successes=%d", success)
	}
}

func TestAccountKeyRightsChangeResetSync(t *testing.T) {
	api, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	second, err := api.store.createUser("second-admin", "test administrator password", "admin", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = second
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "library", AllowedPlaylistIDs: []string{"library"}}, MutationOptions{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/v2/sync", nil)
	request.Header.Set("X-API-Key", key.Secret)
	identity, ok := api.authenticate(request)
	if !ok {
		t.Fatal("key failed")
	}
	first, err := api.store.SyncPage(identity, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.db.Exec("UPDATE users SET role='member',auth_version=auth_version+1 WHERE id=?", user.ID); err != nil {
		t.Fatal(err)
	}
	identity, ok = api.authenticate(request)
	if !ok {
		t.Fatal("downgraded user's key should still authenticate")
	}
	next, err := api.store.SyncPage(identity, first.Cursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !next.Reset || len(next.Changed) == 0 || len(next.Playlists) == 0 {
		t.Fatal("role change should reset while retaining explicitly granted global library access")
	}
	if err := api.store.RevokeAPIKey(key.ID); err != nil {
		t.Fatalf("could not revoke key after owner lost playlist rights: %v", err)
	}
	if _, err := api.store.runScan(context.Background(), false, nil); err != nil {
		t.Fatal(fmt.Errorf("scan after accounts: %w", err))
	}
}

func TestAccountLoginCSRFAndKeyCannotEscalate(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "accounts.sqlite"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.InitializeAdmin("admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store)
	request := httptest.NewRequest("POST", "http://example.com/api/v2/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse battery staple"}`))
	request.Header.Set("Origin", "http://example.com")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("login: %d %s", response.Code, response.Body)
	}
	var login struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	call := func(csrf, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://example.com/api/v2/auth/keys", strings.NewReader(`{"name":"player"}`))
		r.AddCookie(cookies[0])
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("X-CSRF-Token", csrf)
		if key != "" {
			r.Header.Set("X-API-Key", key)
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	if w := call("", ""); w.Code != 403 {
		t.Fatalf("missing CSRF accepted: %d", w.Code)
	}
	w := call(login.CSRFToken, "")
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var key APIKeyView
	if err := json.Unmarshal(w.Body.Bytes(), &key); err != nil {
		t.Fatal(err)
	}
	if w := call(login.CSRFToken, key.Secret); w.Code != 403 {
		t.Fatalf("key inherited admin cookie: %d", w.Code)
	}
	if w := call(login.CSRFToken, "invalid-key"); w.Code != 401 {
		t.Fatalf("invalid key fell back to cookie: %d", w.Code)
	}
}
