package music

import (
	"database/sql"
	"errors"
	"slices"
	"time"
)

type ResourceVersion struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}
type TransferRequest struct {
	TargetID      string            `json:"target_id"`
	TargetVersion int64             `json:"target_version"`
	All           bool              `json:"all"`
	Tracks        []ResourceVersion `json:"tracks"`
	Playlists     []ResourceVersion `json:"playlists"`
}

func requireAdminActor(tx *sql.Tx, options []MutationOptions) error {
	if len(options) == 0 || options[0].UserID == "" {
		return errors.New("administrator identity required")
	}
	user, err := readUser(tx, options[0].UserID)
	if err != nil || user.Role != "admin" || !user.Enabled || user.MustChangePassword {
		return errResourceHidden
	}
	return nil
}

func (s *Store) UserResources(id, kind, relation string, offset, limit int, options ...MutationOptions) (any, error) {
	var result any
	err := s.transaction(func(tx *sql.Tx) error {
		if err := requireAdminActor(tx, options); err != nil {
			return err
		}
		if _, err := readUser(tx, id); err != nil {
			return err
		}
		var total int
		if kind == "tracks" {
			column := "owner_id"
			if relation == "uploaded" {
				column = "uploaded_by"
			} else if relation != "" && relation != "owned" {
				return errors.New("relation must be owned or uploaded")
			}
			if err := tx.QueryRow("SELECT count(*) FROM tracks WHERE "+column+"=?", id).Scan(&total); err != nil {
				return err
			}
			items, err := queryJSON[Track](tx, "SELECT payload FROM tracks WHERE "+column+"=? ORDER BY id LIMIT ? OFFSET ?", id, limit, offset)
			if err != nil {
				return err
			}
			if items == nil {
				items = []Track{}
			}
			result = map[string]any{"items": items, "total": total, "offset": offset, "limit": limit}
		} else {
			if err := tx.QueryRow("SELECT count(*) FROM playlists WHERE owner_id=? AND system=0", id).Scan(&total); err != nil {
				return err
			}
			items, err := queryJSON[Playlist](tx, "SELECT payload FROM playlists WHERE owner_id=? AND system=0 ORDER BY sort_order,id LIMIT ? OFFSET ?", id, limit, offset)
			if err != nil {
				return err
			}
			if items == nil {
				items = []Playlist{}
			}
			result = map[string]any{"items": items, "total": total, "offset": offset, "limit": limit}
		}
		return auditMutation(tx, "user.resources.read", id, nil, map[string]any{"kind": kind, "relation": relation, "offset": offset, "limit": limit}, options)
	})
	return result, err
}

func (s *Store) TransferResources(source string, request TransferRequest, options ...MutationOptions) error {
	return s.transaction(func(tx *sql.Tx) error {
		if err := requireAdminActor(tx, options); err != nil {
			return err
		}
		from, err := readUser(tx, source)
		if err != nil {
			return err
		}
		to, err := readUser(tx, request.TargetID)
		if err != nil {
			return err
		}
		if from.Enabled || !to.Enabled || source == to.ID {
			return errors.New("source must be disabled and target must be a different enabled account")
		}
		if len(options) == 0 || options[0].ExpectedVersion != from.Version || request.TargetVersion != to.Version {
			return errVersionConflict
		}
		if len(request.Tracks)+len(request.Playlists) == 0 {
			return errors.New("select resources to transfer")
		}
		if request.All {
			var tracks, playlists int
			if err := tx.QueryRow("SELECT count(*) FROM tracks WHERE owner_id=?", source).Scan(&tracks); err != nil {
				return err
			}
			if err := tx.QueryRow("SELECT count(*) FROM playlists WHERE owner_id=? AND system=0", source).Scan(&playlists); err != nil {
				return err
			}
			if tracks != len(request.Tracks) || playlists != len(request.Playlists) {
				return errVersionConflict
			}
		}
		seen := map[string]bool{}
		for _, item := range request.Tracks {
			if seen["t:"+item.ID] {
				return errors.New("duplicate track selection")
			}
			seen["t:"+item.ID] = true
			before, err := readTrackFrom(tx, item.ID, true)
			if err != nil {
				return err
			}
			if before.OwnerID != source || before.Version != item.Version {
				return errVersionConflict
			}
			after := before
			after.OwnerID = to.ID
			after.Version++
			if err := writeTrack(tx, after); err != nil {
				return err
			}
			if err := recordChange(tx, "track", after.ID, after.Removed); err != nil {
				return err
			}
			if err := auditMutation(tx, "track.transfer", after.ID, before, after, options); err != nil {
				return err
			}
		}
		var order int
		if err := tx.QueryRow("SELECT COALESCE(MAX(sort_order),0) FROM playlists WHERE owner_id=? AND system=0", to.ID).Scan(&order); err != nil {
			return err
		}
		for _, item := range request.Playlists {
			if seen["p:"+item.ID] {
				return errors.New("duplicate playlist selection")
			}
			seen["p:"+item.ID] = true
			before, err := readPlaylistFrom(tx, item.ID)
			if err != nil {
				return err
			}
			if before.System || before.OwnerID != source || before.Version != item.Version {
				return errVersionConflict
			}
			after := clonePlaylist(before)
			order++
			after.OwnerID = to.ID
			after.SortOrder = order
			after.Version++
			after.UpdatedAt = time.Now().UTC()
			if err := writePlaylist(tx, after); err != nil {
				return err
			}
			keys, err := queryJSON[APIKey](tx, "SELECT payload FROM api_keys WHERE id IN (SELECT key_id FROM key_playlist_grants WHERE playlist_id=?)", item.ID)
			if err != nil {
				return err
			}
			for _, key := range keys {
				key.AllowedPlaylistIDs = slices.DeleteFunc(key.AllowedPlaylistIDs, func(id string) bool { return id == item.ID })
				if err := saveKey(tx, key); err != nil {
					return err
				}
			}
			if err := recordChange(tx, "playlist", item.ID, false); err != nil {
				return err
			}
			if err := auditMutation(tx, "playlist.transfer", item.ID, before, after, options); err != nil {
				return err
			}
		}
		return auditMutation(tx, "user.resources.transfer", source, nil, request, options)
	})
}
