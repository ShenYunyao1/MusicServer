package music

import (
	"database/sql"
	"errors"
	"time"
)

const maxTrackTags = 32

type TrackTagsView struct {
	Public          []Tag `json:"public"`
	Personal        []Tag `json:"personal"`
	PublicVersion   int64 `json:"public_version"`
	PersonalVersion int64 `json:"personal_version"`
}

func trackTagVersion(reader sqlReader, trackID, scopeID string) (int64, error) {
	var version int64
	err := reader.QueryRow("SELECT version FROM track_tag_states WHERE track_id=? AND scope_id=?", trackID, scopeID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 1, nil
	}
	return version, err
}

func bumpTrackTagVersion(tx *sql.Tx, trackID, scopeID string) error {
	_, err := tx.Exec("INSERT INTO track_tag_states(track_id,scope_id,version) VALUES(?,?,2) ON CONFLICT(track_id,scope_id) DO UPDATE SET version=version+1", trackID, scopeID)
	return err
}

func trackTagsInScope(reader sqlReader, trackID, scopeID string) ([]Tag, error) {
	rows, err := reader.Query(`SELECT tags.id FROM track_tags l JOIN tags ON tags.id=l.tag_id AND tags.scope_id=l.scope_id WHERE l.track_id=? AND l.scope_id=? AND tags.deleted=0 AND NOT EXISTS(SELECT 1 FROM track_tag_overrides o WHERE o.track_id=l.track_id AND o.tag_id=l.tag_id AND o.scope_id=l.scope_id AND o.action='suppressed') ORDER BY tags.group_id,tags.normalized_name,tags.id`, trackID, scopeID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
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
	items := []Tag{}
	for _, id := range ids {
		tag, err := readTag(reader, id)
		if err != nil {
			return nil, err
		}
		items = append(items, tag)
	}
	return items, nil
}

func trackTagsView(reader sqlReader, trackID, userID string) (TrackTagsView, error) {
	v := TrackTagsView{}
	var err error
	if v.Public, err = trackTagsInScope(reader, trackID, "public"); err != nil {
		return v, err
	}
	if v.Personal, err = trackTagsInScope(reader, trackID, userID); err != nil {
		return v, err
	}
	if v.PublicVersion, err = trackTagVersion(reader, trackID, "public"); err != nil {
		return v, err
	}
	v.PersonalVersion, err = trackTagVersion(reader, trackID, userID)
	return v, err
}

func (s *Store) GetTrackTags(auth authContext, trackID string) (TrackTagsView, error) {
	var view TrackTagsView
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		if err = requireTagsSchema(tx); err != nil {
			return err
		}
		if _, err = readTrackFrom(tx, trackID, false); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errResourceHidden
			}
			return err
		}
		view, err = trackTagsView(tx, trackID, actor.ID)
		return err
	})
	return view, err
}

func validateTrackTagIDs(addIDs, removeIDs []string) error {
	if len(addIDs) > maxTrackTags || len(removeIDs) > maxTrackTags {
		return errTagLimit
	}
	seen := map[string]bool{}
	for _, ids := range [][]string{addIDs, removeIDs} {
		for _, id := range ids {
			if id == "" || seen[id] {
				return errTagInvalid
			}
			seen[id] = true
		}
	}
	return nil
}

