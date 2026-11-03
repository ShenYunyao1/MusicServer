package music

import (
	"database/sql"
	"errors"
	"net/http"
)

// Do not disclose a foreign private snapshot unless its access is audited.
// Recheck session, owner and version in this short write transaction; own reads
// never enter it. A takeover/edit while queued requires a fresh read.
func (s *Store) auditPlaylistInspection(auth authContext, id, owner string, version int64, options MutationOptions, detail map[string]any) error {
	return s.transaction(func(tx *sql.Tx) error {
		if err := browseIdentity(tx, auth); err != nil {
			return err
		}
		p, err := readPlaylistFrom(tx, id)
		if err != nil {
			return err
		}
		if p.OwnerID != owner || p.Version != version || p.System || p.OwnerID == auth.userID {
			return errBrowseRestart
		}
		if auth.role != "admin" {
			return errResourceHidden
		}
		detail["owner_id"] = owner
		return auditMutation(tx, "playlist.admin.read", id, nil, detail, []MutationOptions{options})
	})
}

func (a *API) readPlaylistDetail(w http.ResponseWriter, r *http.Request, id string) {
	auth := authFromRequest(r)
	var playlist, view Playlist
	var tracks []Track
	err := a.store.readQuery(r.Context(), func(reader sqlReader) error {
		var err error
		auth, err = currentSyncObjectAuth(reader, auth)
		if err != nil {
			return err
		}
		playlist, err = readPlaylistFrom(reader, id)
		if err != nil {
			return err
		}
		if !playlistReadAccess(reader, auth, playlist) {
			return errResourceHidden
		}
		tracks, err = queryJSON[Track](reader, "SELECT t.payload FROM playlist_tracks m JOIN tracks t ON t.id=m.track_id WHERE m.playlist_id=? ORDER BY m.position", id)
		if err != nil {
			return err
		}
		for i, track := range tracks {
			if track.Removed || track.Missing {
				track = unavailableTrack(track)
			}
			tracks[i] = publicTrack(track, auth)
		}
		view, err = readablePlaylistView(reader, auth, playlist)
		return err
	})
	if err == nil && auth.session && auth.role == "admin" && !playlist.System && playlist.OwnerID != auth.userID {
		err = a.store.auditPlaylistInspection(auth, id, playlist.OwnerID, playlist.Version, mutationOptions(r), map[string]any{})
	}
	if err != nil {
		if writeObjectSyncError(w, err) {
			return
		}
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, errResourceHidden) {
			writeError(w, 404, "NOT_FOUND", "playlist not found")
			return
		}
		if errors.Is(err, errBrowseRestart) {
			writeBrowseError(w, err)
			return
		}
		writeError(w, 503, "READ_FAILED", "unable to read playlist")
		return
	}
	writeJSON(w, 200, map[string]any{"playlist": view, "tracks": tracks})
}
