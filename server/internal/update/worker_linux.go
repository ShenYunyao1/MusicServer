//go:build linux

package update

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
	"musicserver/internal/version"
)

func LoadInstaller(configPath string) (*Installer, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("update worker must run in its root-owned systemd service")
	}
	if err := rootOwned(configPath, false); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var config InstallConfig
	if err = json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	for _, dir := range []string{config.Root, filepath.Join(config.Root, "releases"), config.State} {
		if !filepath.IsAbs(dir) {
			return nil, errors.New("installer paths must be absolute")
		}
		if err = rootOwned(dir, true); err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(config.Database) || !filepath.IsAbs(config.Queue) || config.Service != "musicserver.service" {
		return nil, errors.New("invalid updater configuration")
	}
	health, err := url.Parse(config.HealthURL)
	if err != nil || health.Scheme != "http" || (health.Hostname() != "127.0.0.1" && health.Hostname() != "localhost") || health.User != nil || health.Path != "/api/v2/health" {
		return nil, errors.New("health URL must use the loopback health endpoint")
	}
	run := func(action string) func(context.Context) error {
		return func(ctx context.Context) error {
			out, err := exec.CommandContext(ctx, "/usr/bin/systemctl", action, config.Service).CombinedOutput()
			if err != nil {
				return fmt.Errorf("systemctl %s: %w (%s)", action, err, strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	i := &Installer{Config: config, Stop: run("stop"), Start: run("start"), Healthy: func(ctx context.Context, m Manifest) error { return HealthCheck(ctx, config.HealthURL, m) }}
	i.Backup = func(target string) error { return backupDatabase(config.Database, target) }
	i.Restore = func(source string) error {
		return restoreDatabase(source, config.Database)
	}
	i.CheckDatabase = verifyDatabaseSchema
	i.Prepare = func(ctx context.Context, v string) (Manifest, string, error) {
		_, current, err := i.previous()
		if err != nil {
			return Manifest{}, "", err
		}
		m := NewManager(filepath.Join(config.State, "downloads"), func() string { return Repository })
		defer m.Close()
		m.installedSchema = current.Schema
		m.workerState = filepath.Join(config.State, "job.json")
		status, err := m.checkRelease(ctx, Status{Repository: Repository}, strings.TrimPrefix(Repository, "https://github.com/"), "tags/v"+v, false)
		if err != nil {
			return Manifest{}, "", err
		}
		if !status.Compatible {
			return Manifest{}, "", errors.New(status.Message)
		}
		plan := m.plan
		if plan == nil {
			return Manifest{}, "", errors.New("no verified release plan")
		}
		var space unix.Statfs_t
		if err = unix.Statfs(config.Root, &space); err != nil {
			return Manifest{}, "", err
		}
		db, err := os.Stat(config.Database)
		if err != nil {
			return Manifest{}, "", err
		}
		required := uint64(plan.Package.Size)*3 + uint64(db.Size())*2 + (64 << 20)
		if space.Bavail*uint64(space.Bsize) < required {
			return Manifest{}, "", errors.New("insufficient free space for update and rollback")
		}
		if err = unix.Statfs(config.State, &space); err != nil {
			return Manifest{}, "", err
		}
		if space.Bavail*uint64(space.Bsize) < required {
			return Manifest{}, "", errors.New("insufficient backup/staging disk space")
		}
		digest, _ := hex.DecodeString(plan.Package.SHA256)
		archive, err := m.download(ctx, v, plan.Address, plan.Package.Name, plan.Package.Size, digest)
		if err != nil {
			return Manifest{}, "", err
		}
		dest, err := os.MkdirTemp(filepath.Join(config.Root, "releases"), v+"-")
		if err != nil {
			return Manifest{}, "", err
		}
		if err = ExtractRelease(archive, dest, plan.Manifest); err != nil {
			_ = os.RemoveAll(dest)
			return Manifest{}, "", err
		}
		if err = os.Chmod(dest, 0755); err != nil {
			return Manifest{}, "", err
		}
		return plan.Manifest, dest, nil
	}
	return i, nil
}

func rootOwned(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 || info.IsDir() != directory || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must be root-owned, with no group/world write or symlink", path)
	}
	return nil
}

func restoreDatabase(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return errors.New("invalid database backup")
	}
	if err = verifyDatabase(source); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err = os.Remove(target + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err = copyRegular(source, target); err != nil {
		return err
	}
	return os.Chown(target, int(stat.Uid), int(stat.Gid))
}

func backupDatabase(source, target string) error {
	for _, suffix := range []string{"-wal", "-journal"} {
		if info, e := os.Stat(source + suffix); e == nil && info.Size() != 0 {
			return errors.New("database journal is not clean after stopping; refusing unsafe backup")
		}
	}
	if err := copyRegular(source, target); err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot preserve backup ownership")
	}
	if err = os.Chown(target, int(stat.Uid), int(stat.Gid)); err != nil {
		return err
	}
	return verifyDatabase(target)
}
func verifyDatabase(path string) error { return verifyDatabaseSchema(path, 0) }

// expected=0 still requires a finite known schema and matching PRAGMA/meta identity.
func verifyDatabaseSchema(path string, expected int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("SQLite backup must be a regular file")
	}
	address := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {"ro"}, "immutable": {"1"}}
	address.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", address.String())
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("database backup integrity check failed")
	}
	// SQLite's structural integrity check does not validate foreign keys.
	// Reject an inconsistent snapshot before migration or rollback can use it.
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	broken := rows.Next()
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if broken {
		return errors.New("database backup foreign key check failed")
	}
	var schema int
	var metadata string
	if err = db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
		return err
	}
	if !version.KnownSchema(schema) || expected != 0 && schema != expected {
		return errors.New("database schema is unknown or differs from installed release")
	}
	if err = db.QueryRow("SELECT value FROM meta WHERE key='schema_version'").Scan(&metadata); err != nil {
		return err
	}
	if metadata != strconv.Itoa(schema) {
		return errors.New("database PRAGMA and metadata schema identity differ")
	}
	return nil
}

func WorkerLock(state string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(state, "worker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another update worker holds the lock")
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
