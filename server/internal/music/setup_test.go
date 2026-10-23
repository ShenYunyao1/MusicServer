package music

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWebSetupAuthorizationAndSingleInitialization(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "library.sqlite"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	api := NewAPI(store)
	if err := api.ConfigureWebSetup(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(store.DataPath()), "setup-token")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < 40 {
		t.Fatal("weak setup token")
	}
	restarted := NewAPI(store)
	if err := restarted.ConfigureWebSetup(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(raw) {
		t.Fatal("restart changed pending authorization")
	}
	send := func(method, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://example.com/api/v2/setup", strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	status := send("GET", "", "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"required":true`) || strings.Contains(status.Body.String(), token) {
		t.Fatal("unsafe setup status", status.Code)
	}
	body := func(secret, confirm string) string {
		b, _ := json.Marshal(map[string]string{"token": secret, "username": "admin", "password": "web administrator password", "confirm_password": confirm})
		return string(b)
	}
	for _, c := range []struct {
		secret, confirm, origin string
		code                    int
	}{
		{"wrong", "web administrator password", "http://example.com", 403},
		{token, "web administrator password", "http://attacker.test", 403},
		{token, "web administrator password", "", 403},
		{token, "different", "http://example.com", 400},
	} {
		if w := send("POST", body(c.secret, c.confirm), c.origin); w.Code != c.code {
			t.Fatalf("setup rejected incorrectly: %d %s", w.Code, w.Body)
		}
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- send("POST", body(token, "web administrator password"), "http://example.com").Code
		}()
	}
	wg.Wait()
	close(codes)
	successes := 0
	for code := range codes {
		if code == 201 {
			successes++
		} else if code != 409 {
			t.Fatalf("unexpected competing result %d", code)
		}
	}
	if successes != 1 {
		t.Fatal("bootstrap not exclusive")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("setup credential retained")
	}
	if _, _, _, err := store.Login("admin", "web administrator password", "setup-test"); err != nil {
		t.Fatal(err)
	}
	if w := send("GET", "", ""); !strings.Contains(w.Body.String(), `"required":false`) {
		t.Fatal("setup still required")
	}
	if err := restarted.ConfigureWebSetup(); err != nil {
		t.Fatal(err)
	}
	if restarted.SetupPending() {
		t.Fatal("existing admin reopened setup")
	}
	if _, err := store.db.Exec("UPDATE users SET enabled=0 WHERE role='admin'"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ConfigureWebSetup(); err == nil {
		t.Fatal("disabled administrator must require offline recovery")
	}
	if restarted.SetupPending() {
		t.Fatal("disabled administrator reopened registration")
	}
}

func TestWebSetupRejectsWeakPasswordAndDoesNotLeakSecrets(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "library.sqlite"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	api := NewAPI(store)
	if err := api.ConfigureWebSetup(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(store.DataPath()), "setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(raw))
	for _, password := range []string{"short", "valid twelve characters"} {
		b, _ := json.Marshal(map[string]string{"token": token, "username": "admin", "password": password, "confirm_password": password})
		r := httptest.NewRequest("POST", "http://example.com/api/v2/setup", strings.NewReader(string(b)))
		r.Header.Set("Origin", "http://example.com")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		expected := 400
		if len(password) > 12 {
			expected = 201
		}
		if w.Code != expected {
			t.Fatalf("unexpected setup result %d", w.Code)
		}
		if strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), password) {
			t.Fatal("response exposed a secret")
		}
	}
	var encoded string
	if err := store.db.QueryRow("SELECT password_hash FROM users WHERE username='admin'").Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$") {
		t.Fatal("web password not hashed")
	}
	var audits string
	if err := store.db.QueryRow("SELECT group_concat(actor) FROM audit").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(audits, "web-setup") {
		t.Fatal("web initialization audit missing")
	}
}
