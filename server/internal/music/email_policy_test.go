package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEmailAdminCreationAtomicPolicy(t *testing.T) {
	api, _ := testAPI(t)
	w := callAPI(api, "POST", "/api/v2/admin/users", `{"username":"EmailExplicit","quota_bytes":1048576,"email_verification_required":false}`)
	if w.Code != 201 {
		t.Fatalf("explicit quota and verification requirement rejected: %d %s", w.Code, w.Body)
	}
	var result struct {
		User struct {
			ID            string `json:"id"`
			Username      string `json:"username"`
			Required      bool   `json:"email_verification_required"`
			MustChange    bool   `json:"must_change_password"`
			UploadAllowed bool   `json:"upload_allowed"`
		} `json:"user"`
		Password string `json:"temporary_password"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.User.Username != "EmailExplicit" || result.User.Required || !result.User.MustChange || result.User.UploadAllowed || result.Password == "" {
		t.Fatalf("wrong creation policy: %s", w.Body)
	}
	var raw string
	if err := api.store.db.QueryRow("SELECT value FROM settings WHERE key=?", "storage_quota/"+result.User.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var quota StorageQuota
	if err := json.Unmarshal([]byte(raw), &quota); err != nil || quota.LimitBytes != 1048576 || quota.Version != 1 {
		t.Fatalf("quota missing: %s %v", raw, err)
	}
	w = callAPI(api, "POST", "/api/v2/admin/users", `{"username":"EmailInvalid","quota_bytes":-1,"email_verification_required":false}`)
	if w.Code < 400 {
		t.Fatalf("invalid quota accepted: %s", w.Body)
	}
	var exists bool
	if err := api.store.db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE username='EmailInvalid')").Scan(&exists); err != nil || exists {
		t.Fatalf("failed atomic creation left an account: %v %v", exists, err)
	}
}

func TestEmailAdminCreationRequiresExplicitQuota(t *testing.T) {
	for _, body := range []string{
		`{"username":"MissingQuota","email_verification_required":false}`,
		`{"username":"MissingQuota","quota_bytes":null,"email_verification_required":false}`,
	} {
		api, _ := testAPI(t)
		w := callAPI(api, "POST", "/api/v2/admin/users", body)
		if w.Code != 400 {
			t.Fatalf("missing explicit quota accepted: %d %s", w.Code, w.Body)
		}
		var exists bool
		if err := api.store.db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE username='MissingQuota')").Scan(&exists); err != nil || exists {
			t.Fatalf("rejected quota left account: %v %v", exists, err)
		}
	}
	api, _ := testAPI(t)
	w := callAPI(api, "POST", "/api/v2/admin/users", `{"username":"ExplicitUnlimited","quota_bytes":0,"email_verification_required":false}`)
	if w.Code != 201 {
		t.Fatalf("explicit unlimited quota rejected: %d %s", w.Code, w.Body)
	}
}

func TestEmailPolicyLegacyInitializationAndRestart(t *testing.T) {
	api, root := testAPI(t)
	user, _, _ := loginTestAccount(t, api.store, "admin")
	if _, err := api.store.db.Exec("DELETE FROM meta WHERE key IN ('email_policy_initialized',?)", accountEmailPrefix+user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.SetStorageQuota(user.ID, 12345, 0, MutationOptions{UserID: user.ID}); err != nil {
		t.Fatal(err)
	}
	if err := api.store.InitializeEmailPolicy(); err != nil {
		t.Fatal(err)
	}
	legacy, err := readUser(api.store.db, user.ID)
	if err != nil || !legacy.EmailVerificationRequired || legacy.UploadAllowed {
		t.Fatalf("old admin escaped requirement: %+v %v", legacy, err)
	}
	if err := checkUploadEmailPolicy(api.store.db, user.ID); !errors.Is(err, errEmailVerificationRequired) {
		t.Fatal(err)
	}
	var quota StorageQuota
	if err := readStorageJSON(api.store.db, "storage_quota/"+user.ID, &quota); err != nil || quota.LimitBytes != 12345 {
		t.Fatalf("legacy quota reset: %+v %v", quota, err)
	}
	exempt := false
	changed, err := api.store.UpdateUser(user.ID, AccountPatch{EmailVerificationRequired: &exempt}, MutationOptions{UserID: user.ID, ExpectedVersion: legacy.Version})
	if err != nil || !changed.UploadAllowed {
		t.Fatalf("explicit exemption rejected: %+v %v", changed, err)
	}
	if err := api.store.InitializeEmailPolicy(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(api.store.DataPath(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := readUser(reopened.db, user.ID)
	if err != nil || got.EmailVerificationRequired || !got.UploadAllowed {
		t.Fatalf("restart overwrote exemption: %+v %v", got, err)
	}
	var schema int
	if err := reopened.db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil || schema != sqliteSchemaVersion {
		t.Fatalf("implicit DDL migration: %d %v", schema, err)
	}
}

func TestEmailPolicyMemberCannotExemptSelfAndLiveStateWins(t *testing.T) {
	api, _ := testAPI(t)
	user, _ := readyMember(t, api, "policy-member")
	exempt := false
	if _, err := api.store.UpdateUser(user.ID, AccountPatch{EmailVerificationRequired: &exempt}, MutationOptions{UserID: user.ID, ExpectedVersion: user.Version}); err == nil {
		t.Fatal("member exempted itself")
	}
	if err := api.store.transaction(func(tx *sql.Tx) error {
		return writeAccountEmail(tx, user.ID, AccountEmailState{Required: true, Email: "User@example.com", VerifiedAt: persistedTime(time.Now()), QuotaSource: "administrator"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := checkUploadEmailPolicy(api.store.db, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.db.Exec("UPDATE users SET enabled=0 WHERE id=?", user.ID); err != nil {
		t.Fatal(err)
	}
	if err := checkUploadEmailPolicy(api.store.db, user.ID); !errors.Is(err, errResourceHidden) {
		t.Fatalf("verified disabled member allowed: %v", err)
	}
}

func TestEmailAdminCreationRechecksSessionAfterBody(t *testing.T) {
	api, _ := testAPI(t)
	user, session, token := loginTestAccount(t, api.store, "admin")
	body := &resumeReadHook{Reader: strings.NewReader(`{"username":"RevokedCreation","quota_bytes":123,"email_verification_required":false}`), after: func() {
		if err := api.store.RevokeSession(user.ID, session.ID); err != nil {
			t.Fatal(err)
		}
	}}
	r := httptest.NewRequest("POST", "http://example.com/api/v2/admin/users", body)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("X-CSRF-Token", csrfForToken(token))
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code < 400 {
		t.Fatalf("revoked session created member: %d %s", w.Code, w.Body)
	}
	var exists bool
	if err := api.store.db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE username='RevokedCreation')").Scan(&exists); err != nil || exists {
		t.Fatalf("revoked session persisted account: %v %v", exists, err)
	}
}
