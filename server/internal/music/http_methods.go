package music

import (
	"net/http"
	"strings"
)

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	allow := w.Header().Get("Allow")
	if allow == "" {
		allow = routeMethods(r.URL.Path)
	}
	if allow == "" {
		writeError(w, 404, "NOT_FOUND", "endpoint not found")
		return
	}
	w.Header().Set("Allow", allow)
	writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
}

func routeMethods(path string) string {
	p := strings.TrimPrefix(path, "/api/v2/")
	if p == path || strings.Contains(p, "//") {
		return ""
	}
	p = strings.TrimSuffix(p, "/")
	exact := map[string]string{
		"setup": "GET, POST", "health": "GET", "tracks": "GET", "playlists": "GET, POST", "playlists/editor": "POST", "playlists/order": "PUT",
		"auth/keys": "GET, POST, DELETE", "auth/keys/stats": "GET", "auth/avatar": "GET, PUT", "auth/avatar/content": "GET", "auth/profile": "PUT", "auth/storage": "GET", "auth/email": "GET", "auth/email/challenge": "POST", "auth/email/verify": "POST",
		"admin/settings": "GET, PUT, PATCH", "admin/users": "GET, POST", "admin/mail": "GET, PUT", "admin/mail/test": "POST", "admin/registration": "GET, PUT",
		"registration": "POST", "registration/config": "GET", "admin/backups": "GET, POST", "admin/backups/policy": "PUT",
		"admin/storage": "GET", "admin/storage/accounts": "GET", "admin/storage/policy": "PUT", "admin/storage/cleanup-updates": "POST",
		"admin/update": "GET", "admin/update/check": "POST", "admin/update/apply": "POST", "admin/removed": "GET", "admin/audit": "GET", "admin/stats": "GET", "admin/logs": "GET",
		"admin/scan": "POST", "admin/scan/status": "GET", "admin/scan/cancel": "POST",
		"community/events": "GET", "community/sync": "POST", "public/playlists": "GET", "subscriptions": "GET", "invitations": "GET",
		"listening/sessions": "POST", "listening/tracks": "GET", "sync": "GET", "sync/tracks": "POST",
		"uploads": "POST", "uploads/stage": "POST", "uploads/preflight": "GET", "uploads/resumable": "GET, POST",
		"browse/summary": "GET", "browse/tracks": "GET", "browse/playlists": "GET", "browse/playlists/move": "POST",
		"tags": "GET, POST", "tag-groups": "GET, POST", "tag-sync": "GET", "tag-batches": "GET, POST",
		"track-query": "POST", "track-facets": "POST", "saved-track-filters": "GET, POST",
	}
	if allow := exact[p]; allow != "" {
		return allow
	}
	if strings.HasPrefix(p, "auth/") {
		if allow := accountAuthAllow(strings.TrimPrefix(p, "auth/")); allow != "" {
			return allow
		}
	}
	a := strings.Split(p, "/")
	valid := func(i int) bool { return i < len(a) && validID.MatchString(a[i]) }
	switch {
	case len(a) == 2 && a[0] == "tag-groups" && tagGroupID(a[1]):
		return "PATCH, DELETE"
	case len(a) == 2 && (a[0] == "tags" || a[0] == "saved-track-filters") && tagUUID(a[1]):
		return "GET, PATCH, DELETE"
	case len(a) == 3 && a[0] == "tags" && tagUUID(a[1]) && a[2] == "merge":
		return "POST"
	case len(a) == 2 && a[0] == "tag-batches" && tagUUID(a[1]):
		return "GET, PATCH"
	case len(a) == 3 && a[0] == "tag-batches" && tagUUID(a[1]) && (a[2] == "start" || a[2] == "cancel" || a[2] == "retry"):
		return "POST"
	case len(a) == 3 && a[0] == "tracks" && tagUUID(a[1]) && a[2] == "tags":
		return "GET, PATCH"
	case len(a) == 3 && a[0] == "tracks" && tagUUID(a[1]) && a[2] == "tag-import-source":
		return "GET"
	case len(a) == 4 && a[0] == "auth" && a[1] == "keys" && valid(2) && a[3] == "rotate":
		return "POST"
	case len(a) == 3 && a[0] == "auth" && a[1] == "keys" && valid(2):
		return "PATCH, DELETE"
	case len(a) == 2 && (a[0] == "tracks" || a[0] == "playlists") && valid(1):
		if a[0] == "tracks" {
			return "GET, PATCH, DELETE"
		}
		return "GET, PUT, PATCH, DELETE"
	case len(a) == 2 && (a[0] == "covers" || a[0] == "media") && valid(1):
		return "GET, HEAD"
	case len(a) == 3 && a[0] == "tracks" && valid(1) && (a[2] == "cover" || a[2] == "lyrics"):
		return "PUT"
	case len(a) >= 3 && a[0] == "playlists" && valid(1):
		if len(a) == 3 {
			switch a[2] {
			case "editor":
				return "PUT"
			case "visibility", "order":
				return "PUT"
			case "tracks":
				return "POST"
			case "invitations":
				return "GET, POST"
			}
		}
		if len(a) == 4 && valid(3) {
			if a[2] == "tracks" {
				return "PATCH, DELETE"
			}
			if a[2] == "invitations" {
				return "DELETE"
			}
		}
		if len(a) == 5 && a[2] == "invitations" && valid(3) && a[4] == "resend" {
			return "POST"
		}
	case len(a) == 3 && a[0] == "public" && a[1] == "playlists" && valid(2):
		return "GET"
	case a[0] == "subscriptions" && valid(1):
		if len(a) == 2 {
			return "PUT, DELETE"
		}
		if len(a) == 4 && a[2] == "tracks" && valid(3) {
			return "PATCH"
		}
	case a[0] == "invitations" && valid(1):
		if len(a) == 2 {
			return "GET"
		}
		if len(a) == 3 && (a[2] == "accept" || a[2] == "decline") {
			return "POST"
		}
	case a[0] == "browse":
		if len(a) == 3 && (a[1] == "playback" || a[1] == "playlists") && valid(2) && a[1] == "playback" {
			return "GET"
		}
		if len(a) == 4 && a[1] == "playlists" && valid(2) && a[3] == "tracks" {
			return "GET"
		}
		if len(a) == 5 && a[1] == "playlists" && valid(2) && a[3] == "tracks" && a[4] == "move" {
			return "POST"
		}
	case len(a) >= 3 && a[0] == "uploads":
		if a[1] == "receipts" && len(a) == 3 && valid(2) {
			return "GET"
		}
		if a[1] == "resumable" && validResumeID(a[2]) {
			if len(a) == 3 {
				return "GET, PATCH, DELETE"
			}
			if len(a) == 4 {
				if a[3] == "finish" {
					return "POST"
				}
				if a[3] == "status" {
					return "GET"
				}
			}
		}
		if a[1] == "stage" && valid(2) {
			if len(a) == 3 {
				return "GET, DELETE"
			}
			if len(a) == 4 {
				if a[3] == "cover" {
					return "GET, PUT"
				}
				if a[3] == "commit" {
					return "POST"
				}
			}
		}
	case len(a) >= 3 && a[0] == "admin":
		switch a[1] {
		case "users":
			if valid(2) {
				if len(a) == 3 {
					return "PATCH, DELETE"
				}
				if len(a) == 4 {
					switch a[3] {
					case "tracks", "playlists", "keys":
						return "GET"
					case "transfer", "reset-password", "release-email":
						return "POST"
					case "purge":
						return "DELETE"
					}
				}
			}
		case "backups":
			if len(a) == 3 && valid(2) {
				return "DELETE"
			}
			if len(a) == 4 {
				if a[2] == "jobs" && valid(3) {
					return "DELETE"
				}
				if valid(2) && a[3] == "download" {
					return "GET, HEAD"
				}
			}
		case "storage":
			if len(a) == 4 && a[2] == "quotas" && valid(3) {
				return "PUT"
			}
		case "removed":
			if len(a) == 3 && valid(2) {
				return "POST, DELETE"
			}
		}
	}
	return ""
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
	if strings.HasPrefix(path, "sessions/") && len(strings.TrimPrefix(path, "sessions/")) > 0 && !strings.Contains(strings.TrimPrefix(path, "sessions/"), "/") {
		return "DELETE"
	}
	return ""
}
