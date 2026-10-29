package music

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"musicserver/internal/update"
)

func TestUpdateRepositoryPersistsAndRejectsInvalidURLs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "library.sqlite")
	store, err := NewStore(path, root)
	if err != nil {
		t.Fatal(err)
	}
	settings := store.Settings()
	settings.UpdateRepository = "https://github.com/example/musicserver"
	if err := store.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewStore(path, root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := store.Settings().UpdateRepository; got != settings.UpdateRepository {
		t.Fatalf("update repository after restart = %q", got)
	}
	settings.UpdateRepository = "http://example.invalid/evil/repo"
	if err := store.UpdateSettings(settings); err == nil {
		t.Fatal("accepted non-GitHub update source")
	}
}

func TestUpdateStatusRequiresAdministrator(t *testing.T) {
	api, _ := testAPI(t)
	api.SetUpdater(update.NewManager(filepath.Join(t.TempDir(), "updates"), func() string { return "" }))
	t.Cleanup(api.updater.Close)
	_, playerSecret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "player"})
	for _, tc := range []struct {
		method, path, key string
		want              int
	}{
		{http.MethodGet, "/api/v2/admin/update", "", http.StatusUnauthorized},
		{http.MethodPost, "/api/v2/admin/update/check", "", http.StatusUnauthorized},
		{http.MethodPost, "/api/v2/admin/update/apply", "", http.StatusUnauthorized},
		{http.MethodGet, "/api/v2/admin/update", playerSecret, http.StatusForbidden},
		{http.MethodPost, "/api/v2/admin/update/check", playerSecret, http.StatusForbidden},
		{http.MethodPost, "/api/v2/admin/update/apply", playerSecret, http.StatusForbidden},
		{http.MethodGet, "/api/v2/admin/update", "secret", http.StatusOK},
		{http.MethodPost, "/api/v2/admin/update/check", "secret", http.StatusAccepted},
	} {
		request := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.key == "secret" {
			authorizeTestAdmin(api, request)
		} else if tc.key != "" {
			request.Header.Set("X-API-Key", tc.key)
		}
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != tc.want {
			t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, response.Code, tc.want)
		}
	}
	if !strings.Contains(api.updater.Status().CurrentVersion, ".") {
		t.Fatal("missing current version")
	}
}

func TestUpdateMaintenanceBlocksWritesButKeepsHealth(t *testing.T) {
	api, _ := testAPI(t)
	api.SetMaintenance(func() bool { return true })
	for _, path := range []string{"/api/v2/auth/login", "/api/v2/playlists", "/api/v2/admin/update/apply"} {
		r := httptest.NewRecorder()
		api.ServeHTTP(r, httptest.NewRequest("POST", path, nil))
		if r.Code != 503 || r.Header().Get("Retry-After") == "" {
			t.Fatalf("maintenance allowed %s: %d", path, r.Code)
		}
	}
	r := httptest.NewRecorder()
	api.ServeHTTP(r, httptest.NewRequest("GET", "/api/v2/health", nil))
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"schema":8`) {
		t.Fatalf("health not available: %d %s", r.Code, r.Body)
	}
}

func TestApplyUpdateNeedsAnExplicitVersion(t *testing.T) {
	api, _ := testAPI(t)
	api.SetUpdater(update.NewManager(t.TempDir(), func() string { return update.Repository }))
	t.Cleanup(api.updater.Close)
	request := httptest.NewRequest("POST", "/api/v2/admin/update/apply", strings.NewReader(`{}`))
	authorizeTestAdmin(api, request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatalf("unselected release accepted: %d %s", response.Code, response.Body)
	}
}
