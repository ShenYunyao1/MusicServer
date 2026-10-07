package music

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type blockingWriter struct {
	header  http.Header
	started chan struct{}
	release chan struct{}
	status  int
}

func (w *blockingWriter) Header() http.Header    { return w.header }
func (w *blockingWriter) WriteHeader(status int) { w.status = status }
func (w *blockingWriter) Write(value []byte) (int, error) {
	select {
	case <-w.started:
	default:
		close(w.started)
	}
	<-w.release
	return len(value), nil
}

func TestScopeDeniedIsStructuredAndRecorded(t *testing.T) {
	api, _ := testAPI(t)
	view, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "media-only", Role: "player", Scopes: []string{ScopeMediaRead}})
	if view.ID == "" {
		t.Fatal("key ID is empty")
	}
	response := callAPIWithKey(api, http.MethodGet, "/api/v1/playlists", "", secret)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "SCOPE_DENIED") {
		t.Fatalf("scope response = %d %s", response.Code, response.Body.String())
	}
	stats := api.readCallStats()
	var found bool
	for _, value := range stats.Keys {
		if value.KeyID == view.ID {
			found = true
			if value.TotalRequests < 1 || value.FailureRequests < 1 {
				t.Fatalf("scope request stats = %+v", value)
			}
		}
	}
	if !found {
		t.Fatalf("stats did not contain key %q: %+v", view.ID, stats)
	}
}

func TestKeyManagementTrailingSlashRequiresScope(t *testing.T) {
	api, _ := testAPI(t)
	created := callAPI(api, http.MethodPost, "/api/v1/auth/keys", `{"name":"admin-without-scopes","role":"admin","scopes":[]}`)
	var key APIKeyView
	if err := json.Unmarshal(created.Body.Bytes(), &key); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("could not create restricted admin: %d %s", created.Code, created.Body.String())
	}
	secret := key.Secret
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := callAPIWithKey(api, method, "/api/v1/auth/keys/", `{"name":"forbidden","role":"admin"}`, secret)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "SCOPE_DENIED") {
			t.Fatalf("trailing slash %s bypassed key management scope: %d %s", method, response.Code, response.Body.String())
		}
	}
}

func TestKeyConcurrencyLimit(t *testing.T) {
	api, _ := testAPI(t)
	view, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "one-at-a-time", Role: "player", Scopes: []string{ScopeLibraryRead}, MaxConcurrency: 1})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists", nil)
	req.Header.Set("X-API-Key", secret)
	auth, ok := api.authenticate(req)
	if !ok || auth.keyID != view.ID {
		t.Fatalf("authenticate = %+v, %v", auth, ok)
	}
	if !api.acquire(auth) {
		t.Fatal("first request was rejected")
	}
	if api.acquire(auth) {
		t.Fatal("second concurrent request was accepted")
	}
	if api.activeRequests(view.ID) != 1 {
		t.Fatalf("active requests = %d", api.activeRequests(view.ID))
	}
	api.release(auth)
	if !api.acquire(auth) {
		t.Fatal("request was not admitted after release")
	}
	api.release(auth)
}

func TestConcurrencyLeaseCoversStatisticsWrite(t *testing.T) {
	api, _ := testAPI(t)
	view, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "statistics-lease", Role: "player", MaxConcurrency: 1})
	tx, err := api.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	defer func() {
		_ = tx.Rollback()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("request did not finish after statistics connection was released")
		}
	}()
	waits := api.store.db.Stats().WaitCount
	request := httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil)
	request.Header.Set("X-API-Key", secret)
	go func() {
		api.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for api.store.db.Stats().WaitCount == waits {
		if time.Now().After(deadline) {
			t.Fatal("statistics write did not wait for database connection")
		}
		time.Sleep(time.Millisecond)
	}
	if active := api.activeRequests(view.ID); active != 1 {
		t.Fatalf("request released concurrency lease before statistics finished: active=%d", active)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	<-done
	if active := api.activeRequests(view.ID); active != 0 {
		t.Fatalf("completed request retained concurrency lease: active=%d", active)
	}
}

func TestHTTPConcurrencyLimitWhileMediaResponseBlocked(t *testing.T) {
	api, _ := testAPI(t)
	track := api.store.Tracks()[0]
	_, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "media", Role: "player", Scopes: []string{ScopeMediaRead}, AllowedPlaylistIDs: []string{"library"}, MaxConcurrency: 1})
	first := &blockingWriter{header: make(http.Header), started: make(chan struct{}), release: make(chan struct{})}
	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/media/%s", track.ID), nil)
	request.Header.Set("X-API-Key", secret)
	done := make(chan struct{})
	go func() { api.ServeHTTP(first, request); close(done) }()
	select {
	case <-first.started:
	case <-time.After(time.Second):
		t.Fatal("first media request did not reach writer")
	}
	second := callAPIWithKey(api, http.MethodGet, "/api/v1/media/"+track.ID, "", secret)
	if second.Code != http.StatusTooManyRequests || !strings.Contains(second.Body.String(), "KEY_CONCURRENCY_LIMIT") {
		t.Fatalf("second concurrent response = %d %s", second.Code, second.Body.String())
	}
	close(first.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first media request did not finish")
	}
}

func TestCallStatsSurviveStoreReopen(t *testing.T) {
	api, _ := testAPI(t)
	_, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "reader", Role: "player", Scopes: []string{ScopeLibraryRead}})
	if response := callAPIWithKey(api, http.MethodGet, "/api/v1/playlists", "", secret); response.Code != http.StatusOK {
		t.Fatalf("request status = %d", response.Code)
	}
	if response := callAPIWithKey(api, http.MethodGet, "/api/v1/playlists", "", "bad-secret"); response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid request status = %d", response.Code)
	}
	path := api.store.DataPath()
	root := api.store.mediaRoot
	if err := api.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(path, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopenedAPI := NewAPI(reopened, "secret")
	stats := reopenedAPI.readCallStats()
	if stats.Total.TotalRequests < 2 {
		t.Fatalf("reopened stats = %+v", stats)
	}
	if _, marshalErr := json.Marshal(stats); marshalErr != nil {
		t.Fatal(marshalErr)
	}
}
