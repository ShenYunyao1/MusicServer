package music

import (
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
)

func bumpPublicCatalog(tx *sql.Tx) error {
	_, err := tx.Exec("UPDATE meta SET value=CAST(value AS INTEGER)+1 WHERE key='public_revision'")
	return err
}
func publicSummaryChanged(a, b Playlist) bool {
	if a.Visibility != "public" && b.Visibility != "public" {
		return false
	}
	return a.Visibility != b.Visibility || a.Name != b.Name || a.Description != b.Description || a.CoverID != b.CoverID || a.OwnerID != b.OwnerID || len(a.TrackIDs) != len(b.TrackIDs)
}
func publishedPlaylist(reader sqlReader, p Playlist) bool {
	if p.System || p.Visibility != "public" {
		return false
	}
	var enabled bool
	return reader.QueryRow("SELECT enabled=1 FROM users WHERE id=?", p.OwnerID).Scan(&enabled) == nil && enabled
}
func hasSubscription(reader sqlReader, userID, playlistID string) bool {
	var exists bool
	return reader.QueryRow("SELECT EXISTS(SELECT 1 FROM subscriptions WHERE user_id=? AND playlist_id=?)", userID, playlistID).Scan(&exists) == nil && exists
}

func playlistReadAccess(reader sqlReader, auth authContext, p Playlist) bool {
	if auth.session {
		return auth.role == "admin" || p.System || p.OwnerID == auth.userID || publishedPlaylist(reader, p)
	}
	return auth.allowedPlaylists[p.ID] && (p.System || p.OwnerID == auth.userID || hasSubscription(reader, auth.userID, p.ID) && publishedPlaylist(reader, p))
}
func publicPlaylistView(reader sqlReader, p Playlist, userID string) (Playlist, error) {
	if hasSubscription(reader, userID, p.ID) {
		return subscriptionView(reader, userID, p.ID)
	}
	p = clonePlaylist(p)
	p.ReadOnly = true
	p.TrackNotes = map[string]string{}
	if err := reader.QueryRow("SELECT display_name FROM users WHERE id=?", p.OwnerID).Scan(&p.SourceOwnerName); err != nil {
		return Playlist{}, err
	}
	return p, nil
}
func readablePlaylistView(reader sqlReader, auth authContext, p Playlist) (Playlist, error) {
	if p.System || p.OwnerID == auth.userID || auth.session && auth.role == "admin" {
		return clonePlaylist(p), nil
	}
	return publicPlaylistView(reader, p, auth.userID)
}
func playlistViews(reader sqlReader, auth authContext) ([]Playlist, error) {
	all, err := queryJSON[Playlist](reader, "SELECT payload FROM playlists ORDER BY sort_order,id")
	if err != nil {
		return nil, err
	}
	result := []Playlist{}
	for _, p := range all {
		if (p.System || p.OwnerID == auth.userID) && (auth.session || auth.allowedPlaylists[p.ID]) {
			result = append(result, p)
		}
	}
	subscribed, err := subscriptionViews(reader, auth.userID)
	if err != nil {
		return nil, err
	}
	for _, p := range subscribed {
		if p.OwnerID == auth.userID {
			continue
		}
		if auth.session || auth.allowedPlaylists[p.ID] {
			result = append(result, p)
		}
	}
	return result, nil
}
func setPlaylistVisibility(p *Playlist, visibility string) error {
	if p.System || p.ID == "library" {
		return errLibraryMembers
	}
	if visibility != "private" && visibility != "public" {
		return errors.New("visibility must be private or public")
	}
	if p.Visibility != visibility {
		p.Visibility = visibility
		if visibility == "public" {
			p.PublishedAt = time.Now().UTC()
		} else {
			p.PublishedAt = time.Time{}
		}
	}
	return nil
}
func (s *Store) SetPlaylistVisibility(id, visibility string, options ...MutationOptions) (Playlist, error) {
	return s.mutatePlaylistAction(id, false, "playlist.visibility", func(p *Playlist) error { return setPlaylistVisibility(p, visibility) }, options...)
}
func subscriberIdentity(tx *sql.Tx, options MutationOptions) error {
	if options.UserID == "" {
		return errResourceHidden
	}
	user, err := readUser(tx, options.UserID)
	if err != nil || !user.Enabled || user.MustChangePassword {
		return errResourceHidden
	}
	return nil
}
func (s *Store) Subscribe(id string, options MutationOptions) (Playlist, error) {
	var view Playlist
	err := s.transaction(func(tx *sql.Tx) error {
		if err := subscriberIdentity(tx, options); err != nil {
			return err
		}
		p, err := readPlaylistFrom(tx, id)
		if err != nil || !publishedPlaylist(tx, p) {
			return errResourceHidden
		}
		if p.OwnerID == options.UserID {
			return errors.New("cannot subscribe to your own playlist")
		}
		if hasSubscription(tx, options.UserID, id) {
			view, err = subscriptionView(tx, options.UserID, id)
			return err
		}
		if _, err = tx.Exec("INSERT INTO subscriptions(user_id,playlist_id,last_name,version,created_at) VALUES(?,?,?,1,?)", options.UserID, id, p.Name, persistedTime(time.Now())); err != nil {
			return err
		}
		if err := recordChange(tx, "playlist", id, false); err != nil {
			return err
		}
		if err := auditMutation(tx, "subscription.create", id, nil, map[string]string{"user_id": options.UserID}, []MutationOptions{options}); err != nil {
			return err
		}
		view, err = subscriptionView(tx, options.UserID, id)
		return err
	})
	return view, err
}
func (s *Store) Unsubscribe(id string, options MutationOptions) error {
	return s.transaction(func(tx *sql.Tx) error {
		if err := subscriberIdentity(tx, options); err != nil {
			return err
		}
		if !hasSubscription(tx, options.UserID, id) {
			return nil
		}
		keys, err := queryJSON[APIKey](tx, "SELECT payload FROM api_keys WHERE user_id=?", options.UserID)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if slices.Contains(key.AllowedPlaylistIDs, id) {
				key.AllowedPlaylistIDs = slices.DeleteFunc(key.AllowedPlaylistIDs, func(value string) bool { return value == id })
				if err := saveKey(tx, key); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec("DELETE FROM subscriptions WHERE user_id=? AND playlist_id=?", options.UserID, id); err != nil {
			return err
		}
		if err := recordChange(tx, "playlist", id, false); err != nil {
			return err
		}
		return auditMutation(tx, "subscription.delete", id, map[string]string{"user_id": options.UserID}, nil, []MutationOptions{options})
	})
}
func subscriptionView(reader sqlReader, userID, id string) (Playlist, error) {
	var name string
	var version int64
	if err := reader.QueryRow("SELECT last_name,version FROM subscriptions WHERE user_id=? AND playlist_id=?", userID, id).Scan(&name, &version); err != nil {
		return Playlist{}, err
	}
	p, err := readPlaylistFrom(reader, id)
	reason := ""
	if errors.Is(err, sql.ErrNoRows) {
		reason = "deleted"
	} else if err != nil {
		return Playlist{}, err
	} else {
		var enabled bool
		if err := reader.QueryRow("SELECT enabled=1 FROM users WHERE id=?", p.OwnerID).Scan(&enabled); err != nil {
			return Playlist{}, err
		}
		if !enabled {
			reason = "owner_disabled"
		} else if p.Visibility != "public" {
			reason = "private"
		}
	}
	if reason != "" {
		return Playlist{ID: id, Name: name, Visibility: "private", ReadOnly: true, SubscriptionStatus: "invalid", SubscriptionReason: reason, SubscriptionVersion: version, TrackIDs: []string{}, TrackNotes: map[string]string{}}, nil
	}
	p = clonePlaylist(p)
	p.TrackNotes = map[string]string{}
	p.ReadOnly = true
	p.SubscriptionStatus = "active"
	p.SubscriptionVersion = version
	if err := reader.QueryRow("SELECT display_name FROM users WHERE id=?", p.OwnerID).Scan(&p.SourceOwnerName); err != nil {
		return Playlist{}, err
	}
	rows, err := reader.Query("SELECT track_id,note FROM subscription_notes WHERE user_id=? AND playlist_id=?", userID, id)
	if err != nil {
		return Playlist{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var track, note string
		if err := rows.Scan(&track, &note); err != nil {
			return Playlist{}, err
		}
		if slices.Contains(p.TrackIDs, track) {
			p.TrackNotes[track] = note
		}
	}
	return p, rows.Err()
}
func subscriptionViews(reader sqlReader, userID string) ([]Playlist, error) {
	rows, err := reader.Query("SELECT playlist_id FROM subscriptions WHERE user_id=? ORDER BY created_at DESC,playlist_id", userID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []Playlist{}
	for _, id := range ids {
		view, err := subscriptionView(reader, userID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, view)
	}
	return result, nil
}
func (s *Store) Subscriptions(userID string) ([]Playlist, error) {
	var result []Playlist
	err := s.transaction(func(tx *sql.Tx) error { var err error; result, err = subscriptionViews(tx, userID); return err })
	return result, err
}
func (s *Store) UpdateSubscriptionNote(id, trackID, note string, options MutationOptions) (Playlist, error) {
	var result Playlist
	err := s.transaction(func(tx *sql.Tx) error {
		if err := subscriberIdentity(tx, options); err != nil {
			return err
		}
		before, err := subscriptionView(tx, options.UserID, id)
		if err != nil || before.SubscriptionStatus != "active" {
			return errResourceHidden
		}
		if err := checkVersion(before.SubscriptionVersion, []MutationOptions{options}); err != nil {
			return err
		}
		if !slices.Contains(before.TrackIDs, trackID) {
			return errMemberNotFound
		}
		if len(note) > 32000 {
			return errors.New("note is too long")
		}
		if strings.TrimSpace(note) == "" {
			_, err = tx.Exec("DELETE FROM subscription_notes WHERE user_id=? AND playlist_id=? AND track_id=?", options.UserID, id, trackID)
		} else {
			_, err = tx.Exec("INSERT INTO subscription_notes VALUES(?,?,?,?) ON CONFLICT(user_id,playlist_id,track_id) DO UPDATE SET note=excluded.note", options.UserID, id, trackID, note)
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE subscriptions SET version=version+1 WHERE user_id=? AND playlist_id=?", options.UserID, id); err != nil {
			return err
		}
		if err = recordChange(tx, "playlist", id, false); err != nil {
			return err
		}
		if err = auditMutation(tx, "subscription.note", id, before.TrackNotes, map[string]string{trackID: note}, []MutationOptions{options}); err != nil {
			return err
		}
		result, err = subscriptionView(tx, options.UserID, id)
		return err
	})
	return result, err
}
