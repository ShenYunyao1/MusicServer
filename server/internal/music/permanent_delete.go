package music

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var errTrackNotRemoved = errors.New("请先下架曲目，再永久删除")

// PurgeTrack serializes with scanning/import and quarantines the audio until
// the database commit succeeds. A crash is reconciled at the next store open.
func (s *Store) PurgeTrack(id string, options MutationOptions) error {
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	var original, trash string
	moved := false
	err = s.transaction(func(tx *sql.Tx) error {
		if err := requireAdminActor(tx, []MutationOptions{options}); err != nil {
			return err
		}
		if err := validateAccountMutationSession(tx, options); err != nil {
			return err
		}
		track, err := readTrackFrom(tx, id, true)
		if err != nil {
			return err
		}
		if options.ExpectedVersion < 1 || options.ExpectedVersion != track.Version {
			return errVersionConflict
		}
		if !track.Removed {
			return errTrackNotRemoved
		}
		if !filepath.IsLocal(filepath.FromSlash(track.Path)) {
			return errors.New("invalid media path")
		}
		playlists, err := queryJSON[Playlist](tx, "SELECT p.payload FROM playlists p JOIN playlist_tracks m ON m.playlist_id=p.id WHERE m.track_id=?", id)
		if err != nil {
			return err
		}
		for _, p := range playlists {
			p.TrackIDs = slices.DeleteFunc(p.TrackIDs, func(v string) bool { return v == id })
			delete(p.TrackNotes, id)
			p.Version++
			p.UpdatedAt = time.Now().UTC()
			if err := writePlaylist(tx, p); err != nil {
				return err
			}
			if err := recordChange(tx, "playlist", p.ID, false); err != nil {
				return err
			}
		}
		for _, q := range []string{"DELETE FROM subscription_notes WHERE track_id=?", "DELETE FROM metadata_overrides WHERE track_id=?", "DELETE FROM scan_stamps WHERE track_id=?", "DELETE FROM tracks WHERE id=?"} {
			if _, err := tx.Exec(q, id); err != nil {
				return err
			}
		}
		if err := recordChange(tx, "track", id, true); err != nil {
			return err
		}
		if err := auditMutation(tx, "track.purge", id, track, nil, []MutationOptions{options}); err != nil {
			return err
		}
		original = filepath.FromSlash(track.Path)
		trash = filepath.Join(".staging", "purge-"+id)
		if err := root.MkdirAll(".staging", 0700); err != nil {
			return err
		}
		// Never overwrite an unresolved recovery file.
		if _, err := root.Lstat(trash); err == nil {
			return errors.New("pending media deletion requires recovery")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if info, err := root.Lstat(original); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		} else if !info.Mode().IsRegular() {
			return errors.New("media is not a regular file")
		}
		if err := root.Rename(original, trash); err != nil {
			return err
		}
		moved = true
		return nil
	})
	if moved {
		if err != nil {
			return errors.Join(err, root.Rename(trash, original))
		}
		// A failed unlink is safe: startup retries; this path is never scanned.
		_ = root.Remove(trash)
	}
	return err
}

func (s *Store) recoverPurgedMedia() error {
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".staging")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "purge-") {
			continue
		}
		if !entry.Type().IsRegular() {
			return errors.New("invalid purge recovery file")
		}
		id := strings.TrimPrefix(entry.Name(), "purge-")
		path := filepath.Join(".staging", entry.Name())
		track, err := readTrackFrom(s.db, id, true)
		if errors.Is(err, sql.ErrNoRows) {
			if err := root.Remove(path); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(filepath.FromSlash(track.Path)) {
			return errors.New("invalid recovery path")
		}
		if _, err := root.Lstat(filepath.FromSlash(track.Path)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("recovery destination already exists or is inaccessible")
		}
		if err := root.Rename(path, filepath.FromSlash(track.Path)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) PurgeUser(id string, options MutationOptions) error {
	return s.transaction(func(tx *sql.Tx) error {
		if err := requireAdminActor(tx, []MutationOptions{options}); err != nil {
			return err
		}
		if err := validateAccountMutationSession(tx, options); err != nil {
			return err
		}
		if id == options.UserID {
			return errAccountDeleteSelf
		}
		user, err := readUser(tx, id)
		if err != nil {
			return err
		}
		if options.ExpectedVersion < 1 || options.ExpectedVersion != user.Version {
			return errVersionConflict
		}
		if user.Enabled {
			return errAccountNotDisabled
		}
		var assets bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM tracks WHERE owner_id=? OR uploaded_by=?) OR EXISTS(SELECT 1 FROM playlists WHERE owner_id=?) OR EXISTS(SELECT 1 FROM upload_staging WHERE user_id=?) OR EXISTS(SELECT 1 FROM meta WHERE key LIKE 'upload_resume/%' AND json_extract(value,'$.owner')=? AND json_extract(value,'$.state') NOT IN ('ready','canceled'))`, id, id, id, id, id).Scan(&assets); err != nil {
			return err
		}
		if assets {
			return errAccountHasResources
		}
		if _, err := deleteAccountKeyRows(tx, id); err != nil {
			return err
		}
		if err := deleteAccountTagRows(tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM meta WHERE key LIKE 'upload_receipt/%' AND json_extract(value,'$.owner')=?", id); err != nil {
			return err
		}
		// Preserve shared contributions while releasing their author foreign keys.
		for _, table := range []string{"track_tags", "track_tag_overrides"} {
			var exists bool
			if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)", table).Scan(&exists); err != nil {
				return err
			}
			if exists {
				column := "created_by"
				if table == "track_tag_overrides" {
					column = "updated_by"
				}
				if _, err := tx.Exec("UPDATE "+table+" SET "+column+"=? WHERE "+column+"=?", options.UserID, id); err != nil {
					return err
				}
			}
		}
		for _, q := range []string{"DELETE FROM sessions WHERE user_id=?", "DELETE FROM subscriptions WHERE user_id=?", "DELETE FROM playlist_invitations WHERE inviter_id=?1 OR recipient_id=?1", "DELETE FROM meta WHERE key LIKE 'upload_resume/%' AND json_extract(value,'$.owner')=?", "DELETE FROM meta WHERE key LIKE 'email_binding/%' AND value=?", "DELETE FROM meta WHERE key IN ('user_avatar/'||?1,'deleted_user/'||?1,'account_email/'||?1) OR ((key LIKE 'email_challenge/%' OR key LIKE 'mail_job/%') AND json_extract(value,'$.user_id')=?1)", "DELETE FROM settings WHERE key='storage_quota/'||?", "DELETE FROM users WHERE id=?"} {
			if _, err := tx.Exec(q, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("UPDATE meta SET value=? WHERE key='scan_owner_id' AND value=?", options.UserID, id); err != nil {
			return err
		}
		return auditMutation(tx, "account.purge", id, user, nil, []MutationOptions{options})
	})
}
