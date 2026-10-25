package music

import (
	"database/sql"
	"errors"
	"time"
)

const SyncTrackBatchLimit = 100

var errSyncAuthChanged = errors.New("sync authentication is no longer current")
var errSyncScopeChanged = errors.New("sync library read scope is no longer granted")

func currentSyncObjectAuth(reader sqlReader, auth authContext) (authContext, error) {
	if !auth.session {
		current, err := currentKeyAuth(reader, auth)
		if err != nil {
			return authContext{}, errSyncAuthChanged
		}
		if !hasScope(current, ScopeLibraryRead) {
			return authContext{}, errSyncScopeChanged
		}
		return current, nil
	}
	user, err := readUser(reader, auth.userID)
	if err != nil || !user.Enabled || user.MustChangePassword || user.DeletedAt != "" || user.AuthVersion != auth.authVersion {
		return authContext{}, errSyncAuthChanged
	}
	var valid bool
	now := time.Now()
	if err := reader.QueryRow("SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND user_id=? AND auth_version=? AND revoked_at='' AND expires_at>? AND last_active_at>?)", auth.keyID, auth.userID, auth.authVersion, persistedTime(now), persistedTime(now.Add(-sessionIdle))).Scan(&valid); err != nil {
		return authContext{}, err
	}
	if !valid {
		return authContext{}, errSyncAuthChanged
	}
	auth.role = user.Role
	return auth, nil
}

type SyncTracksResponse struct {
	Revision int64   `json:"revision"`
	Tracks   []Track `json:"tracks"`
}

func (s *Store) SyncObjectPage(auth authContext, token string, limit int) (SyncResponse, error) {
	return s.syncPage(auth, token, limit, true)
}

// ResolveSyncTracks validates every identity before returning any details. The
// completed cursor binds the batch to exactly the authorization and library
// revision whose playlist membership the client is about to commit.
func (s *Store) ResolveSyncTracks(auth authContext, token string, ids []string) (SyncTracksResponse, error) {
	result := SyncTracksResponse{Tracks: []Track{}}
	if token == "" || len(ids) == 0 || len(ids) > SyncTrackBatchLimit {
		return result, errors.New("cursor and 1 to 100 track IDs required")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !validID.MatchString(id) || seen[id] {
			return result, errors.New("invalid or duplicate track ID")
		}
		seen[id] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	auth, err = currentSyncObjectAuth(tx, auth)
	if err != nil {
		return result, err
	}
	var secret string
	if err := tx.QueryRow("SELECT value FROM meta WHERE key='sync_secret'").Scan(&secret); err != nil {
		return result, err
	}
	if err := tx.QueryRow("SELECT value FROM meta WHERE key='revision'").Scan(&result.Revision); err != nil {
		return result, err
	}
	cursor, err := parseCursor(token, secret)
	if err != nil {
		return result, err
	}
	playlists, err := playlistViews(tx, auth)
	if err != nil {
		return result, err
	}
	if cursor.Mode != "delta" || cursor.Last != "" || cursor.Revision != result.Revision || cursor.Expires < time.Now().Unix() || cursor.Binding != syncProtocolBinding(auth, playlists, true) {
		return result, errSyncRestart
	}
	allowed := map[string]bool{}
	for _, p := range playlists {
		if !canSyncPlaylist(auth, p) || p.SubscriptionStatus != "" && p.SubscriptionStatus != "active" {
			continue
		}
		for _, id := range p.TrackIDs {
			allowed[id] = true
		}
	}
	for _, id := range ids {
		if !auth.session && !allowed[id] {
			return result, errResourceHidden
		}
	}
	tracks := make([]Track, 0, len(ids))
	for _, id := range ids {
		track, err := readTrackFrom(tx, id, false)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) || !allowed[id] {
				return result, errResourceHidden
			}
			track = Track{ID: id, Missing: true}
		}
		if track.Missing || track.Removed {
			track = unavailableTrack(track)
		}
		tracks = append(tracks, publicTrack(track, auth))
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	result.Tracks = tracks
	return result, nil
}
