package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"pikmelo-server/internal/version"
)

func TestContainerHealthCheck(t *testing.T) {
	ready := fmt.Sprintf(`{"status":"ok","version":%q,"schema":%d}`, version.Version, version.Schema)
	for _, tc := range []struct {
		name  string
		code  int
		body  string
		valid bool
	}{
		{"ready", 200, ready, true},
		{"unavailable", 503, ready, false},
		{"invalid JSON", 200, "not JSON", false},
		{"invalid status", 200, `{"status":"maintenance"}`, false},
		{"old version", 200, `{"status":"ok","version":"0.0.1","schema":9}`, false},
		{"old schema", 200, fmt.Sprintf(`{"status":"ok","version":%q,"schema":8}`, version.Version), false},
		{"redirect", 302, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/health" {
					t.Error("unexpected health endpoint")
				}
				w.Header().Set("Location", "/other")
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			if err := checkHealth(server.URL + "/api/v2/health"); (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
}
