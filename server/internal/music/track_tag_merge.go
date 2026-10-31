package music

import (
	"database/sql"
	"errors"
	"time"
)

const maxTagMergeTracks = 5000

type TagMergeResult struct {
	Source         Tag `json:"source"`
	Target         Tag `json:"target"`
	AffectedTracks int `json:"affected_tracks"`
}

type mergeTagLink struct {
	exists                   bool
	source, creator, created string
}

func readMergeTagLink(tx *sql.Tx, trackID, tagID string) (mergeTagLink, error) {
	var link mergeTagLink
	err := tx.QueryRow("SELECT source,created_by,created_at FROM track_tags WHERE track_id=? AND tag_id=?", trackID, tagID).Scan(&link.source, &link.creator, &link.created)
	if errors.Is(err, sql.ErrNoRows) {
		return link, nil
	}
	link.exists = err == nil
	return link, err
}

func readMergeOverride(tx *sql.Tx, trackID, tagID string) (string, error) {
	var action string
	err := tx.QueryRow("SELECT action FROM track_tag_overrides WHERE track_id=? AND tag_id=?", trackID, tagID).Scan(&action)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return action, err
}

func mergeTrackTag(tx *sql.Tx, auth authContext, trackID string, source, target Tag, now string) (bool, error) {
	left, err := readMergeTagLink(tx, trackID, source.ID)
	if err != nil {
		return false, err
	}
	right, err := readMergeTagLink(tx, trackID, target.ID)
	if err != nil {
		return false, err
	}
	lo, err := readMergeOverride(tx, trackID, source.ID)
	if err != nil {
		return false, err
	}
	ro, err := readMergeOverride(tx, trackID, target.ID)
	if err != nil {
		return false, err
	}
	leftManual := left.exists && left.source == "manual" && lo != "suppressed"
	rightManual := right.exists && right.source == "manual" && ro != "suppressed"
	confirmed := lo == "confirmed" || ro == "confirmed" || leftManual || rightManual
	suppressed := !confirmed && (lo == "suppressed" || ro == "suppressed")
	desired := right
	if !desired.exists && left.exists {
		desired = left
	}
	if leftManual && !rightManual {
		desired = left
	}
	if confirmed && !desired.exists {
		desired = mergeTagLink{exists: true, source: "manual", creator: auth.userID, created: now}
	}
	if suppressed {
		desired = mergeTagLink{}
	}
	desiredAction := ""
	if source.scopeID == "public" {
		if confirmed {
			desiredAction = "confirmed"
		} else if suppressed {
			desiredAction = "suppressed"
		}
	}
	changed := left.exists || right.exists != desired.exists || right.exists && desired.exists && right.source != desired.source || ro != desiredAction
	if _, err = tx.Exec("DELETE FROM track_tags WHERE track_id=? AND tag_id=?", trackID, source.ID); err != nil {
		return false, err
	}
	if _, err = tx.Exec("DELETE FROM track_tag_overrides WHERE track_id=? AND tag_id=?", trackID, source.ID); err != nil {
		return false, err
	}
	if desired.exists {
		if _, err = tx.Exec("INSERT INTO track_tags(track_id,tag_id,scope_id,source,created_by,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(track_id,tag_id) DO UPDATE SET source=excluded.source,created_by=excluded.created_by,created_at=excluded.created_at", trackID, target.ID, target.scopeID, desired.source, desired.creator, desired.created); err != nil {
			return false, err
		}
	} else if _, err = tx.Exec("DELETE FROM track_tags WHERE track_id=? AND tag_id=?", trackID, target.ID); err != nil {
		return false, err
	}
	if desiredAction != "" {
		if ro != desiredAction {
			if _, err = tx.Exec("INSERT INTO track_tag_overrides(track_id,tag_id,scope_id,action,updated_by,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(track_id,tag_id) DO UPDATE SET action=excluded.action,updated_by=excluded.updated_by,updated_at=excluded.updated_at", trackID, target.ID, target.scopeID, desiredAction, auth.userID, now); err != nil {
				return false, err
			}
		}
	} else if ro != "" {
		if _, err = tx.Exec("DELETE FROM track_tag_overrides WHERE track_id=? AND tag_id=?", trackID, target.ID); err != nil {
			return false, err
		}
	}
	return changed, nil
}

