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
	DisplayName *string `json:"display_name"`
	Enabled     *bool   `json:"enabled"`
}

func (s *Store) UpdateUser(id string, patch AccountPatch, options MutationOptions) (User, error) {
	var user User
	if patch.DisplayName == nil && patch.Enabled == nil {
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
		var err error
		user, err = readUser(tx, id)
		if err != nil {
			return err
		}
		if err := checkVersion(user.Version, []MutationOptions{options}); err != nil {
			return err
		}
		before := user
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
		if user.Enabled == before.Enabled && user.DisplayName == before.DisplayName {
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
	password, err := randomToken(18)
	if err != nil {
		return TemporaryAccount{}, err
	}
	user, err := s.createUserRecord(username, password, "member", display, true, false, options)
	if err != nil {
		return TemporaryAccount{}, err
	}
	return TemporaryAccount{user, password}, nil
}
func (s *Store) Users() ([]User, error) {
	rows, err := s.db.Query("SELECT id,username,display_name,role,enabled,auth_version,version,created_at,must_change_password FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []User{}
	for rows.Next() {
		var u User
		var created string
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Enabled, &u.AuthVersion, &u.Version, &created, &u.MustChangePassword); err != nil {
			return nil, err
		}
		u.CreatedAt, _ = parsePersistedTime(created)
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
		var err error
		user, err = readUser(tx, id)
		if err != nil {
			return err
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
			keys, err := queryJSON[APIKey](tx, "SELECT payload FROM api_keys WHERE user_id=?", id)
			if err != nil {
				return err
			}
			for _, key := range keys {
				key.RevokedAt = time.Now().UTC()
				if err := saveKey(tx, key); err != nil {
					return err
				}
			}
		}
		user.MustChangePassword = true
		user.Version++
		user.AuthVersion++
		return auditMutation(tx, "account.reset_password", id, nil, map[string]bool{"must_change_password": true, "revoke_keys": revoke}, []MutationOptions{options})
	})
	if err != nil {
		return TemporaryAccount{}, err
	}
	return TemporaryAccount{user, password}, nil
}
