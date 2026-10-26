package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const accountEmailPrefix = "account_email/"

var errEmailVerificationRequired = errors.New("请先在个人中心验证邮箱，再上传曲目")

// Existing schema 7 already supports extensible account metadata. Keeping the
// policy in meta avoids an implicit DDL change or destroying existing accounts.
type AccountEmailState struct {
	Required                  bool   `json:"required"`
	Email                     string `json:"email,omitempty"`
	VerifiedAt                string `json:"verified_at,omitempty"`
	QuotaSource               string `json:"quota_source"`
	RegistrationQuotaBytes    int64  `json:"registration_quota_bytes,omitempty"`
	RegistrationPolicyVersion int64  `json:"registration_policy_version,omitempty"`
}

func decodeAccountEmail(raw string) (AccountEmailState, error) {
	state := AccountEmailState{Required: true, QuotaSource: "existing"}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return state, err
		}
	}
	return state, nil
}

func readAccountEmail(reader sqlReader, id string) (AccountEmailState, error) {
	var raw string
	err := reader.QueryRow("SELECT value FROM meta WHERE key=?", accountEmailPrefix+id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return decodeAccountEmail("")
	}
	if err != nil {
		return AccountEmailState{}, err
	}
	return decodeAccountEmail(raw)
}

func writeAccountEmail(tx *sql.Tx, id string, state AccountEmailState) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", accountEmailPrefix+id, string(encoded))
	return err
}

func accountEmailVerified(state AccountEmailState) bool {
	verified, err := parsePersistedTime(state.VerifiedAt)
	return state.Email != "" && err == nil && !verified.IsZero()
}

func applyAccountEmail(user *User, state AccountEmailState) {
	user.Email, user.EmailVerificationRequired = state.Email, state.Required
	user.EmailVerified = accountEmailVerified(state)
	user.UploadAllowed = user.Enabled && user.DeletedAt == "" && !user.MustChangePassword && (!state.Required || user.EmailVerified)
}

func (s *Store) InitializeEmailPolicy() error {
	return s.transaction(func(tx *sql.Tx) error {
		var initialized bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM meta WHERE key='email_policy_initialized')").Scan(&initialized); err != nil {
			return err
		}
		if initialized {
			return nil
		}
		rows, err := tx.Query("SELECT id FROM users WHERE NOT EXISTS(SELECT 1 FROM meta WHERE key='deleted_user/'||users.id) AND NOT EXISTS(SELECT 1 FROM meta WHERE key='account_email/'||users.id)")
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
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
		for _, id := range ids {
			if err := writeAccountEmail(tx, id, AccountEmailState{Required: true, QuotaSource: "existing"}); err != nil {
				return err
			}
		}
		_, err = tx.Exec("INSERT INTO meta(key,value) VALUES('email_policy_initialized',?)", persistedTime(time.Now()))
		return err
	})
}

func checkUploadEmailPolicy(reader sqlReader, userID string) error {
	user, err := readUser(reader, userID)
	if err != nil || !user.Enabled || user.DeletedAt != "" || user.MustChangePassword {
		return errResourceHidden
	}
	if !user.UploadAllowed {
		return errEmailVerificationRequired
	}
	return nil
}

// Long requests must revalidate persisted sessions after consuming the body.
func validateAccountSession(reader sqlReader, auth authContext) error {
	if !auth.session {
		return errResourceHidden
	}
	user, err := readUser(reader, auth.userID)
	if err != nil || !user.Enabled || user.DeletedAt != "" || user.MustChangePassword || user.AuthVersion != auth.authVersion {
		return errResourceHidden
	}
	var valid bool
	now := time.Now()
	if err := reader.QueryRow("SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND user_id=? AND auth_version=? AND revoked_at='' AND expires_at>? AND last_active_at>?)", auth.keyID, auth.userID, auth.authVersion, persistedTime(now), persistedTime(now.Add(-sessionIdle))).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return errResourceHidden
	}
	return nil
}

func validateQuotaBytes(value int64) error {
	if value < 0 || value > 1<<50 {
		return errors.New("账户额度范围为 0–1 PiB，0 为不限额")
	}
	return nil
}

func validateAccountMutationSession(reader sqlReader, options MutationOptions) error {
	if options.ActorType != "session" {
		return nil
	}
	user, err := readUser(reader, options.UserID)
	if err != nil {
		return errResourceHidden
	}
	return validateAccountSession(reader, authContext{session: true, userID: user.ID, keyID: options.CredentialID, authVersion: user.AuthVersion, role: user.Role})
}

func writeNewAccountQuota(tx *sql.Tx, id string, limit int64) error {
	if err := validateQuotaBytes(limit); err != nil {
		return err
	}
	raw, err := json.Marshal(StorageQuota{LimitBytes: limit, Version: 1})
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO settings(key,value) VALUES(?,?)", "storage_quota/"+id, string(raw))
	return err
}
