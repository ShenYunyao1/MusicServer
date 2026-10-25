package music

import (
	"errors"
	"net/http"
)

func writeObjectSyncError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, errSyncAuthChanged) {
		writeError(w, 401, "AUTH_INVALID", "authentication changed; sign in again")
		return true
	}
	if errors.Is(err, errSyncScopeChanged) {
		writeError(w, 403, "SCOPE_DENIED", "library read permission is required")
		return true
	}
	return false
}
func (a *API) objectSyncTracks(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	var input struct {
		Cursor string   `json:"cursor"`
		IDs    []string `json:"ids"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if decodeStrict(r, &input) != nil {
		writeError(w, 400, "INVALID_SYNC_BATCH", "invalid sync batch")
		return
	}
	result, err := a.store.ResolveSyncTracks(authFromRequest(r), input.Cursor, input.IDs)
	if err != nil {
		if writeObjectSyncError(w, err) {
			return
		}
		if errors.Is(err, errSyncRestart) {
			writeError(w, 409, "SYNC_RESTART", err.Error())
		} else if errors.Is(err, errResourceHidden) {
			writeError(w, 404, "TRACK_NOT_FOUND", "track is not available")
		} else {
			writeError(w, 400, "INVALID_SYNC_BATCH", err.Error())
		}
		return
	}
	writeJSON(w, 200, result)
}
