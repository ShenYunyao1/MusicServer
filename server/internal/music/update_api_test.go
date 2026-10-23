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