// changeTrackTagsTx is shared by single edits, upload commits and bounded batch
// tasks. The caller owns the transaction and notifies only after committing it.
func changeTrackTagsTx(tx *sql.Tx, auth authContext, trackID, scope string, expectedVersion int64, addIDs, removeIDs []string) (TrackTagsView, bool, error) {
	var view TrackTagsView
	changed := false
	public := scope == "" || scope == "public"
	err := func() error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		if err = requireTagsSchema(tx); err != nil {
			return err
		}
		track, err := readTrackFrom(tx, trackID, false)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errResourceHidden
			}
			return err
		}
		scopeID, err := tagScopeID(actor.ID, scope)
		if err != nil {
			return err
		}
		if err = validateTrackTagIDs(addIDs, removeIDs); err != nil {
			return err
		}
		for _, ids := range [][]string{addIDs, removeIDs} {
			for _, id := range ids {
				tag, e := readTag(tx, id)
				if e != nil {
					if errors.Is(e, sql.ErrNoRows) {
						return errResourceHidden
					}
					return e
				}
				// All active members may bind public terms; personal scope stays isolated.
				if e = authorizeTag(actor, tag, false); e != nil {
					return e
				}
				if tag.scopeID != scopeID || tag.Deleted {
					return errResourceHidden
				}
			}
		}
		version, err := trackTagVersion(tx, trackID, scopeID)
		if err != nil {
			return err
		}
		if expectedVersion < 1 || expectedVersion != version {
			return errVersionConflict
		}
		before, err := trackTagsInScope(tx, trackID, scopeID)
		if err != nil {
			return err
		}
		now := persistedTime(time.Now())
		for _, id := range addIDs {
			var source, override string
			e := tx.QueryRow("SELECT source FROM track_tags WHERE track_id=? AND tag_id=? AND scope_id=?", trackID, id, scopeID).Scan(&source)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if public {
				e = tx.QueryRow("SELECT action FROM track_tag_overrides WHERE track_id=? AND tag_id=? AND scope_id=?", trackID, id, scopeID).Scan(&override)
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return e
				}
			}
			if source == "manual" && (!public || override == "confirmed") {
				continue
			}
			if scopeID != "public" {
				if err = ensureTagScope(tx, actor.ID); err != nil {
					return err
				}
			}
			if _, err = tx.Exec("INSERT INTO track_tags(track_id,tag_id,scope_id,source,created_by,created_at) VALUES(?,?,?,'manual',?,?) ON CONFLICT(track_id,tag_id) DO UPDATE SET source='manual',created_by=excluded.created_by,created_at=excluded.created_at", trackID, id, scopeID, actor.ID, now); err != nil {
				return err
			}
			if public {
				if _, err = tx.Exec("INSERT INTO track_tag_overrides(track_id,tag_id,scope_id,action,updated_by,updated_at) VALUES(?,?,?,'confirmed',?,?) ON CONFLICT(track_id,tag_id) DO UPDATE SET action='confirmed',updated_by=excluded.updated_by,updated_at=excluded.updated_at", trackID, id, scopeID, actor.ID, now); err != nil {
					return err
				}
			}
			changed = true
		}
		for _, id := range removeIDs {
			var exists bool
			if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM track_tags WHERE track_id=? AND tag_id=? AND scope_id=?)", trackID, id, scopeID).Scan(&exists); err != nil {
				return err
			}
			var override string
			if public {
				e := tx.QueryRow("SELECT action FROM track_tag_overrides WHERE track_id=? AND tag_id=? AND scope_id=?", trackID, id, scopeID).Scan(&override)
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return e
				}
			}
			if !exists && (!public || override == "suppressed") {
				continue
			}
			if _, err = tx.Exec("DELETE FROM track_tags WHERE track_id=? AND tag_id=? AND scope_id=?", trackID, id, scopeID); err != nil {
				return err
			}
			if public {
				if _, err = tx.Exec("INSERT INTO track_tag_overrides(track_id,tag_id,scope_id,action,updated_by,updated_at) VALUES(?,?,?,'suppressed',?,?) ON CONFLICT(track_id,tag_id) DO UPDATE SET action='suppressed',updated_by=excluded.updated_by,updated_at=excluded.updated_at", trackID, id, scopeID, actor.ID, now); err != nil {
					return err
				}
			}
			changed = true
		}
		var count int
		if err = tx.QueryRow("SELECT count(*) FROM track_tags WHERE track_id=? AND scope_id=?", trackID, scopeID).Scan(&count); err != nil {
			return err
		}
		if count > maxTrackTags {
			return errTagLimit
		}
		if changed {
			if err = bumpTrackTagVersion(tx, trackID, scopeID); err != nil {
				return err
			}
			if err = bumpTagScope(tx, scopeID); err != nil {
				return err
			}
			if public {
				track.Version++
				if err = writeTrack(tx, track); err != nil {
					return err
				}
				if err = recordChange(tx, "track", trackID, false); err != nil {
					return err
				}
			}
			after, e := trackTagsInScope(tx, trackID, scopeID)
			if e != nil {
				return e
			}
			if err = auditTagChange(tx, auth, scopeID, "track.tags.update", trackID, before, after); err != nil {
				return err
			}
		}
		view, err = trackTagsView(tx, trackID, actor.ID)
		return err
	}()
	return view, changed, err
}

func (s *Store) ChangeTrackTags(auth authContext, trackID, scope string, expectedVersion int64, addIDs, removeIDs []string) (TrackTagsView, error) {
	var view TrackTagsView
	var changed bool
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		var err error
		view, changed, err = changeTrackTagsTx(tx, auth, trackID, scope, expectedVersion, addIDs, removeIDs)
		return err
	})
	if err == nil && changed && (scope == "" || scope == "public") {
		s.notifyCommunityCommit()
	}
	return view, err
}
