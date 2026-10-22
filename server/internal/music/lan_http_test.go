package music

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLANHTTPListenerConfiguration(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8080", ":8080", "[::]:8080", "192.168.1.187:8080"} {
		config, err := NewAuthConfig(address, "")
		if err != nil || config.Secure || config.Origin != "" {
			t.Fatalf("LAN HTTP listener %s: %+v, %v", address, config, err)
		}
	}
	if _, err := NewAuthConfig("8.8.8.8:8080", ""); err == nil {
		t.Fatal("public-address HTTP listener accepted")
	}
	config, err := NewAuthConfig("0.0.0.0:8080", "https://music.example")
	if err != nil || !config.Secure || config.Origin != "https://music.example" {
		t.Fatalf("HTTPS deployment changed: %+v, %v", config, err)
	}
}

func TestLANHTTPRejectsPublicPeersAndHosts(t *testing.T) {
	api, _ := testAPI(t)
	config, err := NewAuthConfig("0.0.0.0:8080", "")
	if err != nil {
		t.Fatal(err)
	}
	api.SetAuthConfig(config)
	for _, item := range []struct {
		peer, host string
		status     int
	}{
		{"192.168.1.20:1234", "192.168.1.187:8080", 200},
		{"127.0.0.1:1234", "127.0.0.1:8080", 200},
		{"127.0.0.1:1234", "localhost:8080", 200},
		{"10.1.2.3:1234", "10.1.2.4:8080", 200},
		{"8.8.8.8:1234", "192.168.1.187:8080", 403},
		{"192.168.1.20:1234", "8.8.8.8:8080", 403},
		{"192.168.1.20:1234", "attacker.example:8080", 403},
	} {
		r := httptest.NewRequest("GET", "http://"+item.host+"/api/v2/health", nil)
		r.RemoteAddr = item.peer
		r.Header.Set("X-Forwarded-For", "192.168.1.20")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != item.status {
			t.Fatalf("peer %s host %s: %d %s", item.peer, item.host, w.Code, w.Body.String())
		}
	}
}

func TestLANHTTPLoginRetainsOriginAndCookieProtection(t *testing.T) {
	api, _ := testAPI(t)
	config, err := NewAuthConfig("0.0.0.0:8080", "")
	if err != nil {
		t.Fatal(err)
	}
	api.SetAuthConfig(config)
	for _, origin := range []string{"http://attacker.example", "http://192.168.1.187:8080"} {
		r := httptest.NewRequest("POST", "http://192.168.1.187:8080/api/v2/auth/login", strings.NewReader(`{"username":"admin","password":"test administrator password"}`))
		r.RemoteAddr = "192.168.1.20:1234"
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if strings.Contains(origin, "attacker") {
			if w.Code != 403 {
				t.Fatalf("cross-origin LAN login: %d", w.Code)
			}
			continue
		}
		if w.Code != 200 {
			t.Fatalf("LAN login: %d %s", w.Code, w.Body.String())
		}
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
			t.Fatalf("LAN session cookie: %+v", cookies)
		}
	}
}
