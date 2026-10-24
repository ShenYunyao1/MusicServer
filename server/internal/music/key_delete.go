package music

import (
	"database/sql"
	"encoding/json"
)

func deleteAccountKeyRows(tx *sql.Tx, userID string) (int64, error) {
	if _, err := tx.Exec("DELETE FROM api_call_stats WHERE key_id IN (SELECT id FROM api_keys WHERE user_id=?)", userID); err != nil {
		return 0, err
	}
	result, err := tx.Exec("DELETE FROM api_keys WHERE user_id=?", userID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Revocation is permanent deletion, including grants, scopes and statistics.
func (s *Store) RevokeAPIKey(id string, options ...MutationOptions) error {
	return s.transaction(func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRow("SELECT payload FROM api_keys WHERE id=?", id).Scan(&raw); err != nil {
			return err
		}
		var key APIKey
		if err := json.Unmarshal([]byte(raw), &key); err != nil {
			return err
		}
		if len(options) > 0 && options[0].UserID != "" && options[0].UserID != key.UserID {
			return errResourceHidden
		}
		if err := checkActorOwner(tx, key.UserID, options); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM api_call_stats WHERE key_id=?", id); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM api_keys WHERE id=?", id); err != nil {
			return err
		}
		return auditMutation(tx, "key.delete", id, publicAPIKey(key), nil, options)
	})
}
