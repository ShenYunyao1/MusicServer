package update

import (
	"path/filepath"
	"testing"
)

func TestCapabilitiesReadDoesNotHoldManagerLockAndRechecksSchema(t *testing.T) {
	m := NewManager(t.TempDir(), func() string { return Repository })
	defer m.Close()
	m.workerState = filepath.Join(t.TempDir(), "job.json")
	m.installedSchema = 8
	m.plan = &releasePlan{Repository: Repository, Manifest: Manifest{Version: "9.0.0", Schema: 8}}
	m.status = Status{State: "available", Repository: Repository, LatestVersion: "9.0.0", Schema: 8, Compatible: true}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan Status, 1)
	m.readCapabilities = func(string) (workerCapabilities, error) { close(started); <-release; return currentCapabilities(), nil }
	go func() { done <- m.Status() }()
	<-started
	if !m.mu.TryLock() {
		close(release)
		<-done
		t.Fatal("capability file I/O holds manager mutex")
	}
	// A schema change while I/O was pending must be considered after relocking.
	m.installedSchema = 9
	m.mu.Unlock()
	close(release)
	status := <-done
	if status.Compatible || status.State != "incompatible" {
		t.Fatal("stale source schema accepted", status)
	}
}

func TestCapabilitiesReadCannotAuthorizeChangedWorkerConfiguration(t *testing.T) {
	m := NewManager(t.TempDir(), func() string { return Repository })
	defer m.Close()
	m.workerState = filepath.Join(t.TempDir(), "old-job.json")
	m.installedSchema = 8
	m.plan = &releasePlan{Repository: Repository, Manifest: Manifest{Version: "9.0.0", Schema: 9, MigrateFrom: []int{8}}}
	m.status = Status{State: "available", Repository: Repository, LatestVersion: "9.0.0", Schema: 9, Compatible: true}
	m.readCapabilities = func(string) (workerCapabilities, error) {
		m.ConfigureInstaller("new-queue", filepath.Join(t.TempDir(), "new-job.json"))
		return currentCapabilities(), nil
	}
	if status := m.Status(); status.Compatible || status.CanInstall {
		t.Fatal("old worker capability authorized changed configuration", status)
	}
}
