package music

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mailRequest(api *API, token, method, path, body string, version int64) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.com"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("X-CSRF-Token", csrfForToken(token))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	if version > 0 {
		r.Header.Set("If-Match", fmt.Sprint(version))
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}
func TestMailHTTPPrivacyCSRFAndRevokedBody(t *testing.T) {
	api, _, _ := mailTestSetup(t)
	user, session, token := loginTestAccount(t, api.store, "admin")
	settings := mailRequest(api, token, "GET", "/api/v2/admin/mail", "", 0)
	if settings.Code != 200 || strings.Contains(settings.Body.String(), "private-smtp-password") || strings.Contains(settings.Body.String(), "secret_id") {
		t.Fatal(settings.Code, settings.Body)
	}
	challenge := mailRequest(api, token, "POST", "/api/v2/auth/email/challenge", `{"email":"http@example.com"}`, 0)
	if challenge.Code != 202 || strings.Contains(challenge.Body.String(), `"code"`) || strings.Contains(challenge.Body.String(), "digest") {
		t.Fatal(challenge.Code, challenge.Body)
	}
	r := httptest.NewRequest("POST", "http://example.com/api/v2/auth/email/verify", strings.NewReader(`{}`))
	r.Header.Set("Origin", "http://example.com")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF bypass", w.Code, w.Body)
	}
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "mail privacy", AllowedPlaylistIDs: []string{"library"}}, MutationOptions{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "http://example.com/api/v2/auth/email", nil)
	r.Header.Set("X-API-Key", key.Secret)
	w = httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("player email access", w.Code, w.Body)
	}
	body := &resumeReadHook{Reader: strings.NewReader(`{"host":"smtp.example.com","port":465,"tls_mode":"tls","from_address":"sender@example.com","username":"sender","version":1}`), after: func() {
		if err := api.store.RevokeSession(user.ID, session.ID); err != nil {
			t.Fatal(err)
		}
	}}
	r = httptest.NewRequest("PUT", "http://example.com/api/v2/admin/mail", body)
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("X-CSRF-Token", csrfForToken(token))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	w = httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code < 400 {
		t.Fatal("revoked request changed SMTP settings", w.Code, w.Body)
	}
	current, _ := api.store.MailSettings()
	if current.Version != 1 {
		t.Fatal(current)
	}
}
func TestEmailReleaseDeletedOnlyIdempotentAndProtectsNewOwner(t *testing.T) {
	api, auth, _ := mailTestSetup(t)
	s := api.store
	_, _, token := loginTestAccount(t, s, "admin")
	target, _ := readyMember(t, api, "release-email-old")
	other, otherToken := readyMember(t, api, "release-email-new")
	targetEmail := "released@example.com"
	if err := s.transaction(func(tx *sql.Tx) error {
		state, err := readAccountEmail(tx, target.ID)
		if err != nil {
			return err
		}
		state.Email = targetEmail
		state.VerifiedAt = persistedTime(time.Now())
		if err = writeAccountEmail(tx, target.ID, state); err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?)", "email_binding/"+hashSecret(targetEmail), target.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	active := mailRequest(api, token, "POST", "/api/v2/admin/users/"+target.ID+"/release-email", `{}`, target.Version)
	if active.Code < 400 {
		t.Fatal("active account released", active.Code)
	}
	if err := s.transaction(func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE users SET enabled=0,version=version+1 WHERE id=?", target.ID); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO meta(key,value) VALUES(?,?)", "deleted_user/"+target.ID, persistedTime(time.Now()))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	deleted, err := readUser(s.db, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreign := mailRequest(api, otherToken, "POST", "/api/v2/admin/users/"+target.ID+"/release-email", `{}`, deleted.Version)
	if foreign.Code != 403 {
		t.Fatal("member released email", foreign.Code, foreign.Body)
	}
	released := mailRequest(api, token, "POST", "/api/v2/admin/users/"+target.ID+"/release-email", `{}`, deleted.Version)
	if released.Code != 204 {
		t.Fatal(released.Code, released.Body)
	}
	original, err := readUser(s.db, target.ID)
	if err != nil || original.ID != target.ID || original.DeletedAt == "" || original.Email != "" {
		t.Fatal(original, err)
	}
	if _, err = s.db.Exec("INSERT INTO meta(key,value) VALUES(?,?)", "email_binding/"+hashSecret(targetEmail), other.ID); err != nil {
		t.Fatal(err)
	}
	repeated := mailRequest(api, token, "POST", "/api/v2/admin/users/"+target.ID+"/release-email", `{}`, deleted.Version)
	if repeated.Code != 204 {
		t.Fatal("release is not idempotent", repeated.Code, repeated.Body)
	}
	var owner string
	if err = s.db.QueryRow("SELECT value FROM meta WHERE key=?", "email_binding/"+hashSecret(targetEmail)).Scan(&owner); err != nil || owner != other.ID {
		t.Fatal("new binding deleted", owner, err)
	}
	var entries int
	_ = s.db.QueryRow("SELECT count(*) FROM audit WHERE action='email.released' AND entity_id=?", target.ID).Scan(&entries)
	if entries != 1 {
		t.Fatal("duplicate release audit", entries)
	}
	response, err := s.EmailStatus(auth)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(response)
	if strings.Contains(string(raw), targetEmail) {
		t.Fatal("foreign email leaked")
	}
}
