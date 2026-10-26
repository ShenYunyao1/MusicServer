package music

import (
	"net"
	"net/http"
	"strings"
)

// The shipped Nginx template overwrites X-Forwarded-For with exactly one
// $remote_addr. Only the pinned HTTPS deployment's loopback proxy can supply
// that value; direct/public/LAN peers cannot manufacture new rate-limit buckets.
func (a *API) requestClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = strings.Trim(r.RemoteAddr, "[]")
	}
	peer := net.ParseIP(host)
	if peer == nil {
		return "unknown"
	}
	if a.authConfig.Secure && strings.HasPrefix(a.authConfig.Origin, "https://") && peer.IsLoopback() {
		values := r.Header.Values("X-Forwarded-For")
		if len(values) == 1 {
			ip := net.ParseIP(strings.TrimSpace(values[0]))
			if ip != nil && !ip.IsUnspecified() && !ip.IsMulticast() {
				return ip.String()
			}
		}
	}
	return peer.String()
}