func (s *Store) MergeTags(auth authContext, sourceID, targetID string, sourceVersion, targetVersion int64) (TagMergeResult, error) {
	var result TagMergeResult
	public := false
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		if err = requireTagsSchema(tx); err != nil {
			return err
		}
		source, err := readTag(tx, sourceID)
		if err != nil {
			return err
		}
		if err = authorizeTag(actor, source, true); err != nil {
			return err
		}
		target, err := readTag(tx, targetID)
		if err != nil {
			return err
		}
		if err = authorizeTag(actor, target, true); err != nil {
			return err
		}
		if source.scopeID != target.scopeID || source.GroupID != target.GroupID || source.ID == target.ID {
			return errTagInvalid
		}
		if sourceVersion < 1 || targetVersion < 1 || sourceVersion != source.Version || targetVersion != target.Version {
			return errVersionConflict
		}
		if source.Deleted || target.Deleted || source.MergedInto != "" || target.MergedInto != "" {
			return errTagInvalid
		}
		if used, err := tagUsedByActiveBatch(tx, sourceID); err != nil {
			return err
		} else if used {
			return errTagInUse
		}
		var terms int
		if err = tx.QueryRow("SELECT count(*) FROM tag_aliases WHERE tag_id=? OR tag_id=?", sourceID, targetID).Scan(&terms); err != nil {
			return err
		}
		if terms-1 > tagAliasLimit {
			return errTagLimit
		}
		rows, err := tx.Query("SELECT track_id FROM track_tags WHERE tag_id=? UNION SELECT track_id FROM track_tag_overrides WHERE tag_id=? LIMIT ?", sourceID, sourceID, maxTagMergeTracks+1)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) > maxTagMergeTracks {
			return errTagLimit
		}
		now := persistedTime(time.Now())
		public = source.scopeID == "public"
		for _, id := range ids {
			changed, e := mergeTrackTag(tx, auth, id, source, target, now)
			if e != nil {
				return e
			}
			if !changed {
				continue
			}
			if err = bumpTrackTagVersion(tx, id, source.scopeID); err != nil {
				return err
			}
			if public {
				track, e := readTrackFrom(tx, id, true)
				if e != nil {
					return e
				}
				track.Version++
				if err = writeTrack(tx, track); err != nil {
					return err
				}
				if err = recordChange(tx, "track", id, track.Removed); err != nil {
					return err
				}
			}
			result.AffectedTracks++
		}
		// The old primary spelling becomes an alias; the namespace is never dropped.
		if _, err = tx.Exec("UPDATE tag_aliases SET tag_id=?,is_primary=0 WHERE tag_id=?", targetID, sourceID); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE tags SET deleted=1,merged_into=?,version=version+1,updated_at=? WHERE id=?", targetID, now, sourceID); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE tags SET merged_into=?,version=version+1,updated_at=? WHERE merged_into=?", targetID, now, sourceID); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE tags SET version=version+1,updated_at=? WHERE id=?", now, targetID); err != nil {
			return err
		}
		if err = bumpTagScope(tx, source.scopeID); err != nil {
			return err
		}
		if result.Source, err = readTag(tx, sourceID); err != nil {
			return err
		}
		if result.Target, err = readTag(tx, targetID); err != nil {
			return err
		}
		return auditTagChange(tx, auth, source.scopeID, "tag.merge", sourceID, map[string]Tag{"source": source, "target": target}, result)
	})
	if err == nil && public && result.AffectedTracks > 0 {
		s.notifyCommunityCommit()
	}
	return result, err
}
