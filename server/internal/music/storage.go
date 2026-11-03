package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"musicserver/internal/diskspace"
)

var errStorageLow = errors.New("磁盘可用空间不足，请释放空间或调整预留空间后重试")
var errStorageQuota = errors.New("账户存储额度不足，请联系管理员调整额度")

type StoragePolicy struct {
	ReserveBytes int64 `json:"reserve_bytes"`
	Version      int64 `json:"version"`
}
type StorageQuota struct {
	LimitBytes int64 `json:"limit_bytes"`
	Version    int64 `json:"version"`
}
type AccountStorage struct {
	storageBreakdown
	ID        string `json:"id"`
	Username  string `json:"username"`
	Deleted   bool   `json:"deleted"`
	UsedBytes int64  `json:"used_bytes"`
	StorageQuota
}
type StorageSummary struct {
	Policy           StoragePolicy    `json:"policy"`
	DataDisk         diskspace.Space  `json:"data_disk"`
	MediaDisk        diskspace.Space  `json:"media_disk"`
	DatabaseBytes    int64            `json:"database_bytes"`
	MediaBytes       int64            `json:"media_bytes"`
	StagingBytes     int64            `json:"staging_bytes"`
	BackupBytes      int64            `json:"backup_bytes"`
	UpdateBytes      int64            `json:"update_bytes"`
	Accounts         []AccountStorage `json:"accounts"`
	UsageRefreshedAt string           `json:"usage_refreshed_at,omitempty"`
}

func readStorageJSON(reader sqlReader, key string, target any) error {
	var raw string
	err := reader.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), target)
}
func (s *Store) StoragePolicy() (StoragePolicy, error) {
	policy := StoragePolicy{ReserveBytes: diskspace.DefaultReserve}
	err := readStorageJSON(s.db, "storage_policy", &policy)
	return policy, err
}
func (s *Store) SetStoragePolicy(policy StoragePolicy, options MutationOptions) error {
	if policy.ReserveBytes < 0 || policy.ReserveBytes > 1<<40 {
		return errors.New("预留空间范围为 0–1 TiB")
	}
	return s.transaction(func(tx *sql.Tx) error {
		if err := requireAdminActor(tx, []MutationOptions{options}); err != nil {
			return err
		}
		current := StoragePolicy{ReserveBytes: diskspace.DefaultReserve}
		if err := readStorageJSON(tx, "storage_policy", &current); err != nil {
			return err
		}
		if current.Version != policy.Version {
			return errVersionConflict
		}
		policy.Version++
		encoded, _ := json.Marshal(policy)
		if _, err := tx.Exec("INSERT INTO settings(key,value) VALUES('storage_policy',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(encoded)); err != nil {
			return err
		}
		return auditMutation(tx, "storage.policy", "storage", current, policy, []MutationOptions{options})
	})
}
func (s *Store) SetStorageQuota(id string, limit, expected int64, options MutationOptions) (StorageQuota, error) {
	quota := StorageQuota{LimitBytes: limit, Version: expected + 1}
	if limit < 0 || limit > 1<<50 {
		return quota, errors.New("账户额度范围为 0–1 PiB，0 为不限额")
	}
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
		if user.DeletedAt != "" {
			return sql.ErrNoRows
		}
		var current StorageQuota
		if err := readStorageJSON(tx, "storage_quota/"+id, &current); err != nil {
			return err
		}
		if current.Version != expected {
			return errVersionConflict
		}
		encoded, _ := json.Marshal(quota)
		if _, err := tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "storage_quota/"+id, string(encoded)); err != nil {
			return err
		}
		email, err := readAccountEmail(tx, id)
		if err != nil {
			return err
		}
		email.QuotaSource = "administrator"
		if err := writeAccountEmail(tx, id, email); err != nil {
			return err
		}
		return auditMutation(tx, "storage.quota", id, current, quota, []MutationOptions{options})
	})
	return quota, err
}
func (s *Store) probeSpace(path string) (diskspace.Space, error) {
	if s.spaceProbe != nil {
		return s.spaceProbe(path)
	}
	return diskspace.Read(path)
}
func (s *Store) checkSpace(path string, additional int64) error {
	policy, err := s.StoragePolicy()
	if err != nil {
		return err
	}
	space, err := s.probeSpace(path)
	if err != nil {
		return err
	}
	if additional < 0 || uint64(additional) > space.Available || uint64(policy.ReserveBytes) > space.Available-uint64(additional) {
		return errStorageLow
	}
	return nil
}

