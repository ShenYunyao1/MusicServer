package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"musicserver/internal/activity"
)

var errBackupBusy = errors.New("another backup task is running")
var errBackupInUse = errors.New("backup is being downloaded")

type BackupPolicy struct {
	Enabled       bool `json:"enabled"`
	IntervalHours int  `json:"interval_hours"`
	KeepLast      int  `json:"keep_last"`
	IncludeMedia  bool `json:"include_media"`
}
type BackupJob struct {
	ID          string         `json:"id"`
	State       string         `json:"state"`
	Phase       string         `json:"phase"`
	Bytes       int64          `json:"bytes"`
	Total       int64          `json:"total"`
	StartedAt   time.Time      `json:"started_at,omitzero"`
	CompletedAt time.Time      `json:"completed_at,omitzero"`
	Error       string         `json:"error,omitempty"`
	Archive     *BackupArchive `json:"archive,omitempty"`
}

func (s *Store) BackupPolicy() (BackupPolicy, error) {
	policy := BackupPolicy{IntervalHours: 24, KeepLast: 3, IncludeMedia: true}
	var raw string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key='backup_policy'").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	if err != nil {
		return policy, err
	}
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return policy, err
	}
	return policy, nil
}
func (s *Store) SetBackupPolicy(policy BackupPolicy, options MutationOptions) error {
	if policy.IntervalHours < 1 || policy.IntervalHours > 168 || policy.KeepLast < 1 || policy.KeepLast > 30 {
		return errors.New("backup interval must be 1-168 hours and retention 1-30 archives")
	}
	return s.transaction(func(tx *sql.Tx) error {
		if err := requireAdminActor(tx, []MutationOptions{options}); err != nil {
			return err
		}
		raw, _ := json.Marshal(policy)
		if _, err := tx.Exec("INSERT INTO settings(key,value) VALUES('backup_policy',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(raw)); err != nil {
			return err
		}
		return auditMutation(tx, "backup.policy", "backup", nil, policy, []MutationOptions{options})
	})
}
func (s *Store) BackupJob() BackupJob {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	job := s.backupJob
	if job.State == "" {
		job.State = "idle"
	}
	return job
}
func (s *Store) StartBackup(includeMedia, automatic bool, options MutationOptions) (BackupJob, error) {
	if !automatic {
		if err := s.transaction(func(tx *sql.Tx) error { return requireAdminActor(tx, []MutationOptions{options}) }); err != nil {
			return BackupJob{}, err
		}
	}
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if s.closed {
		return BackupJob{}, errors.New("server is closing")
	}
	s.backupMu.Lock()
	if s.backupJob.State == "running" || s.backupJob.State == "cancelling" {
		s.backupMu.Unlock()
		return BackupJob{}, errBackupBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.backupCancel = cancel
	s.backupJob = BackupJob{ID: uuid.NewString(), State: "running", Phase: "snapshot", StartedAt: time.Now().UTC()}
	job := s.backupJob
	s.workers.Add(1)
	s.backupMu.Unlock()
	go func() {
		defer s.workers.Done()
		defer cancel()
		_ = s.activity.Emit("server.backup_started", activity.Fields{})
		_, attemptErr := s.db.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES('backup_last_attempt',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", persistedTime(time.Now()))
		var archive BackupArchive
		err := attemptErr
		if err == nil {
			archive, err = s.CreateBackupBundle(ctx, includeMedia, func(phase string, done, total int64) {
				s.backupMu.Lock()
				s.backupJob.Phase, s.backupJob.Bytes, s.backupJob.Total = phase, done, total
				s.backupMu.Unlock()
			}, automatic)
		}
		if err == nil {
			err = s.transaction(func(tx *sql.Tx) error {
				if _, err := tx.Exec("INSERT INTO meta(key,value) VALUES('backup_last_success',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", persistedTime(time.Now())); err != nil {
					return err
				}
				return auditMutation(tx, "backup.created", archive.ID, nil, archive, []MutationOptions{options})
			})
			if err == nil {
				// Manual archives are never pruned by the automatic retention policy.
				_ = s.pruneAutomaticBackups()
			}
		}
		s.backupMu.Lock()
		s.backupJob.CompletedAt = time.Now().UTC()
		s.backupCancel = nil
		if err == nil {
			s.backupJob.State, s.backupJob.Phase, s.backupJob.Archive = "completed", "completed", &archive
		} else if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			s.backupJob.State, s.backupJob.Error = "cancelled", "备份已取消，已有成功备份保持不变。"
		} else {
			s.backupJob.State, s.backupJob.Error = "failed", err.Error()
		}
		s.backupMu.Unlock()
		code := "server.backup_completed"
		if err != nil {
			code = "server.backup_failed"
		}
		_ = s.activity.Emit(code, activity.Fields{})
	}()
	return job, nil
}
func (s *Store) CancelBackup() {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	if s.backupCancel != nil {
		s.backupJob.State = "cancelling"
		s.backupCancel()
	}
}
func (s *Store) DeleteBackup(id string, options MutationOptions) error {
	name, err := backupArchiveName(id)
	if err != nil {
		return err
	}
	if err := s.transaction(func(tx *sql.Tx) error {
		if err := requireAdminActor(tx, []MutationOptions{options}); err != nil {
			return err
		}
		return auditMutation(tx, "backup.delete_requested", id, nil, nil, []MutationOptions{options})
	}); err != nil {
		return err
	}
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	if s.backupReaders[id] > 0 {
		return errBackupInUse
	}
	return os.Remove(filepath.Join(s.backupDirectory(), name))
}
func (s *Store) pruneAutomaticBackups() error {
	policy, err := s.BackupPolicy()
	if err != nil {
		return err
	}
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	items, err := s.BackupArchives()
	if err != nil {
		return err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	kept := 0
	for _, item := range items {
		if !item.Automatic {
			continue
		}
		kept++
		if kept > policy.KeepLast && s.backupReaders[item.ID] == 0 {
			if err := os.Remove(filepath.Join(s.backupDirectory(), item.Name)); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) MaybeRunScheduledBackup(now time.Time) error {
	policy, err := s.BackupPolicy()
	if err != nil || !policy.Enabled || s.ScanStatus().State == "running" {
		return err
	}
	var last string
	err = s.db.QueryRow("SELECT value FROM meta WHERE key='backup_last_success'").Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	when, err := parsePersistedTime(last)
	if err != nil {
		return err
	}
	if !when.IsZero() && now.Before(when.Add(time.Duration(policy.IntervalHours)*time.Hour)) {
		return nil
	}
	var attempt string
	err = s.db.QueryRow("SELECT value FROM meta WHERE key='backup_last_attempt'").Scan(&attempt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if attempted, err := parsePersistedTime(attempt); err != nil {
		return err
	} else if !attempted.IsZero() && now.Before(attempted.Add(time.Hour)) {
		return nil
	}
	_, err = s.StartBackup(policy.IncludeMedia, true, MutationOptions{Actor: "backup-scheduler", ActorType: "system"})
	if errors.Is(err, errBackupBusy) {
		return nil
	}
	return err
}
func (s *Store) StartBackupScheduler(blocked func() bool) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if blocked != nil && blocked() {
					continue
				}
				if err := s.MaybeRunScheduledBackup(now); err != nil {
					_ = s.activity.Emit("server.backup_failed", activity.Fields{})
				}
			}
		}
	}()
	return func() { cancel(); <-done; s.CancelBackup() }
}
