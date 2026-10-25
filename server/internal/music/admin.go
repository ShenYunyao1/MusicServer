package music

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"time"
)

func (s *Store) Audit(limit int, before int64) ([]AuditEntry, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := s.db.Query("SELECT id,actor,action,entity_id,changes,created_at,actor_type,user_id,credential_id FROM audit WHERE id<? ORDER BY id DESC LIMIT ?", before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AuditEntry{}
	for rows.Next() {
		var entry AuditEntry
		var raw, created string
		if err := rows.Scan(&entry.ID, &entry.Actor, &entry.Action, &entry.EntityID, &raw, &created, &entry.ActorType, &entry.UserID, &entry.CredentialID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &entry.Changes); err != nil {
			return nil, err
		}
		var err error
		entry.CreatedAt, err = parsePersistedTime(created)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}
func (s *Store) Backup() (string, error) {
	return s.backupSnapshot(context.Background())
}
func (s *Store) backupSnapshot(ctx context.Context) (string, error) {
	if err := backupAcquire(ctx, s.writeMu.TryLock); err != nil {
		return "", err
	}
	defer s.writeMu.Unlock()
	directory := filepath.Join(filepath.Dir(s.dataPath), "backups")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(filepath.Join(directory, time.Now().UTC().Format("20060102T150405")+"-"+uuid.NewString()+".sqlite"))
	if err != nil {
		return "", err
	}
	success := false
	defer func() {
		if !success {
			_ = os.Remove(absolute)
		}
	}()
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", absolute); err != nil {
		return "", err
	}
	if err := os.Chmod(absolute, 0600); err != nil {
		return "", err
	}
	db, err := openSQLite(absolute)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return "", err
	}
	if integrity != "ok" {
		return "", errors.New("backup integrity check failed: " + integrity)
	}
	success = true
	return absolute, nil
}
