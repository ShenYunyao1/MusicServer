package music

import (
	"database/sql"
	"errors"
	"os"
	"strings"
)

// A disabled account's unconfirmed uploads are discardable, not permanent
// ownership assets. Serialize with stage publication and account re-enabling.
func (s *Store) discardAccountTemporary(id string, options MutationOptions) error {
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
	var discarded []string
	err := s.transaction(func(tx *sql.Tx) error {
		if err := requireAdminActor(tx, []MutationOptions{options}); err != nil {
			return err
		}
		if err := validateAccountMutationSession(tx, options); err != nil {
			return err
		}
		user, err := readUser(tx, id)
		if err != nil {
			return err
		}
		if user.Enabled {
			return errAccountNotDisabled
		}
		if user.Version != options.ExpectedVersion {
			return errVersionConflict
		}
		var permanent bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM tracks WHERE owner_id=? OR uploaded_by=?) OR EXISTS(SELECT 1 FROM playlists WHERE owner_id=?)", id, id, id).Scan(&permanent); err != nil {
			return err
		}
		if permanent {
			return errAccountHasResources
		}
		rows, err := tx.Query("SELECT id,temp_path FROM upload_staging WHERE user_id=?", id)
		if err != nil {
			return err
		}
		type staged struct{ id, path string }
		var stages []staged
		for rows.Next() {
			var stage staged
			if err := rows.Scan(&stage.id, &stage.path); err != nil {
				rows.Close()
				return err
			}
			stages = append(stages, stage)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, stage := range stages {
			if !validStagePath(stage.path) || s.activeStages[stage.path] {
				return errAccountHasResources
			}
		}
		for _, stage := range stages {
			if _, err := tx.Exec("DELETE FROM upload_staging WHERE id=? AND user_id=?", stage.id, id); err != nil {
				return err
			}
			discarded = append(discarded, stage.path)
		}
		rows, err = tx.Query("SELECT key FROM meta WHERE key LIKE 'upload_resume/%' AND CASE WHEN json_valid(value) THEN json_extract(value,'$.owner') END=?", id)
		if err != nil {
			return err
		}
		var invalid []string
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			if !validResumeID(strings.TrimPrefix(key, resumableMetaPrefix)) {
				invalid = append(invalid, key)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, key := range invalid {
			if _, err := tx.Exec("DELETE FROM meta WHERE key=?", key); err != nil {
				return err
			}
		}
		if len(stages) > 0 || len(invalid) > 0 {
			return auditMutation(tx, "account.temporary_discard", id, nil, map[string]int{"previews": len(stages), "invalid_tasks": len(invalid)}, []MutationOptions{options})
		}
		return nil
	})
	if err != nil || len(discarded) == 0 {
		return err
	}
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	var failures []error
	for _, path := range discarded {
		if err := root.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err)
		}
	}
	// The ordinary stage sweep recognizes generated orphan names and retries a
	// post-commit filesystem failure; a SQL rollback never removes live bytes.
	if len(failures) == 0 {
		return nil
	}
	// The discard audit was committed with the row deletions; record that the
	// filesystem half is still pending so the audit does not claim full cleanup.
	compensation := s.transaction(func(tx *sql.Tx) error {
		return auditMutation(tx, "account.temporary_discard_incomplete", id, nil, map[string]int{"files": len(failures)}, []MutationOptions{options})
	})
	return errors.Join(append(failures, compensation)...)
}
