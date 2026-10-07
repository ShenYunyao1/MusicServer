package music

import (
	"fmt"
	"strings"
)

// Scopes are deliberately small and endpoint-oriented.  Keeping the names in
// one place makes the policy returned by the key management API auditable.
const (
	ScopeLibraryRead    = "library:read"
	ScopeLibraryWrite   = "library:write"
	ScopeMediaRead      = "media:read"
	ScopeUploadsWrite   = "uploads:write"
	ScopeKeysManage     = "keys:manage"
	ScopeSettingsManage = "settings:manage"
	ScopeStatsRead      = "stats:read"
)

var knownScopes = map[string]struct{}{
	ScopeLibraryRead: {}, ScopeLibraryWrite: {}, ScopeMediaRead: {},
	ScopeUploadsWrite: {}, ScopeKeysManage: {}, ScopeSettingsManage: {},
	ScopeStatsRead: {},
}

// defaultScopes returns the least-privilege policy used when a new key omits
// the scopes member.  A non-nil empty slice is intentionally preserved by the
// caller and means that the key has no capabilities.
func defaultScopes() []string {
	return []string{ScopeLibraryRead, ScopeMediaRead}
}

func validateScopes(scopes []string) error {
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if _, ok := knownScopes[scope]; !ok {
			return fmt.Errorf("unknown scope %q", scope)
		}
		if _, duplicate := seen[scope]; duplicate {
			return fmt.Errorf("duplicate scope %q", scope)
		}
		seen[scope] = struct{}{}
	}
	return nil
}

func hasScope(auth authContext, scope string) bool {
	if auth.startup {
		return true
	}
	for _, candidate := range auth.scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

// endpointScope maps the stable v1 routes to their capability.  Unknown
// routes are left unscoped so handlers added later can decide their own policy
// while the server still returns a normal NOT_FOUND response.
func endpointScope(path, method string) string {
	playlistRoute := path == "/api/v1/playlists" || strings.HasPrefix(path, "/api/v1/playlists/")
	trackRoute := path == "/api/v1/tracks" || strings.HasPrefix(path, "/api/v1/tracks/")
	if path == "/api/v1/sync" || playlistRoute || trackRoute {
		if method == "GET" {
			return ScopeLibraryRead
		}
		if playlistRoute || trackRoute {
			return ScopeLibraryWrite
		}
	}
	if strings.HasPrefix(path, "/api/v1/media/") || strings.HasPrefix(path, "/api/v1/covers/") {
		return ScopeMediaRead
	}
	if path == "/api/v1/uploads" || strings.HasPrefix(path, "/api/v1/uploads/") {
		return ScopeUploadsWrite
	}
	if path == "/api/v1/admin/scan" {
		return ScopeLibraryWrite
	}
	if path == "/api/v1/auth/keys" || strings.HasPrefix(path, "/api/v1/auth/keys/") {
		return ScopeKeysManage
	}
	if path == "/api/v1/admin/stats" {
		return ScopeStatsRead
	}
	if path == "/api/v1/settings" || strings.HasPrefix(path, "/api/v1/settings/") || path == "/api/v1/admin/settings" || strings.HasPrefix(path, "/api/v1/admin/settings/") {
		return ScopeSettingsManage
	}
	return ""
}
