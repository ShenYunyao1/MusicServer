package music

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

// TagSyncState contains only account-bound invalidation stamps. It is never
// published in the shared object stream or exposed to a player Key.
type TagSyncState struct {
	TrackFilterBaseline
	Saved string `json:"saved"`
}

func (s *Store) tagSyncState(auth authContext) (TagSyncState, error) {
	var state TagSyncState
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		var err error
		state.TrackFilterBaseline, err = trackFilterBaseline(tx, auth)
		if err != nil {
			return err
		}
		// Saved rules are capped at 100 per account. Hash IDs and versions rather
		// than names/rules: no JSON drafts need decoding, and deletes invalidate
		// even when count or MAX(version) alone would remain unchanged.
		rows, err := tx.Query("SELECT id,version FROM saved_track_filters WHERE user_id=? ORDER BY id LIMIT ?", auth.userID, savedTrackFilterLimit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		type stamp struct {
			ID      string
			Version int64
		}
		items := []stamp{}
		for rows.Next() {
			var item stamp
			if err := rows.Scan(&item.ID, &item.Version); err != nil {
				return err
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(items) > savedTrackFilterLimit {
			return errTagLimit
		}
		encoded, err := json.Marshal(items)
		if err != nil {
			return err
		}
		state.Saved = hashBytes(encoded)
		return nil
	})
	return state, err
}

func (a *API) tagSyncHTTP(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/v2/tag-sync" {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if !a.requireAccount(w, r) {
		return true
	}
	if r.Method != "GET" {
		tagMethod(w, "GET")
		return true
	}
	if !tagNoQuery(w, r) {
		return true
	}
	state, err := a.store.tagSyncState(authFromRequest(r))
	if err != nil {
		writeTagError(w, err)
		return true
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		writeTagError(w, err)
		return true
	}
	etag := `"tags-` + hashBytes(encoded) + `"`
	w.Header().Set("ETag", etag)
	// The transaction above rechecks live session and schema before a 304.
	if matchesETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	writeJSON(w, http.StatusOK, state)
	return true
}
