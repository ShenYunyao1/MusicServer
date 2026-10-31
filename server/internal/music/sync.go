package music

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

type syncCursor struct {
	Mode           string   `json:"mode"`
	Position       int64    `json:"position"`
	Revision       int64    `json:"revision"`
	Last           string   `json:"last"`
	Binding        string   `json:"binding"`
	Expires        int64    `json:"expires"`
	OwnedPlaylists []string `json:"owned_playlists,omitempty"`
	StableBinding  string   `json:"stable_binding,omitempty"`
	TrackBinding   string   `json:"track_binding,omitempty"`
}

const syncKnownPlaylistLimit = 32

func syncOwnedProof(auth authContext, playlists []Playlist, objects bool) ([]string, string, string) {
	owned := []string{}
	remaining := []Playlist{}
	reduced := auth
	reduced.allowedPlaylists = map[string]bool{}
	ownedSet := map[string]bool{}
	visibleTracks := map[string]bool{}
	for _, p := range playlists {
		if !canSyncPlaylist(auth, p) {
			continue
		}
		for _, id := range p.TrackIDs {
			visibleTracks[id] = true
		}
		if !p.System && !p.ReadOnly && p.OwnerID == auth.userID {
			owned = append(owned, p.ID)
			ownedSet[p.ID] = true
		} else {
			remaining = append(remaining, p)
		}
	}
	if len(owned) > syncKnownPlaylistLimit {
		return nil, "", ""
	}
	sort.Strings(owned)
	for id, allowed := range auth.allowedPlaylists {
		if allowed && !ownedSet[id] {
			reduced.allowedPlaylists[id] = true
		}
	}
	stable := syncProtocolBinding(reduced, remaining, objects) + "/owned-deletion-v1"
	trackBinding := ""
	if !auth.session && !objects {
		ids := []string{}
		for id := range visibleTracks {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		raw, _ := json.Marshal(ids)
		trackBinding = hashBytes(raw)
	}
	return owned, stable, trackBinding
}

func syncDeletedOwned(tx *sql.Tx, auth authContext, parsed syncCursor, playlists []Playlist, objects bool) ([]string, bool, error) {
	owned, stable, tracks := syncOwnedProof(auth, playlists, objects)
	if parsed.StableBinding == "" || stable != parsed.StableBinding || tracks != parsed.TrackBinding || len(parsed.OwnedPlaylists) > syncKnownPlaylistLimit {
		return nil, false, nil
	}
	previous := map[string]bool{}
	for _, id := range parsed.OwnedPlaylists {
		if previous[id] {
			return nil, false, nil
		}
		previous[id] = true
	}
	for _, id := range owned {
		if !previous[id] {
			return nil, false, nil
		}
		delete(previous, id)
	}
	removed := []string{}
	for id := range previous {
		var retired bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM playlist_tombstones WHERE id=?)", id).Scan(&retired); err != nil {
			return nil, false, err
		}
		if !retired {
			return nil, false, nil
		}
		removed = append(removed, id)
	}
	return removed, len(removed) > 0, nil
}

var errSyncRestart = errors.New("library changed during pagination; restart snapshot")

func canSyncPlaylist(auth authContext, p Playlist) bool {
	if p.ReadOnly && p.SubscriptionStatus != "" {
		return auth.session || auth.allowedPlaylists[p.ID]
	}
	if auth.session {
		return p.System || p.OwnerID == auth.userID
	}
	return auth.allowedPlaylists[p.ID] && (p.System || p.OwnerID == auth.userID)
}

func syncBinding(auth authContext, playlists []Playlist) string {
	return syncProtocolBinding(auth, playlists, false)
}

