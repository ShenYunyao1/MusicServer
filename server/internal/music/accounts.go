package music

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

type User struct {
	ID                 string    `json:"id"`
	Username           string    `json:"username"`
	DisplayName        string    `json:"display_name"`
	Role               string    `json:"role"`
	Enabled            bool      `json:"enabled"`
	MustChangePassword bool      `json:"must_change_password"`
	AuthVersion        int64     `json:"-"`
	Version            int64     `json:"version"`
	CreatedAt          time.Time `json:"created_at"`
}

type Session struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Current      bool      `json:"current"`
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,63}$`)
var errCredentials = errors.New("username or password incorrect")
var errLoginLimited = errors.New("too many login attempts; retry later")

const sessionIdle = 2 * time.Hour
const sessionLifetime = 7 * 24 * time.Hour

func normalizeUsername(name string) string { return strings.ToLower(strings.TrimSpace(name)) }
func passwordHash(password string) (string, error) {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || len(password) > 1024 {
		return "", errors.New("password must contain at least 12 characters and at most 1024 bytes")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}
func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" || len(password) > 1024 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func readUser(reader sqlReader, id string) (User, error) {
	var user User
	var created string
	err := reader.QueryRow("SELECT id,username,display_name,role,enabled,auth_version,version,created_at,must_change_password FROM users WHERE id=?", id).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &user.Enabled, &user.AuthVersion, &user.Version, &created, &user.MustChangePassword)
	if err == nil {
		user.CreatedAt, err = parsePersistedTime(created)
	}
	return user, err
}
func internalOwner(tx *sql.Tx, options []MutationOptions) (string, error) {
	if len(options) > 0 && options[0].UserID != "" {
		return options[0].UserID, nil
	}
	var id string
	err := tx.QueryRow("SELECT id FROM users WHERE role='admin' AND enabled=1 ORDER BY created_at,id LIMIT 1").Scan(&id)
	return id, err
}
func (s *Store) InitializeAdmin(username, password string) (User, error) {
	return s.createUser(username, password, "admin", true)
}
func (s *Store) createUser(username, password, role string, bootstrap bool) (User, error) {
	return s.createUserRecord(username, password, role, "", false, bootstrap, nil)
}
func (s *Store) createUserRecord(username, password, role, display string, mustChange, bootstrap bool, options []MutationOptions) (User, error) {
	username = normalizeUsername(username)
	if !usernamePattern.MatchString(username) || (role != "admin" && role != "member") {
		return User{}, errors.New("invalid username or role (username: 3-64 ASCII letters, digits, dot, dash or underscore)")
	}
	display = strings.TrimSpace(display)
	if display == "" {
		display = username
	}
	if !utf8.ValidString(display) || utf8.RuneCountInString(display) > 128 {
		return User{}, errors.New("display name must be valid UTF-8 and at most 128 characters")
	}
	encoded, err := passwordHash(password)
	if err != nil {
		return User{}, err
	}
	now := time.Now().UTC()
	user := User{ID: uuid.NewString(), Username: username, DisplayName: display, Role: role, Enabled: true, MustChangePassword: mustChange, AuthVersion: 1, Version: 1, CreatedAt: now}
	err = s.transaction(func(tx *sql.Tx) error {
		if bootstrap {
			var count int
			if err := tx.QueryRow("SELECT count(*) FROM users WHERE role='admin'").Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return errors.New("administrator already initialized")
			}
		}
		var exists bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE username=?)", username).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return errUsernameTaken
		}
		if _, err := tx.Exec("INSERT INTO users(id,username,display_name,password_hash,role,enabled,auth_version,version,created_at,updated_at,must_change_password) VALUES(?,?,?,?,?,1,1,1,?,?,?)", user.ID, username, user.DisplayName, encoded, role, persistedTime(now), persistedTime(now), mustChange); err != nil {
			return err
		}
		if bootstrap {
			if _, err := tx.Exec("INSERT INTO meta(key,value) VALUES('scan_owner_id',?)", user.ID); err != nil {
				return err
			}
		}
		if len(options) == 0 {
			options = []MutationOptions{{Actor: "local-console", ActorType: "console", UserID: user.ID}}
		}
		return auditMutation(tx, "account.create", user.ID, nil, user, options)
	})
	return user, err
}
func (s *Store) HasAdministrator() (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT count(*) FROM users WHERE role='admin'").Scan(&count)
	return count > 0, err
}
func (s *Store) RequireAdministrator() error {
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM users WHERE role='admin' AND enabled=1").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return errors.New("administrator unavailable; initialize with musicserver admin init, or reset an existing administrator offline")
	}
	return nil
}
func (s *Store) BeginServer() error {
	if err := s.RequireAdministrator(); err != nil {
		return err
	}
	return s.transaction(func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE sessions SET revoked_at=? WHERE revoked_at=''", persistedTime(time.Now())); err != nil {
			return err
		}
		return syncCallStats(tx)
	})
}
func (s *Store) reserveLogin(username, ip string) error {
	return s.transaction(func(tx *sql.Tx) error {
		now := time.Now().UTC()
		cutoff := persistedTime(now.Add(-time.Minute))
		if _, err := tx.Exec("DELETE FROM login_limits WHERE window_start<?", cutoff); err != nil {
			return err
		}
		for _, bucket := range []struct {
			key   string
			limit int
		}{{"user/" + hashSecret(username), 5}, {"ip/" + hashSecret(ip), 30}} {
			var attempts int
			err := tx.QueryRow("SELECT attempts FROM login_limits WHERE bucket=?", bucket.key).Scan(&attempts)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if attempts >= bucket.limit {
				return errLoginLimited
			}
		}
		for _, key := range []string{"user/" + hashSecret(username), "ip/" + hashSecret(ip)} {
			if _, err := tx.Exec("INSERT INTO login_limits VALUES(?,1,?) ON CONFLICT(bucket) DO UPDATE SET attempts=attempts+1", key, persistedTime(now)); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) Login(username, password, ip string) (User, Session, string, error) {
	username = normalizeUsername(username)
	if err := s.reserveLogin(username, ip); err != nil {
		return User{}, Session{}, "", err
	}
	var id, encoded string
	err := s.db.QueryRow("SELECT id,password_hash FROM users WHERE username=?", username).Scan(&id, &encoded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, Session{}, "", err
	}
	if err != nil {
		encoded = "$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	verified := verifyPassword(encoded, password)
	if !verified || id == "" {
		return User{}, Session{}, "", errCredentials
	}
	token, err := randomToken(32)
	if err != nil {
		return User{}, Session{}, "", err
	}
	now := time.Now().UTC()
	session := Session{ID: uuid.NewString(), UserID: id, CreatedAt: now, LastActiveAt: now, ExpiresAt: now.Add(sessionLifetime)}
	var user User
	err = s.transaction(func(tx *sql.Tx) error {
		var currentHash string
		if err := tx.QueryRow("SELECT password_hash FROM users WHERE id=?", id).Scan(&currentHash); err != nil {
			return err
		}
		var err error
		user, err = readUser(tx, id)
		if err != nil {
			return err
		}
		if !user.Enabled || currentHash != encoded {
			return errCredentials
		}
		if _, err := tx.Exec("DELETE FROM sessions WHERE expires_at<? OR revoked_at<>''", persistedTime(now)); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO sessions VALUES(?,?,?,?,?,?,?,'')", session.ID, hashSecret(token), id, user.AuthVersion, persistedTime(now), persistedTime(now), persistedTime(session.ExpiresAt)); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM login_limits WHERE bucket=?", "user/"+hashSecret(username)); err != nil {
			return err
		}
		return auditMutation(tx, "account.login", session.ID, nil, nil, []MutationOptions{{Actor: user.Username, ActorType: "session", UserID: id, CredentialID: session.ID}})
	})
	return user, session, token, err
}
func (s *Store) sessionIdentity(token string) (User, Session, bool) {
	var session Session
	var version int64
	var created, active, expires string
	err := s.db.QueryRow("SELECT id,user_id,auth_version,created_at,last_active_at,expires_at FROM sessions WHERE token_hash=? AND revoked_at=''", hashSecret(token)).Scan(&session.ID, &session.UserID, &version, &created, &active, &expires)
	if err != nil {
		return User{}, session, false
	}
	session.CreatedAt, _ = parsePersistedTime(created)
	session.LastActiveAt, _ = parsePersistedTime(active)
	session.ExpiresAt, _ = parsePersistedTime(expires)
	user, err := readUser(s.db, session.UserID)
	now := time.Now()
	return user, session, err == nil && user.Enabled && user.AuthVersion == version && now.Before(session.ExpiresAt) && now.Before(session.LastActiveAt.Add(sessionIdle))
}
func (s *Store) Sessions(userID, current string) ([]Session, error) {
	rows, err := s.db.Query("SELECT id,created_at,last_active_at,expires_at FROM sessions WHERE user_id=? AND revoked_at='' AND expires_at>? AND last_active_at>? ORDER BY created_at DESC", userID, persistedTime(time.Now()), persistedTime(time.Now().Add(-sessionIdle)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Session{}
	for rows.Next() {
		var v Session
		var created, active, expires string
		if err := rows.Scan(&v.ID, &created, &active, &expires); err != nil {
			return nil, err
		}
		v.UserID = userID
		v.Current = v.ID == current
		v.CreatedAt, _ = parsePersistedTime(created)
		v.LastActiveAt, _ = parsePersistedTime(active)
		v.ExpiresAt, _ = parsePersistedTime(expires)
		result = append(result, v)
	}
	return result, rows.Err()
}
func (s *Store) RevokeSession(userID, id string, options ...MutationOptions) error {
	return s.transaction(func(tx *sql.Tx) error {
		result, err := tx.Exec("UPDATE sessions SET revoked_at=? WHERE user_id=? AND id=? AND revoked_at=''", persistedTime(time.Now()), userID, id)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			return sql.ErrNoRows
		}
		return auditMutation(tx, "session.revoke", id, nil, nil, options)
	})
}
func (s *Store) ResetPassword(username, password string, revokeKeys bool) error {
	return s.replacePassword(normalizeUsername(username), "", password, true, revokeKeys, nil)
}
func (s *Store) ChangePassword(user User, old, password string, options ...MutationOptions) error {
	return s.replacePassword(user.Username, old, password, false, false, options)
}
func (s *Store) replacePassword(username, old, password string, local, revokeKeys bool, options []MutationOptions) error {
	if !local && old == password {
		return errors.New("new password must differ from the previous password")
	}
	var id, before string
	if err := s.db.QueryRow("SELECT id,password_hash FROM users WHERE username=?", username).Scan(&id, &before); err != nil {
		return err
	}
	if !local && !verifyPassword(before, old) {
		return errCredentials
	}
	encoded, err := passwordHash(password)
	if err != nil {
		return err
	}
	return s.transaction(func(tx *sql.Tx) error {
		result, err := tx.Exec("UPDATE users SET password_hash=?,must_change_password=0,auth_version=auth_version+1,version=version+1,updated_at=? WHERE id=? AND password_hash=?", encoded, persistedTime(time.Now()), id, before)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return errVersionConflict
		}
		if _, err := tx.Exec("UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at=''", persistedTime(time.Now()), id); err != nil {
			return err
		}
		if revokeKeys {
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
		if local {
			options = []MutationOptions{{Actor: "local-console", ActorType: "console", UserID: id}}
		}
		return auditMutation(tx, "account.password", id, nil, map[string]bool{"revoke_keys": revokeKeys}, options)
	})
}
func (s *Store) SetUserEnabled(id string, enabled bool, expected int64) error {
	return s.transaction(func(tx *sql.Tx) error {
		user, err := readUser(tx, id)
		if err != nil {
			return err
		}
		if user.Version != expected {
			return errVersionConflict
		}
		if user.Role == "admin" && !enabled {
			var count int
			if err := tx.QueryRow("SELECT count(*) FROM users WHERE role='admin' AND enabled=1").Scan(&count); err != nil {
				return err
			}
			if user.Enabled && count <= 1 {
				return errors.New("cannot disable the last administrator")
			}
		}
		if _, err := tx.Exec("UPDATE users SET enabled=?,auth_version=auth_version+1,version=version+1,updated_at=? WHERE id=?", enabled, persistedTime(time.Now()), id); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at=''", persistedTime(time.Now()), id); err != nil {
			return err
		}
		return auditMutation(tx, "account.enabled", id, user, map[string]bool{"enabled": enabled}, []MutationOptions{{Actor: "local-console", ActorType: "console", UserID: id}})
	})
}

func (s *Store) RevokeAccountKeys(userID string, options ...MutationOptions) error {
	return s.transaction(func(tx *sql.Tx) error {
		keys, err := queryJSON[APIKey](tx, "SELECT payload FROM api_keys WHERE user_id=?", userID)
		if err != nil {
			return err
		}
		for _, key := range keys {
			key.RevokedAt = time.Now().UTC()
			if err := saveKey(tx, key); err != nil {
				return err
			}
		}
		return auditMutation(tx, "key.revoke_all", userID, nil, nil, options)
	})
}