type storageWriter struct {
	store  *Store
	path   string
	writer io.Writer
}

func (w storageWriter) Write(data []byte) (int, error) {
	w.store.storageWriteMu.Lock()
	defer w.store.storageWriteMu.Unlock()
	if err := w.store.checkSpace(w.path, int64(len(data))); err != nil {
		return 0, err
	}
	return w.writer.Write(data)
}

// The scanner and mutations maintain reference sizes atomically. External file
// changes become visible on scan, rather than forcing every upload to stat the
// entire library under the global write/admission lock.
func (s *Store) accountUsage(reader sqlReader) (map[string]int64, error) {
	rows, err := reader.Query("SELECT id FROM users")
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return s.accountUsageForIDs(reader, ids)
}
func directoryBytes(path string) (int64, error) {
	return directoryBytesWithWalker(path, filepath.WalkDir)
}
func directoryBytesWithWalker(path string, walk func(string, fs.WalkDirFunc) error) (int64, error) {
	var total int64
	err := walk(path, func(name string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}
func (s *Store) StorageOverview(contexts ...context.Context) (StorageSummary, error) {
	var out StorageSummary
	var err error
	if out.DataDisk, err = s.probeSpace(filepath.Dir(s.dataPath)); err != nil {
		return out, err
	}
	if out.MediaDisk, err = s.probeSpace(s.mediaRoot); err != nil {
		return out, err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if stat, err := os.Stat(s.dataPath + suffix); err == nil {
			out.DatabaseBytes += stat.Size()
		} else if !errors.Is(err, os.ErrNotExist) {
			return out, err
		}
	}
	for _, entry := range []struct {
		path   string
		target *int64
	}{{filepath.Join(s.mediaRoot, ".staging"), &out.StagingBytes}, {s.backupDirectory(), &out.BackupBytes}, {filepath.Join(filepath.Dir(s.dataPath), "updates"), &out.UpdateBytes}} {
		if *entry.target, err = directoryBytes(entry.path); err != nil {
			return out, err
		}
	}
	ctx, cancel := context.WithTimeout(readContext(contexts), 5*time.Second)
	defer cancel()
	err = s.readQuery(ctx, func(reader sqlReader) error {
		out.Policy = StoragePolicy{ReserveBytes: diskspace.DefaultReserve}
		if err := readStorageJSON(reader, "storage_policy", &out.Policy); err != nil {
			return err
		}
		if err := reader.QueryRow("SELECT value FROM settings WHERE key='storage_refreshed_at'").Scan(&out.UsageRefreshedAt); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		used, err := s.accountUsage(reader)
		if err != nil {
			return err
		}
		rows, err := reader.Query("SELECT id,username,COALESCE((SELECT value FROM meta WHERE key='deleted_user/'||users.id),'') FROM users ORDER BY username,id")
		if err != nil {
			return err
		}
		out.Accounts = []AccountStorage{}
		for rows.Next() {
			var account AccountStorage
			var deleted string
			if err := rows.Scan(&account.ID, &account.Username, &deleted); err != nil {
				rows.Close()
				return err
			}
			account.Deleted = deleted != ""
			account.UsedBytes = used[account.ID]
			out.Accounts = append(out.Accounts, account)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for i := range out.Accounts {
			b, err := accountStorageBreakdown(reader, out.Accounts[i].ID)
			if err != nil {
				return err
			}
			out.Accounts[i].storageBreakdown = b
			out.MediaBytes += b.AudioBytes
			if err := readStorageJSON(reader, "storage_quota/"+out.Accounts[i].ID, &out.Accounts[i].StorageQuota); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
