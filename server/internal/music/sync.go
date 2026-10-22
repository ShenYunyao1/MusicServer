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
	Mode     string `json:"mode"`
	Position int64  `json:"position"`
	Revision int64  `json:"revision"`
	Last     string `json:"last"`
	Binding  string `json:"binding"`
	Expires  int64  `json:"expires"`
}

var errSyncRestart = errors.New("library changed during pagination; restart snapshot")

func syncBinding(auth authContext, playlists []Playlist) string {
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
	if auth.role != "admin" {
		for _, playlist := range playlists {
			if canReadPlaylist(auth, playlist.ID) {
				for _, id := range playlist.TrackIDs {
					visible[id] = true
				}
			}
		}
	}
	members := []string{}
	for id := range visible {
		members = append(members, id)
	}
	sort.Strings(members)
	encoded, _ := json.Marshal([]any{auth.userID, auth.authVersion, auth.keyID, auth.role, grants, scopes, members})
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
	return cursor, nil
}
func visiblePlaylist(playlist Playlist, auth authContext, tracks map[string]bool) Playlist {
	result := clonePlaylist(playlist)
	result.TrackIDs = []string{}
	result.TrackNotes = map[string]string{}
	for _, id := range playlist.TrackIDs {
		if tracks[id] {
			result.TrackIDs = append(result.TrackIDs, id)
			if note, ok := playlist.TrackNotes[id]; ok {
				result.TrackNotes[id] = note
			}
		}
	}
	return result
}
func (s *Store) SyncPage(auth authContext, token string, limit int) (SyncResponse, error) {
	result := SyncResponse{Changed: []Track{}, Playlists: []Playlist{}, DeletedTracks: []string{}, DeletedPlaylists: []string{}}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var secret string
	if err := tx.QueryRow("SELECT value FROM meta WHERE key='sync_secret'").Scan(&secret); err != nil {
		return result, err
	}
	if err := tx.QueryRow("SELECT value FROM meta WHERE key='revision'").Scan(&result.Revision); err != nil {
		return result, err
	}
	playlists, err := queryJSON[Playlist](tx, "SELECT payload FROM playlists")
	if err != nil {
		return result, err
	}
	binding := syncBinding(auth, playlists)
	cursor := syncCursor{Mode: "snapshot", Revision: result.Revision, Binding: binding, Expires: time.Now().Add(24 * time.Hour).Unix()}
	result.Reset = token == ""
	if token != "" {
		parsed, err := parseCursor(token, secret)
		if err != nil {
			return result, err
		}
		if parsed.Binding == binding && parsed.Expires >= time.Now().Unix() {
			cursor = parsed
			if cursor.Mode == "snapshot" && cursor.Revision != result.Revision {
				return result, errSyncRestart
			}
			var earliest sql.NullInt64
			if err := tx.QueryRow("SELECT min(revision) FROM changes").Scan(&earliest); err != nil {
				return result, err
			}
			if cursor.Mode == "delta" && (cursor.Position > result.Revision || (earliest.Valid && cursor.Position < earliest.Int64-1)) {
				cursor = syncCursor{Mode: "snapshot", Revision: result.Revision, Binding: binding}
				result.Reset = true
			}
		} else {
			result.Reset = true
		}
	}
	cursor.Expires = time.Now().Add(24 * time.Hour).Unix()
	allowedTracks := map[string]bool{}
	allowedPlaylists := map[string]Playlist{}
	for _, playlist := range playlists {
		if canReadPlaylist(auth, playlist.ID) {
			allowedPlaylists[playlist.ID] = playlist
			for _, id := range playlist.TrackIDs {
				allowedTracks[id] = true
			}
		}
	}
	active := map[string]bool{}
	trackRows, err := tx.Query("SELECT id FROM tracks WHERE removed=0")
	if err != nil {
		return result, err
	}
	for trackRows.Next() {
		var id string
		if err := trackRows.Scan(&id); err != nil {
			_ = trackRows.Close()
			return result, err
		}
		if auth.role == "admin" || allowedTracks[id] {
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
				track, err := readTrackFrom(tx, id, false)
				if err != nil {
					return result, err
				}
				result.Changed = append(result.Changed, publicTrack(track, auth))
			} else {
				result.Playlists = append(result.Playlists, visiblePlaylist(allowedPlaylists[id], auth, active))
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
				if auth.role != "admin" && !allowedTracks[item.id] {
					continue
				}
				if !active[item.id] || item.deleted {
					result.DeletedTracks = append(result.DeletedTracks, item.id)
				} else {
					track, err := readTrackFrom(tx, item.id, false)
					if err != nil {
						return result, err
					}
					result.Changed = append(result.Changed, publicTrack(track, auth))
				}
			} else if item.kind == "playlist" {
				if playlist, ok := allowedPlaylists[item.id]; ok && !item.deleted {
					result.Playlists = append(result.Playlists, visiblePlaylist(playlist, auth, active))
				} else if auth.role == "admin" {
					result.DeletedPlaylists = append(result.DeletedPlaylists, item.id)
				}
			}
		}
		if len(changes) == 0 {
			cursor.Position = result.Revision
		}
	} else {
		return result, fmt.Errorf("unknown cursor mode %q", cursor.Mode)
	}
	slices.Sort(result.DeletedTracks)
	slices.Sort(result.DeletedPlaylists)
	result.Cursor = signCursor(cursor, secret)
	return result, tx.Commit()
}
