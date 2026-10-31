package music

import (
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var errUsernameTaken = errors.New("username already exists")

type AccountPatch struct {
	DisplayName               *string `json:"display_name"`
	Enabled                   *bool   `json:"enabled"`
	EmailVerificationRequired *bool   `json:"email_verification_required"`
}

func (s *Store) UpdateUser(id string, patch AccountPatch, options MutationOptions) (User, error) {
	var user User
	if patch.DisplayName == nil && patch.Enabled == nil && patch.EmailVerificationRequired == nil {
		return user, errors.New("no account changes supplied")
	}
	if patch.DisplayName != nil {
		v := strings.TrimSpace(*patch.DisplayName)
		if v == "" || !utf8.ValidString(v) || utf8.RuneCountInString(v) > 128 {
			return user, errors.New("invalid display name")
		}
		patch.DisplayName = &v
	}
	err := s.transaction(func(tx *sql.Tx) error {
		if err := validateAccountMutationSession(tx, options); err != nil {
			return err
		}
		if patch.EmailVerificationRequired != nil {
			if err := requireAdminActor(tx, []MutationOptions{options}); err != nil {
				return err
			}
		}
		if options.UserID != "" && options.ActorType != "console" {
			if patch.Enabled != nil || options.UserID != id {
				if err := requireAdminActor(tx, []MutationOptions{options}); err != nil {
					return err
				}
			} else if err := checkActorOwner(tx, id, []MutationOptions{options}); err != nil {
				return err
			}
		}
		var err error
		user, err = readUser(tx, id)
		if err != nil {
			return err
		}
		if user.DeletedAt != "" {
			return sql.ErrNoRows
		}
		if err := checkVersion(user.Version, []MutationOptions{options}); err != nil {
			return err
		}
		before := user
		emailState, err := readAccountEmail(tx, id)
		if err != nil {
			return err
		}
		emailChanged := patch.EmailVerificationRequired != nil && *patch.EmailVerificationRequired != emailState.Required
		stateChanged := patch.Enabled != nil && *patch.Enabled != user.Enabled
		if stateChanged && !*patch.Enabled && user.Role == "admin" {
			var count int
			if err := tx.QueryRow("SELECT count(*) FROM users WHERE role='admin' AND enabled=1").Scan(&count); err != nil {
				return err
			}
			if count <= 1 {
				return errors.New("cannot disable the last administrator")
			}
		}
		if patch.Enabled != nil {
			user.Enabled = *patch.Enabled
		}
		if patch.DisplayName != nil {
			user.DisplayName = *patch.DisplayName
		}
		if emailChanged {
			emailState.Required = *patch.EmailVerificationRequired
			if err := writeAccountEmail(tx, id, emailState); err != nil {
				return err
			}
		}
		applyAccountEmail(&user, emailState)
		if user.Enabled == before.Enabled && user.DisplayName == before.DisplayName && !emailChanged {
			return nil
		}
		user.Version++
		if stateChanged {
			user.AuthVersion++
		}
		if _, err := tx.Exec("UPDATE users SET enabled=?,display_name=?,auth_version=?,version=?,updated_at=? WHERE id=?", user.Enabled, user.DisplayName, user.AuthVersion, user.Version, persistedTime(time.Now()), id); err != nil {
			return err
		}
		if stateChanged {
			if _, err := tx.Exec("UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at=''", persistedTime(time.Now()), id); err != nil {
				return err
			}
			if !user.Enabled {
				if _, err := tx.Exec("UPDATE meta SET value=(SELECT id FROM users WHERE enabled=1 AND role='admin' ORDER BY created_at,id LIMIT 1) WHERE key='scan_owner_id' AND value=?", id); err != nil {
					return err
				}
			}
		}
		if err := recordUserPlaylistChanges(tx, id); err != nil {
			return err
		}
		return auditMutation(tx, "account.update", id, before, user, []MutationOptions{options})
	})
	return user, err
}

type TemporaryAccount struct {
	User              User   `json:"user"`
	TemporaryPassword string `json:"temporary_password"`
}

func (s *Store) CreateMember(username, display string, options ...MutationOptions) (TemporaryAccount, error) {
	return s.CreateMemberWithPolicy(AdminMemberCreate{Username: username, DisplayName: display}, options...)
}

type AdminMemberCreate struct {
	Username                  string `json:"username"`
	DisplayName               string `json:"display_name,omitempty"`
	QuotaBytes                *int64 `json:"quota_bytes,omitempty"`
	EmailVerificationRequired *bool  `json:"email_verification_required,omitempty"`
}

func (s *Store) CreateMemberWithPolicy(input AdminMemberCreate, options ...MutationOptions) (TemporaryAccount, error) {
	limit := int64(0)
	if input.QuotaBytes != nil {
		limit = *input.QuotaBytes
	}
	if err := validateQuotaBytes(limit); err != nil {
		return TemporaryAccount{}, err
	}
	required := true
	if input.EmailVerificationRequired != nil {
		required = *input.EmailVerificationRequired
	}
	password, err := randomToken(18)
	if err != nil {
		return TemporaryAccount{}, err
	}
	user, err := s.createUserRecordInitialized(input.Username, password, "member", input.DisplayName, true, false, options, func(tx *sql.Tx, user *User) error {
		if len(options) > 0 {
			if err := validateAccountMutationSession(tx, options[0]); err != nil {
				return err
			}
		}
		if err := writeAccountEmail(tx, user.ID, AccountEmailState{Required: required, QuotaSource: "administrator"}); err != nil {
			return err
		}
		if input.QuotaBytes != nil {
			return writeNewAccountQuota(tx, user.ID, limit)
		}
		return nil
	})
	if err != nil {
		return TemporaryAccount{}, err
	}
	return TemporaryAccount{user, password}, nil
}
func (s *Store) Users() ([]User, error) {
	rows, err := s.db.Query("SELECT id,username,display_name,role,enabled,auth_version,version,created_at,must_change_password,COALESCE((SELECT value FROM meta WHERE key='account_email/'||users.id),'') FROM users WHERE NOT EXISTS(SELECT 1 FROM meta WHERE key='deleted_user/'||users.id) ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []User{}
	for rows.Next() {
		var u User
		var created, raw string
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Enabled, &u.AuthVersion, &u.Version, &created, &u.MustChangePassword, &raw); err != nil {
			return nil, err
		}
		u.CreatedAt, _ = parsePersistedTime(created)
		state, err := decodeAccountEmail(raw)
		if err != nil {
			return nil, err
		}
		applyAccountEmail(&u, state)
		result = append(result, u)
	}
	return result, rows.Err()
}
func (s *Store) ResetMemberPassword(id string, revoke bool, options MutationOptions) (TemporaryAccount, error) {
	password, err := randomToken(18)
	if err != nil {
		return TemporaryAccount{}, err
	}
	encoded, err := passwordHash(password)
	if err != nil {
		return TemporaryAccount{}, err
	}
	var user User
	err = s.transaction(func(tx *sql.Tx) error {
		if options.UserID != "" {
			if err := requireAdminActor(tx, []MutationOptions{options}); err != nil {
				return err
			}
		}
		var err error
		user, err = readUser(tx, id)
		if err != nil {
			return err
		}
		if user.DeletedAt != "" {
			return sql.ErrNoRows
		}
		if err = checkVersion(user.Version, []MutationOptions{options}); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE users SET password_hash=?,must_change_password=1,auth_version=auth_version+1,version=version+1,updated_at=? WHERE id=?", encoded, persistedTime(time.Now()), id); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at=''", persistedTime(time.Now()), id); err != nil {
			return err
		}
		if revoke {
			if _, err := deleteAccountKeyRows(tx, id); err != nil {
				return err
			}
		}
		user.MustChangePassword = true
		user.UploadAllowed = false
		user.Version++
		user.AuthVersion++
		return auditMutation(tx, "account.reset_password", id, nil, map[string]bool{"must_change_password": true, "revoke_keys": revoke}, []MutationOptions{options})
	})
	if err != nil {
		return TemporaryAccount{}, err
	}
	return TemporaryAccount{user, password}, nil
}
