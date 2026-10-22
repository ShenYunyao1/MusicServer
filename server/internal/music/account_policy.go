package music

import (
	"errors"
	"net/http"
	"strings"
)

var errResourceHidden = errors.New("resource not found")

// Mutation identity is supplied by the authenticated server handler, never JSON.
func checkActorOwner(reader sqlReader, owner string, options []MutationOptions) error {
	if len(options) == 0 || options[0].UserID == "" {
		return nil
	}
	user, err := readUser(reader, options[0].UserID)
	if err != nil || !user.Enabled || user.MustChangePassword {
		return errResourceHidden
	}
	if user.Role != "admin" && user.ID != owner {
		return errResourceHidden
	}
	return nil
}
func (a *API) requireAccount(response http.ResponseWriter, request *http.Request) bool {
	if !authFromRequest(request).session {
		writeError(response, 403, "SESSION_REQUIRED", "account session required")
		return false
	}
	return true
}
func (a *API) authorizeResource(response http.ResponseWriter, request *http.Request) bool {
	auth := authFromRequest(request)
	path := request.URL.Path
	write := request.Method != "GET" && request.Method != "HEAD"
	deny := func() bool { writeError(response, 404, "NOT_FOUND", "resource not found"); return false }
	if strings.HasPrefix(path, "/api/v2/playlists/") && path != "/api/v2/playlists/order" {
		id := strings.Split(strings.TrimPrefix(path, "/api/v2/playlists/"), "/")[0]
		p, ok := a.store.Playlist(id)
		if !ok || !canReadPlaylist(auth, id) {
			return deny()
		}
		if write && (!auth.session || (auth.role != "admin" && p.OwnerID != auth.userID)) {
			return deny()
		}
	}
	if write && strings.HasPrefix(path, "/api/v2/tracks/") {
		id := strings.Split(strings.TrimPrefix(path, "/api/v2/tracks/"), "/")[0]
		track, ok := a.store.Track(id)
		if !ok || !auth.session || (auth.role != "admin" && track.OwnerID != auth.userID) {
			return deny()
		}
	}
	if strings.HasPrefix(path, "/api/v2/uploads/stage/") {
		id := strings.Split(strings.TrimPrefix(path, "/api/v2/uploads/stage/"), "/")[0]
		var owner string
		if a.store.db.QueryRow("SELECT user_id FROM upload_staging WHERE id=?", id).Scan(&owner) != nil || owner != auth.userID {
			return deny()
		}
	}
	if strings.HasPrefix(path, "/api/v2/auth/keys/") && path != "/api/v2/auth/keys/stats" {
		id := strings.Split(strings.TrimPrefix(path, "/api/v2/auth/keys/"), "/")[0]
		var owner string
		if id != "" && (a.store.db.QueryRow("SELECT user_id FROM api_keys WHERE id=?", id).Scan(&owner) != nil || owner != auth.userID) {
			return deny()
		}
	}
	return true
}

func publicTrack(track Track, auth authContext) Track {
	if auth.role != "admin" {
		track.Path = ""
	}
	return track
}

func unavailableTrack(track Track) Track {
	return Track{ID: track.ID, Title: "Unavailable", Removed: track.Removed, Missing: track.Missing, Version: track.Version}
}
