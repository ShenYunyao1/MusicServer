package music

import (
	"database/sql"
	"net/http/httptest"
	"testing"
)

func TestAuditPlaylistDeleteMissingRowHasTyped404(t *testing.T) {
	w := httptest.NewRecorder()
	writePlaylistMutationError(w, sql.ErrNoRows)
	if w.Code != 404 || w.Header().Get("X-Error-Code") != "NOT_FOUND" {
		t.Fatal(w.Code, w.Body)
	}
}

func TestAuditMailSettingFilesAreOutsideWriteLock(t *testing.T) {
	a, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, a.store, "admin")
	s := a.store
	check := func() {
		if !s.writeMu.TryLock() {
			t.Error("mail setting file operation held writeMu")
		} else {
			s.writeMu.Unlock()
		}
	}
	s.mail.secretReadHook = check
	s.mail.secretWriteHook = check
	defer func() { s.mail.secretReadHook = nil; s.mail.secretWriteHook = nil }()
	settings, err := s.UpdateMailSettings(MailSettingsInput{Host: "smtp.example.com", Port: 465, TLSMode: "tls", FromAddress: "sender@example.com", Username: "sender", Password: "private-test-password"}, MutationOptions{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateMailSettings(MailSettingsInput{Host: settings.Host, Port: settings.Port, TLSMode: settings.TLSMode, FromAddress: settings.FromAddress, Username: settings.Username, Version: settings.Version}, MutationOptions{UserID: user.ID}); err != nil {
		t.Fatal(err)
	}
}

func TestAuditRemaining405IncludesSupportedMethods(t *testing.T) {
	a, _ := testAPI(t)
	_, _, token := loginTestAccount(t, a.store, "admin")
	for _, item := range []struct{ path, method, allow string }{
		{"/api/v2/auth/avatar", "POST", "GET, PUT"},
		{"/api/v2/auth/avatar/content", "POST", "GET"},
		{"/api/v2/auth/profile", "GET", "PUT"},
		{"/api/v2/public/playlists", "PUT", "GET"},
		{"/api/v2/subscriptions", "PUT", "GET"},
		{"/api/v2/subscriptions/example", "POST", "PUT, DELETE"},
		{"/api/v2/community/sync", "GET", "POST"},
		{"/api/v2/browse/playlists/move", "GET", "POST"},
		{"/api/v2/admin/users", "PUT", "GET, POST"},
		{"/api/v2/auth/email/challenge", "GET", "POST"},
		{"/api/v2/listening/sessions", "GET", "POST"},
		{"/api/v2/sync/tracks", "GET", "POST"},
	} {
		w := sessionRequest(a, token, item.method, item.path, "")
		if w.Code != 405 || w.Header().Get("Allow") != item.allow {
			t.Errorf("%s: %d Allow=%q %s", item.path, w.Code, w.Header().Get("Allow"), w.Body)
		}
	}
	w := httptest.NewRecorder()
	methodNotAllowed(w, httptest.NewRequest("GET", "/api/v2/listening/sessions", nil))
	if w.Header().Get("Allow") != "POST" {
		t.Fatal("dispatch overwrote listening contract", w.Header())
	}
}
