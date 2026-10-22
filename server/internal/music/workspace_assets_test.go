package music

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkspaceAssetsAvailableBeforeLogin(t *testing.T) {
	api, _ := testAPI(t)
	for _, name := range []string{"management.js", "management.css", "library.js", "administration.js", "navigation.js", "feedback.js"} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/"+name, nil))
			contentType := "text/javascript"
			if strings.HasSuffix(name, ".css") {
				contentType = "text/css"
			}
			if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), contentType) || response.Body.Len() == 0 {
				t.Fatalf("asset unavailable before login: status=%d content-type=%q", response.Code, response.Header().Get("Content-Type"))
			}
			if response.Header().Get("Cache-Control") != "no-cache" {
				t.Fatal("mutable workspace assets must be revalidated after an upgrade")
			}
		})
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/sqlite.go", nil))
	if response.Code == http.StatusOK {
		t.Fatal("asset handling exposed a file outside the explicit asset list")
	}
}
