package update

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"musicserver/internal/version"
)

// CleanupCache only unlinks recognized download files from version directories.
// Installed releases and their rollback database snapshots are never traversed.
func (m *Manager) CleanupCache() (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.status.State == "checking" || m.status.State == "downloading" || m.jobActiveLocked() {
		return 0, errors.New("更新任务运行中，稍后再清理")
	}
	protected := map[string]bool{version.Version: true, m.status.LatestVersion: true}
	if m.workerState != "" {
		data, err := os.ReadFile(m.workerState)
		if err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		if err == nil {
			var job Job
			if json.Unmarshal(data, &job) != nil {
				return 0, errors.New("cannot inspect rollback state")
			}
			if job.Previous != "" {
				raw, err := os.ReadFile(filepath.Join(job.Previous, "build.json"))
				if err != nil {
					return 0, err
				}
				var previous Manifest
				if json.Unmarshal(raw, &previous) != nil {
					return 0, errors.New("cannot inspect rollback identity")
				}
				protected[previous.Version] = true
			}
		}
	}
	root, err := os.OpenRoot(m.dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer root.Close()
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return 0, err
	}
	var freed int64
	for _, entry := range entries {
		if !entry.IsDir() || !versionTag.MatchString(entry.Name()) || protected[entry.Name()] {
			continue
		}
		for _, platform := range []string{"linux", "windows"} {
			for _, arch := range []string{"amd64", "arm64"} {
				name := filepath.Join(entry.Name(), PackageName(platform, arch))
				info, err := root.Lstat(name)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return freed, err
				}
				if !info.Mode().IsRegular() {
					continue
				}
				if err := root.Remove(name); err != nil {
					return freed, err
				}
				freed += info.Size()
			}
		}
		_ = root.Remove(entry.Name())
	}
	return freed, nil
}
