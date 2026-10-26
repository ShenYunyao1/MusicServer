package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func registrationReady(t *testing.T, api *API) User {
	t.Helper()
	admin, _, _ := loginTestAccount(t, api.store, "admin")
	settings, err := api.store.UpdateMailSettings(MailSettingsInput{Host: "smtp.example.test", Port: 465, TLSMode: "tls", FromAddress: "sender@example.test", Username: "test-smtp", Password: "isolated-test-secret"}, MutationOptions{UserID: admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := api.store.markMailTested(settings.Version); err != nil {
		t.Fatal(err)
	}
	if !api.store.MailReady() {
		t.Fatal("isolated mail configuration not ready")
	}
	return admin
}

func setRegistration(t *testing.T, store *Store, admin User, enabled bool, quota, version int64) RegistrationPolicy {
	t.Helper()
	policy, err := store.UpdateRegistrationPolicy(RegistrationPolicyInput{Enabled: &enabled, DefaultQuotaBytes: &quota, Version: version}, MutationOptions{UserID: admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func registrationCall(api *API, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.com"+path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.11:1234"
	r.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}

func TestRegistrationClosedByDefaultAndRequiresReadyMail(t *testing.T) {
	api, _ := testAPI(t)
	admin, _, _ := loginTestAccount(t, api.store, "admin")
	policy, err := api.store.RegistrationPolicy()
	if err != nil || policy.Enabled || policy.QuotaConfigured {
		t.Fatal(policy, err)
	}
	w := registrationCall(api, "GET", "/api/v2/registration/config", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("public config: %d %s", w.Code, w.Body)
	}
	if _, err := api.store.RegisterAccount("ClosedAccount", "valid personal password", "192.0.2.10"); !errors.Is(err, errRegistrationDisabled) {
		t.Fatal(err)
	}
	enabled := true
	quota := int64(1024)
	if _, err := api.store.UpdateRegistrationPolicy(RegistrationPolicyInput{Enabled: &enabled, DefaultQuotaBytes: &quota}, MutationOptions{UserID: admin.ID}); !errors.Is(err, errRegistrationMail) {
		t.Fatalf("enabled before SMTP test: %v", err)
	}
	policy, _ = api.store.RegistrationPolicy()
	if policy.Version != 0 || policy.Enabled || policy.QuotaConfigured {
		t.Fatalf("failed configuration changed policy: %+v", policy)
	}
	admin = registrationReady(t, api)
	if _, err := api.store.UpdateRegistrationPolicy(RegistrationPolicyInput{Enabled: &enabled}, MutationOptions{UserID: admin.ID}); err == nil {
		t.Fatal("enabled without an explicit quota")
	}
	setRegistration(t, api.store, admin, true, 0, 0)
	if _, err := api.store.RegisterAccount("UnlimitedExplicit", "valid personal password", "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationSnapshotAndAdministratorQuotaOverride(t *testing.T) {
	api, _ := testAPI(t)
	admin := registrationReady(t, api)
	first := setRegistration(t, api.store, admin, true, 1<<30, 0)
	alice, err := api.store.RegisterAccount("SnapshotAlice", "valid personal password", "192.0.2.12")
	if err != nil {
		t.Fatal(err)
	}
	second := setRegistration(t, api.store, admin, true, 2<<30, first.Version)
	bob, err := api.store.RegisterAccount("SnapshotBob", "valid personal password", "192.0.2.13")
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		user           User
		quota, version int64
	}{{alice, 1 << 30, first.Version}, {bob, 2 << 30, second.Version}} {
		state, err := readAccountEmail(api.store.db, fixture.user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !state.Required || state.QuotaSource != "registration" || state.RegistrationQuotaBytes != fixture.quota || state.RegistrationPolicyVersion != fixture.version || fixture.user.UploadAllowed || fixture.user.MustChangePassword {
			t.Fatalf("wrong snapshot: %+v %+v", fixture.user, state)
		}
		var quota StorageQuota
		if err := readStorageJSON(api.store.db, "storage_quota/"+fixture.user.ID, &quota); err != nil || quota.LimitBytes != fixture.quota || quota.Version != 1 {
			t.Fatal(quota, err)
		}
	}
	if _, err := api.store.SetStorageQuota(alice.ID, 777, 1, MutationOptions{UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	if err := api.store.transaction(func(tx *sql.Tx) error {
		state, err := readAccountEmail(tx, alice.ID)
		if err != nil {
			return err
		}
		state.Email = "snapshot@example.test"
		state.VerifiedAt = persistedTime(time.Now())
		return writeAccountEmail(tx, alice.ID, state)
	}); err != nil {
		t.Fatal(err)
	}
	user, err := readUser(api.store.db, alice.ID)
	if err != nil || !user.UploadAllowed {
		t.Fatal(user, err)
	}
	var quota StorageQuota
	if err := readStorageJSON(api.store.db, "storage_quota/"+alice.ID, &quota); err != nil || quota.LimitBytes != 777 || quota.Version != 2 {
		t.Fatalf("verification overwrote admin quota: %+v %v", quota, err)
	}
	var audit string
	if err := api.store.db.QueryRow("SELECT changes FROM audit WHERE action='account.create' AND entity_id=?", alice.ID).Scan(&audit); err != nil || strings.Contains(audit, "valid personal password") {
		t.Fatal("registration secret entered audit", err)
	}
}

func TestRegistrationHTTPStrictOriginAndNoAutomaticSession(t *testing.T) {
	api, _ := testAPI(t)
	admin := registrationReady(t, api)
	setRegistration(t, api.store, admin, true, 123456, 0)
	for _, body := range []string{`{"username":"InjectedOne","password":"valid personal password","email_verification_required":false}`, `{"username":"InjectedTwo","password":"valid personal password","quota_bytes":0}`, `{"username":"InjectedThree","password":"valid personal password","role":"admin"}`} {
		w := registrationCall(api, "POST", "/api/v2/registration", body)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	r := httptest.NewRequest("POST", "http://example.com/api/v2/registration", strings.NewReader(`{"username":"CrossOrigin","password":"valid personal password"}`))
	r.Header.Set("Origin", "https://other.example")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	w = registrationCall(api, "POST", "/api/v2/registration", `{"username":"HttpRegister","password":"valid personal password"}`)
	if w.Code != 201 || len(w.Result().Cookies()) != 0 || w.Body.String() != "{\"registered\":true}\n" {
		t.Fatalf("registration exposed data/session: %d %s", w.Code, w.Body)
	}
	user, _, _, err := api.store.Login("httpregister", "valid personal password", "login-fixture")
	if err != nil || user.MustChangePassword || !user.EmailVerificationRequired || user.UploadAllowed {
		t.Fatal(user, err)
	}
}

func TestRegistrationLimitsAndPolicyVersion(t *testing.T) {
	api, _ := testAPI(t)
	admin := registrationReady(t, api)
	setRegistration(t, api.store, admin, true, 1024, 0)
	for i := 0; i < 5; i++ {
		if err := api.store.reserveRegistration("192.0.2.20"); err != nil {
			t.Fatal(err)
		}
	}
	if err := api.store.reserveRegistration("192.0.2.20"); !errors.Is(err, errRegistrationLimited) {
		t.Fatalf("IP limit bypass: %v", err)
	}
	if _, err := api.store.UpdateRegistrationPolicy(RegistrationPolicyInput{Version: 0}, MutationOptions{UserID: admin.ID}); !errors.Is(err, errVersionConflict) {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(registrationCounter{Count: 5, WindowStart: persistedTime(time.Now().Add(-2 * time.Hour))})
	if _, err := api.store.db.Exec("UPDATE meta SET value=? WHERE key=?", string(raw), "registration_limit/ip/"+hashSecret("192.0.2.20")); err != nil {
		t.Fatal(err)
	}
	if err := api.store.reserveRegistration("192.0.2.20"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.db.Exec("UPDATE login_limits SET attempts=0"); err != nil {
		t.Fatal(err)
	}
	var rawLimit string
	if err := api.store.db.QueryRow("SELECT value FROM meta WHERE key=?", "registration_limit/ip/"+hashSecret("192.0.2.20")).Scan(&rawLimit); err != nil {
		t.Fatal(err)
	}
	var limit registrationCounter
	if err := json.Unmarshal([]byte(rawLimit), &limit); err != nil || limit.Count != 1 {
		t.Fatal(limit, err)
	}
}

func TestRegistrationConcurrentIdentityAndRevokedPolicyWriter(t *testing.T) {
	api, _ := testAPI(t)
	admin := registrationReady(t, api)
	setRegistration(t, api.store, admin, true, 4096, 0)
	var group sync.WaitGroup
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := api.store.RegisterAccount("ConcurrentRegister", "valid personal password", "192.0.2.30")
			results <- err
		}()
	}
	group.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, errUsernameTaken) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("concurrent creation count=%d", success)
	}
	var id string
	if err := api.store.db.QueryRow("SELECT id FROM users WHERE username='ConcurrentRegister'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	var quota StorageQuota
	if err := readStorageJSON(api.store.db, "storage_quota/"+id, &quota); err != nil || quota.LimitBytes != 4096 {
		t.Fatal(quota, err)
	}
	_, session, token := loginTestAccount(t, api.store, "admin")
	body := &resumeReadHook{Reader: strings.NewReader(`{"enabled":false,"version":1}`), after: func() {
		if err := api.store.RevokeSession(admin.ID, session.ID); err != nil {
			t.Fatal(err)
		}
	}}
	r := httptest.NewRequest("PUT", "http://example.com/api/v2/admin/registration", body)
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("Cookie", sessionCookie+"="+token)
	r.Header.Set("X-CSRF-Token", csrfForToken(token))
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code < 400 {
		t.Fatalf("revoked admin edited registration policy: %d %s", w.Code, w.Body)
	}
	policy, err := api.store.RegistrationPolicy()
	if err != nil || !policy.Enabled || policy.Version != 1 {
		t.Fatal(policy, err)
	}
}
