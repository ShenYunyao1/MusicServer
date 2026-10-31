package music

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuditHTTPContractsAndUploadBoundary(t *testing.T) {
	a, _ := testAPI(t)
	for _, entry := range []struct{ path, allow string }{{"/api/v2/health", "GET"}, {"/api/v2/playlists", "GET, POST"}, {"/api/v2/admin/settings", "GET, PUT"}} {
		w := callAPI(a, "OPTIONS", entry.path, "")
		if w.Code != 405 || w.Header().Get("Allow") != entry.allow || !strings.Contains(w.Body.String(), "METHOD_NOT_ALLOWED") {
			t.Fatal(entry, w.Code, w.Header(), w.Body)
		}
	}
	if err := a.store.PutPlaylist(Playlist{ID: "audit-conflict", Name: "One"}); err != nil {
		t.Fatal(err)
	}
	if w := callAPI(a, "POST", "/api/v2/playlists", `{"id":"audit-conflict","name":"Two"}`); w.Code != 409 {
		t.Fatal("create conflict", w.Code, w.Body)
	}
	audio := validTestAudio(t)
	settings := a.store.Settings()
	settings.MaxUploadBytes = int64(len(audio))
	if err := a.store.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	form := multipart.NewWriter(&buffer)
	part, _ := form.CreateFormFile("file", "limit.wav")
	part.Write(audio)
	form.Close()
	_, _, token := loginTestAccount(t, a.store, "admin")
	req := httptest.NewRequest("POST", "http://example.com/api/v2/uploads", &buffer)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("X-CSRF-Token", csrfForToken(token))
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, req)
	if w.Code != 201 && w.Code != 200 {
		t.Fatal("exact max audio rejected", w.Code, w.Body)
	}
	preview, err := a.store.StageAudio("manual.wav", bytes.NewReader(audio), nil)
	if err != nil {
		t.Fatal(err)
	}
	manual, err := a.store.updateStageCover(preview.ID, tinyPNG(), "manual")
	if err != nil {
		t.Fatal(err)
	}
	external, err := a.store.updateStageCover(preview.ID, tinyPNG(), "external")
	if err != nil || external.Track.CoverSource != "manual" || external.Track.CoverID != manual.Track.CoverID {
		t.Fatal("external replaced manual", external, err)
	}
}

func TestAuditDisabledScannerOwnerIsReassigned(t *testing.T) {
	a, _ := testAPI(t)
	old, _, _ := loginTestAccount(t, a.store, "admin")
	next, err := a.store.createUser("next-scanner", "another administrator password", "admin", false)
	if err != nil {
		t.Fatal(err)
	}
	a.store.db.Exec("INSERT INTO meta(key,value) VALUES('scan_owner_id',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", old.ID)
	if err := a.store.SetUserEnabled(old.ID, false, old.Version); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := a.store.db.QueryRow("SELECT value FROM meta WHERE key='scan_owner_id'").Scan(&owner); err != nil || owner != next.ID {
		t.Fatal("scan ownership not reassigned", owner, err)
	}
}

func TestAuditProxyLoginBuckets(t *testing.T) {
	a, _ := testAPI(t)
	a.authConfig = AuthConfig{Origin: "https://music.example", Secure: true}
	request := func(ip, user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://music.example/api/v2/auth/login", strings.NewReader(`{"username":"`+user+`","password":"wrong password"}`))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "https://music.example")
		r.Header.Set("X-Forwarded-For", ip)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < 30; i++ {
		request("203.0.113.5", "invalid"+strings.Repeat("a", i))
	}
	if w := request("203.0.113.6", "unrelated"); w.Code != 401 {
		t.Fatalf("other proxy client blocked: %d %s", w.Code, w.Body)
	}
}

func TestAuditUnrelatedHeadersKeepCookieAndInvalidKeyFailsClosed(t *testing.T) {
	a, _ := testAPI(t)
	_, _, token := loginTestAccount(t, a.store, "admin")
	for _, headers := range []map[string]string{{"X-API-Key": ""}, {"Authorization": "Basic cHJveHk6dXNlcg=="}, {"X-API-Key": "bad"}, {"Authorization": "Bearer bad"}} {
		r := httptest.NewRequest("GET", "http://example.com/api/v2/auth/me", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		expected := 200
		if headers["X-API-Key"] == "bad" || headers["Authorization"] == "Bearer bad" {
			expected = 401
		}
		if w.Code != expected {
			t.Fatalf("header handling: %v status %d want%d", headers, w.Code, expected)
		}
	}
}

func TestAuditRevokedAdminMutations(t *testing.T) {
	a, _ := testAPI(t)
	admin, session, token := loginTestAccount(t, a.store, "admin")
	r := httptest.NewRequest("GET", "http://example.com/api/v2/auth/me", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	auth, ok := a.authenticate(r)
	if !ok {
		t.Fatal("auth")
	}
	options := MutationOptions{UserID: admin.ID, ActorType: "session", CredentialID: auth.keyID}
	if _, err := a.store.db.Exec("UPDATE sessions SET revoked_at='2026-10-31T00:00:00Z' WHERE id=?", session.ID); err != nil {
		t.Fatal(err)
	}
	before := a.store.Settings()
	changed := before
	changed.MaxUploadBytes++
	if err := a.store.UpdateSettings(changed, options); err == nil {
		t.Fatal("revoked settings accepted")
	}
	if err := a.store.transaction(func(tx *sql.Tx) error { return requireAdminActor(tx, []MutationOptions{options}) }); err == nil {
		t.Fatal("revoked admin accepted")
	}
	for _, handler := range []func(http.ResponseWriter, *http.Request){a.sync, a.playlists} {
		request := httptest.NewRequest("GET", "http://example.com/api/v2/sync", nil)
		request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, auth))
		w := httptest.NewRecorder()
		handler(w, request)
		if w.Code != 401 {
			t.Fatal("late auth should be401", w.Code, w.Body)
		}
	}
}

func TestAuditReadErrorsNotEmptySuccess(t *testing.T) {
	a, _ := testAPI(t)
	a.store.db.Close()
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://example.com/api/v2/health", nil))
	if w.Code != 503 {
		t.Fatalf("database failure reported healthy: %d %s", w.Code, w.Body)
	}
}
