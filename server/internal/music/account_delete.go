package music

import (
	"database/sql"
	"errors"
	"time"
)

var errAccountDeleteSelf = errors.New("cannot delete the current administrator")
var errAccountNotDisabled = errors.New("disable the account before deleting it")
var errAccountHasResources = errors.New("transfer all managed tracks and playlists before deleting the account")

// Retain the user row for uploader foreign keys; a durable tombstone prevents
// all account management paths from resurrecting the identity without a schema change.
func (s *Store) DeleteUser(id string, options MutationOptions) error {
	return s.transaction(func(tx *sql.Tx) error {
		if err := requireAdminActor(tx, []MutationOptions{options}); err != nil {
			return err
		}
		if id == options.UserID {
			return errAccountDeleteSelf
		}
		user, err := readUser(tx, id)
		if err != nil {
			return err
		}
		if user.DeletedAt != "" {
			return sql.ErrNoRows
		}
		if options.ExpectedVersion < 1 || options.ExpectedVersion != user.Version {
			return errVersionConflict
		}
		if user.Enabled {
			return errAccountNotDisabled
		}
		var resources bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM tracks WHERE owner_id=?) OR EXISTS(SELECT 1 FROM playlists WHERE owner_id=? AND system=0)", id, id).Scan(&resources); err != nil {
			return err
		}
		if resources {
			return errAccountHasResources
		}
		before := user
		deletedKeys, err := deleteAccountKeyRows(tx, id)
		if err != nil {
			return err
		}
		for _, query := range []string{
			"DELETE FROM sessions WHERE user_id=?",
			"DELETE FROM subscriptions WHERE user_id=?",
		} {
			if _, err := tx.Exec(query, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("DELETE FROM playlist_invitations WHERE inviter_id=? OR recipient_id=?", id, id); err != nil {
			return err
		}
		if err := deleteAccountTagRows(tx, id); err != nil {
			return err
		}
		user.DeletedAt = persistedTime(time.Now())
		user.AuthVersion++
		user.Version++
		user.MustChangePassword = false
		if _, err := tx.Exec("UPDATE users SET enabled=0,password_hash='',must_change_password=0,auth_version=?,version=?,updated_at=? WHERE id=?", user.AuthVersion, user.Version, user.DeletedAt, id); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO meta(key,value) VALUES(?,?)", "deleted_user/"+id, user.DeletedAt); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE meta SET value=? WHERE key='scan_owner_id' AND value=?", options.UserID, id); err != nil {
			return err
		}
		return auditMutation(tx, "account.delete", id, before, map[string]any{"user": user, "deleted_keys": deletedKeys}, []MutationOptions{options})
	})
}

// Schema 7 has no tag tables. Account tombstones retain uploader references,
// so explicitly remove the private scope instead of relying on users DELETE.
func deleteAccountTagRows(tx *sql.Tx, userID string) error {
	for _, table := range []string{"tag_scopes", "saved_track_filters", "tag_batches"} {
		var exists bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)", table).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		query := "DELETE FROM tag_scopes WHERE user_id=?"
		if table == "saved_track_filters" {
			query = "DELETE FROM saved_track_filters WHERE user_id=?"
		}
		if table == "tag_batches" {
			query = "DELETE FROM tag_batches WHERE user_id=?"
		}
		if _, err := tx.Exec(query, userID); err != nil {
			return err
		}
	}
	return nil
}