func syncProtocolBinding(auth authContext, playlists []Playlist, objects bool) string {
	grants := []string{}
	for id, allowed := range auth.allowedPlaylists {
		if allowed {
			grants = append(grants, id)
		}
	}
	sort.Strings(grants)
	scopes := cloneStrings(auth.scopes)
	sort.Strings(scopes)
	visible := map[string]bool{}
	visiblePlaylists := []string{}
	{
		for _, playlist := range playlists {
			if canSyncPlaylist(auth, playlist) {
				visiblePlaylists = append(visiblePlaylists, playlist.ID+":"+playlist.SubscriptionStatus+":"+playlist.SubscriptionReason+":"+playlist.OwnerID)
				if !objects && !auth.session {
					for _, id := range playlist.TrackIDs {
						visible[id] = true
					}
				}
			}
		}
	}
	members := []string{}
	for id := range visible {
		members = append(members, id)
	}
	sort.Strings(members)
	sort.Strings(visiblePlaylists)
	if auth.session || objects {
		members = nil
	}
	encoded, _ := json.Marshal([]any{auth.userID, auth.authVersion, auth.keyID, auth.role, auth.session, grants, scopes, members, visiblePlaylists})
	if objects {
		encoded = append(encoded, []byte("/objects-v2")...)
	}
	return hashBytes(encoded)
}
func signCursor(cursor syncCursor, secret string) string {
	raw, _ := json.Marshal(cursor)
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(signature.Sum(nil))
}
func parseCursor(token, secret string) (syncCursor, error) {
	var cursor syncCursor
	if len(token) > 16384 {
		return cursor, errors.New("sync cursor exceeds size limit")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return cursor, errors.New("invalid sync cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursor, err
	}
	signed, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursor, err
	}
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write(raw)
	if !hmac.Equal(signature.Sum(nil), signed) {
		return cursor, errors.New("invalid sync cursor signature")
	}
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return cursor, err
	}
	if len(cursor.OwnedPlaylists) > syncKnownPlaylistLimit {
		return cursor, errors.New("invalid sync playlist proof")
	}
	return cursor, nil
}
func visiblePlaylist(playlist Playlist, auth authContext, tracks map[string]bool) Playlist {
	result := clonePlaylist(playlist)
	result.TrackIDs = []string{}
	result.TrackNotes = map[string]string{}
	result.UnavailableTrackIDs = []string{}
	for _, id := range playlist.TrackIDs {
		{
			if !tracks[id] {
				result.UnavailableTrackIDs = append(result.UnavailableTrackIDs, id)
			}
			result.TrackIDs = append(result.TrackIDs, id)
			if note, ok := playlist.TrackNotes[id]; ok {
				result.TrackNotes[id] = note
			}
		}
	}
	return result
}
func (s *Store) SyncPage(auth authContext, token string, limit int) (SyncResponse, error) {
	return s.syncPage(auth, token, limit, false)
}

