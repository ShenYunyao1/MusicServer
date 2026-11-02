package music

import (
	"net/http"
	"strings"
)

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	allow := "GET"
	switch {
	case p == "/api/v2/auth/keys":
		allow = "GET, POST, DELETE"
	case accountAuthAllow(strings.TrimPrefix(p, "/api/v2/auth/")) != "" && strings.HasPrefix(p, "/api/v2/auth/"):
		allow = accountAuthAllow(strings.TrimPrefix(p, "/api/v2/auth/"))
	case p == "/api/v2/playlists" || p == "/api/v2/keys" || p == "/api/v2/admin/backups":
		allow = "GET, POST"
	case p == "/api/v2/admin/settings":
		allow = "GET, PUT, PATCH"
	case p == "/api/v2/admin/registration":
		allow = "GET, PUT"
	case p == "/api/v2/registration":
		allow = "POST"
	case p == "/api/v2/registration/config":
		allow = "GET"
	case strings.HasPrefix(p, "/api/v2/admin/backups/"):
		allow = "DELETE"
		if strings.HasSuffix(p, "/download") {
			allow = "GET, HEAD"
		}
		if strings.HasSuffix(p, "/policy") {
			allow = "PUT"
		}
	case strings.HasPrefix(p, "/api/v2/admin/storage/"):
		allow = "PUT"
		if strings.HasSuffix(p, "/cleanup-updates") {
			allow = "POST"
		}
		if strings.HasSuffix(p, "/accounts") {
			allow = "GET"
		}
	case p == "/api/v2/admin/update":
		allow = "GET"
	case strings.HasPrefix(p, "/api/v2/admin/update/") && (strings.HasSuffix(p, "/check") || strings.HasSuffix(p, "/apply") || strings.HasSuffix(p, "/install")):
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
		allow = "GET, PUT, PATCH, DELETE"
		if strings.HasSuffix(p, "/editor") {
			allow = "PUT"
		}
		if p == "/api/v2/playlists/editor" {
			allow = "POST"
		}
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
		if strings.HasSuffix(p, "/test") || strings.HasSuffix(p, "/rotate") {
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

func accountAuthAllow(path string) string {
	switch path {
	case "login", "logout", "activity", "sessions/revoke-others":
		return "POST"
	case "me":
		return "GET, PATCH"
	case "password":
		return "PUT"
	case "sessions":
		return "GET"
	}
	if strings.HasPrefix(path, "sessions/") && len(strings.TrimPrefix(path, "sessions/")) > 0 {
		return "DELETE"
	}
	return ""
}
