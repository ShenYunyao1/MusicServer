package music

import (
	"net/http"
	"strings"
)

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	allow := "GET"
	switch {
	case p == "/api/v2/playlists" || p == "/api/v2/keys" || p == "/api/v2/auth/keys":
		allow = "GET, POST"
	case p == "/api/v2/admin/settings":
		allow = "GET, PUT"
	case p == "/api/v2/admin/update":
		allow = "GET"
	case strings.HasSuffix(p, "/check") || strings.HasSuffix(p, "/apply") || strings.HasSuffix(p, "/install"):
		allow = "POST"
	case strings.HasPrefix(p, "/api/v2/covers/") || strings.HasPrefix(p, "/api/v2/media/"):
		allow = "GET, HEAD"
	case p == "/api/v2/uploads" || p == "/api/v2/uploads/stage" || p == "/api/v2/admin/scan":
		allow = "POST"
	case strings.HasPrefix(p, "/api/v2/uploads/stage/"):
		allow = "GET, DELETE"
		if strings.HasSuffix(p, "/commit") {
			allow = "POST"
		}
		if strings.HasSuffix(p, "/cover") {
			allow = "GET, PUT"
		}
	case strings.HasPrefix(p, "/api/v2/playlists/"):
		allow = "GET, PATCH, DELETE"
		if strings.HasSuffix(p, "/visibility") || strings.HasSuffix(p, "/order") {
			allow = "PUT"
		} else if strings.HasSuffix(p, "/tracks") {
			allow = "POST"
		} else if strings.Contains(p, "/tracks/") {
			allow = "PATCH, DELETE"
		}
	case strings.HasPrefix(p, "/api/v2/tracks/"):
		allow = "GET, PATCH, DELETE"
		if strings.HasSuffix(p, "/cover") || strings.HasSuffix(p, "/lyrics") {
			allow = "PUT"
		}
	case strings.HasPrefix(p, "/api/v2/auth/keys/") || strings.HasPrefix(p, "/api/v2/keys/"):
		allow = "PATCH, DELETE"
		if strings.HasSuffix(p, "/test") {
			allow = "POST"
		}
	case strings.HasPrefix(p, "/api/v2/admin/removed/"):
		allow = "POST, DELETE"
	case strings.HasSuffix(p, "/cancel"):
		allow = "POST"
	}
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
}