func (s *Store) syncPage(auth authContext, token string, limit int, objects bool) (SyncResponse, error) {
	result := SyncResponse{Changed: []Track{}, Playlists: []Playlist{}, DeletedTracks: []string{}, DeletedPlaylists: []string{}}
	result.ObjectDelta = objects
	if objects {
		result.ChangedTrackIDs = []string{}
	}
	if limit < 1 || limit > 500 {
		limit = 100
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
	playlists, err := playlistViews(tx, auth)
	if err != nil {
		return result, err
	}
	binding := syncProtocolBinding(auth, playlists, objects)
	cursor := syncCursor{Mode: "snapshot", Revision: result.Revision, Binding: binding, Expires: time.Now().Add(24 * time.Hour).Unix()}
	result.Reset = token == ""
	if token != "" {
		parsed, err := parseCursor(token, secret)
		if err != nil {
			return result, err
		}
		bindingMatches := parsed.Binding == binding
		if !bindingMatches && parsed.Mode == "delta" && parsed.Last == "" && parsed.Expires >= time.Now().Unix() {
			removed, compatible, err := syncDeletedOwned(tx, auth, parsed, playlists, objects)
			if err != nil {
				return result, err
			}
			if compatible {
				bindingMatches = true
				parsed.Binding = binding
				result.DeletedPlaylists = append(result.DeletedPlaylists, removed...)
			}
		}
		if bindingMatches && parsed.Expires >= time.Now().Unix() {
			cursor = parsed
			if (cursor.Mode == "snapshot" || objects && cursor.Last == "paging") && cursor.Revision != result.Revision {
				return result, errSyncRestart
			}
			var earliest sql.NullInt64
			if err := tx.QueryRow("SELECT min(revision) FROM changes").Scan(&earliest); err != nil {
				return result, err
			}
			if cursor.Mode == "delta" && (cursor.Position > result.Revision || (earliest.Valid && cursor.Position < earliest.Int64-1)) {
				cursor = syncCursor{Mode: "snapshot", Revision: result.Revision, Binding: binding}
				result.Reset = true
				result.DeletedPlaylists = []string{}
			}
		} else {
			result.Reset = true
		}
	}
	cursor.Expires = time.Now().Add(24 * time.Hour).Unix()
	allowedTracks := map[string]bool{}
	allowedPlaylists := map[string]Playlist{}
	for _, playlist := range playlists {
		if canSyncPlaylist(auth, playlist) {
			allowedPlaylists[playlist.ID] = playlist
			for _, id := range playlist.TrackIDs {
				allowedTracks[id] = true
			}
		}
	}
	active := map[string]bool{}
	available := map[string]bool{}
	trackRows, err := tx.Query("SELECT id,missing FROM tracks WHERE removed=0")
	if err != nil {
		return result, err
	}
	for trackRows.Next() {
		var id string
		var missing bool
		if err := trackRows.Scan(&id, &missing); err != nil {
			_ = trackRows.Close()
			return result, err
		}
		if auth.session || allowedTracks[id] {
			available[id] = !missing
			active[id] = true
		}
	}
	err = trackRows.Err()
	_ = trackRows.Close()
	if err != nil {
		return result, err
	}
	if cursor.Mode == "snapshot" {
		result.FullSnapshot = true
		keys := []string{}
		for id := range active {
			keys = append(keys, "track/"+id)
		}
		for id := range allowedPlaylists {
			keys = append(keys, "playlist/"+id)
		}
		sort.Strings(keys)
		start := sort.Search(len(keys), func(index int) bool { return keys[index] > cursor.Last })
		end := min(start+limit, len(keys))
		for _, key := range keys[start:end] {
			kind, id, _ := strings.Cut(key, "/")
			if kind == "track" {
				if objects {
					result.ChangedTrackIDs = append(result.ChangedTrackIDs, id)
					cursor.Last = key
					continue
				}
				track, err := readTrackFrom(tx, id, false)
				if err != nil {
					return result, err
				}
				if track.Missing {
					track = unavailableTrack(track)
				}
				result.Changed = append(result.Changed, publicTrack(track, auth))
			} else {
				result.Playlists = append(result.Playlists, visiblePlaylist(allowedPlaylists[id], auth, available))
			}
			cursor.Last = key
		}
		result.HasMore = end < len(keys)
		if !result.HasMore {
			cursor.Mode = "delta"
			cursor.Position = result.Revision
			cursor.Last = ""
		}
	} else if cursor.Mode == "delta" {
		rows, err := tx.Query("SELECT revision,kind,entity_id,deleted FROM changes WHERE revision>? ORDER BY revision LIMIT ?", cursor.Position, limit+1)
		if err != nil {
			return result, err
		}
		type change struct {
			revision int64
			kind, id string
			deleted  bool
		}
		changes := []change{}
		for rows.Next() {
			var item change
			if err := rows.Scan(&item.revision, &item.kind, &item.id, &item.deleted); err != nil {
				_ = rows.Close()
				return result, err
			}
			changes = append(changes, item)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return result, err
		}
		result.HasMore = len(changes) > limit
		if result.HasMore {
			changes = changes[:limit]
		}
		latest := map[string]change{}
		for _, item := range changes {
			latest[item.kind+"/"+item.id] = item
			cursor.Position = item.revision
		}
		for _, item := range latest {
			if item.kind == "track" {
				if !auth.session && !allowedTracks[item.id] {
					continue
				}
				if !active[item.id] || item.deleted {
					result.DeletedTracks = append(result.DeletedTracks, item.id)
				} else {
					if objects {
						result.ChangedTrackIDs = append(result.ChangedTrackIDs, item.id)
						continue
					}
					track, err := readTrackFrom(tx, item.id, false)
					if err != nil {
						return result, err
					}
					if track.Missing {
						track = unavailableTrack(track)
					}
					result.Changed = append(result.Changed, publicTrack(track, auth))
				}
			} else if item.kind == "playlist" {
				if playlist, ok := allowedPlaylists[item.id]; ok && (!item.deleted || playlist.SubscriptionStatus == "invalid") {
					result.Playlists = append(result.Playlists, visiblePlaylist(playlist, auth, available))
				}
			}
		}
		if len(changes) == 0 {
			cursor.Position = result.Revision
		}
		if objects {
			cursor.Revision = result.Revision
			cursor.Last = ""
			if result.HasMore {
				cursor.Last = "paging"
			}
		}
	} else {
		return result, fmt.Errorf("unknown cursor mode %q", cursor.Mode)
	}
	slices.Sort(result.DeletedTracks)
	slices.Sort(result.DeletedPlaylists)
	slices.Sort(result.ChangedTrackIDs)
	if cursor.Mode == "delta" {
		cursor.OwnedPlaylists, cursor.StableBinding, cursor.TrackBinding = syncOwnedProof(auth, playlists, objects)
	}
	result.Cursor = signCursor(cursor, secret)
	return result, tx.Commit()
}
