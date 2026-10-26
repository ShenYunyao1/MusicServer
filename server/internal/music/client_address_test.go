package music

import (
	"net/http/httptest"
	"testing"
)

func TestMailRegistrationClientAddressTrust(t *testing.T) {
	for _, tc := range []struct {
		name, peer, header, origin, want string
		secure                           bool
	}{
		{"proxy", "127.0.0.1:8000", "203.0.113.8", "https://music.example.com", "203.0.113.8", true},
		{"proxy ipv6", "[::1]:8000", "2001:db8::3", "https://music.example.com", "2001:db8::3", true},
		{"public forged", "203.0.113.9:8000", "203.0.113.8", "https://music.example.com", "203.0.113.9", true},
		{"LAN forged", "192.168.1.8:8000", "203.0.113.8", "", "192.168.1.8", false},
		{"unconfigured loopback", "127.0.0.1:8000", "203.0.113.8", "", "127.0.0.1", false},
		{"chain", "127.0.0.1:8000", "203.0.113.8, 203.0.113.7", "https://music.example.com", "127.0.0.1", true},
		{"invalid", "127.0.0.1:8000", "bad-address", "https://music.example.com", "127.0.0.1", true},
		{"unspecified", "127.0.0.1:8000", "0.0.0.0", "https://music.example.com", "127.0.0.1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &API{authConfig: AuthConfig{Origin: tc.origin, Secure: tc.secure}}
			r := httptest.NewRequest("POST", "http://music.example.com/api/v2/registration", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", tc.header)
			if got := a.requestClientIP(r); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
	a := &API{authConfig: AuthConfig{Origin: "https://music.example.com", Secure: true}}
	r := httptest.NewRequest("POST", "http://music.example.com/api/v2/registration", nil)
	r.RemoteAddr = "127.0.0.1:8000"
	r.Header.Add("X-Forwarded-For", "203.0.113.8")
	r.Header.Add("X-Forwarded-For", "203.0.113.9")
	if got := a.requestClientIP(r); got != "127.0.0.1" {
		t.Fatal("ambiguous proxy headers accepted", got)
	}
}
