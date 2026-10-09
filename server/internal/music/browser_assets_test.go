package music

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserTagReaderPublicAsset(t *testing.T) {
	api, _ := testAPI(t)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/jsmediatags-3.9.7.min.js", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("public asset status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if !bytes.Equal(response.Body.Bytes(), browserTagReader) || !strings.Contains(response.Body.String(), "jsmediatags") {
		t.Fatal("asset response did not include the bundled tag reader")
	}
}

func TestBrowserPageReferencesBundledTagReader(t *testing.T) {
	api, _ := testAPI(t)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("management page status = %d", response.Code)
	}
	const reference = `<script src="/assets/jsmediatags-3.9.7.min.js"></script>`
	if strings.Count(response.Body.String(), reference) != 1 {
		t.Fatal("management page must reference the bundled tag reader exactly once")
	}
	for _, element := range []string{`id="checkUpdate"`, `id="updateStatus"`, `update_repository`, `admin/update/check`} {
		if !strings.Contains(response.Body.String(), element) {
			t.Fatalf("management page is missing update control %q", element)
		}
	}
}

func TestBrowserAssetRouteDoesNotExposeOtherPathsOrMethods(t *testing.T) {
	api, _ := testAPI(t)
	for _, target := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/assets/missing.js"},
		{http.MethodGet, "/assets/"},
		{http.MethodGet, "/assets/../internal/music/vendor/README.md"},
		{http.MethodPost, "/assets/jsmediatags-3.9.7.min.js"},
	} {
		t.Run(target.method+" "+target.path, func(t *testing.T) {
			for _, key := range []string{"", "secret"} {
				request := httptest.NewRequest(target.method, target.path, nil)
				if key != "" {
					request.Header.Set("X-API-Key", key)
				}
				response := httptest.NewRecorder()
				api.ServeHTTP(response, request)
				if response.Code < http.StatusBadRequest {
					t.Fatalf("unexpected status = %d with key %q", response.Code, key)
				}
				if strings.Contains(response.Header().Get("Content-Type"), "javascript") || strings.Contains(response.Body.String(), "jsmediatags=d()") {
					t.Fatal("unsupported path or method exposed the bundled script")
				}
			}
		})
	}
}
