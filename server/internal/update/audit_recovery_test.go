package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvalidQueueEntriesDoNotBlockFollowingRequests(t *testing.T) {
	root := t.TempDir()
	queue := filepath.Join(root, "queue")
	os.MkdirAll(queue, 0700)
	bad := strings.Repeat("0", 32)
	good := strings.Repeat("f", 32)
	os.Mkdir(filepath.Join(queue, bad+".json"), 0700)
	os.WriteFile(filepath.Join(queue, good+".json"), []byte(`{"id":"`+good+`","version":"9.0.0"}`), 0600)
	i := Installer{Config: InstallConfig{Queue: queue, State: filepath.Join(root, "state")}, Prepare: func(context.Context, string) (Manifest, string, error) {
		return Manifest{}, "", errors.New("fixture preparation stopped")
	}}
	_ = i.RunQueued(context.Background())
	job, err := i.loadJob()
	if err != nil || job.ID != good {
		t.Fatal("invalid queue file blocked the next valid request", job, err)
	}
	if _, err := os.Stat(filepath.Join(queue, bad+".json")); !os.IsNotExist(err) {
		t.Fatal("invalid entry remained active", err)
	}
}

func TestQueuedStatusWithoutDurableRequestRecoversImmediately(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, func() string { return Repository })
	defer m.Close()
	m.status = Status{State: "queued", JobID: strings.Repeat("a", 32), Repository: Repository}
	if err := atomicJSON(filepath.Join(root, "status.json"), m.status, 0600); err != nil {
		t.Fatal(err)
	}
	m.ConfigureInstaller(filepath.Join(root, "queue"), filepath.Join(root, "state", "job.json"))
	status := m.Status()
	if status.State == "queued" || status.JobID != "" {
		t.Fatalf("orphan queued state remains: %+v", status)
	}
}

func TestMissingJobMaintenanceRequiresVerifiedInstalledState(t *testing.T) {
	root := t.TempDir()
	i := Installer{Config: InstallConfig{State: root}}
	os.WriteFile(i.maintenance(), []byte("leftover"), 0644)
	if err := i.Recover(context.Background()); err == nil {
		t.Fatal("unknown installed state was treated as recovered")
	}
	if _, err := os.Stat(i.maintenance()); err != nil {
		t.Fatal("unsafe marker was removed", err)
	}
}

func TestMissingJobMaintenanceRecoversVerifiedInstalledState(t *testing.T) {
	root := t.TempDir()
	release := filepath.Join(root, "releases", "current")
	if err := os.MkdirAll(release, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(release, filepath.Join(root, "current")); err != nil {
		t.Skipf("managed link unavailable: %v", err)
	}
	build := Manifest{Version: "0.21.9", Commit: strings.Repeat("a", 40), Schema: 8}
	if err := atomicJSON(filepath.Join(release, "build.json"), build, 0644); err != nil {
		t.Fatal(err)
	}
	i := Installer{Config: InstallConfig{Root: root, State: filepath.Join(root, "state"), Database: filepath.Join(root, "library.sqlite")}, CheckDatabase: func(path string, schema int) error {
		if schema != 8 {
			t.Fatal(schema)
		}
		return nil
	}, Healthy: func(context.Context, Manifest) error { return nil }}
	os.MkdirAll(i.Config.State, 0755)
	os.WriteFile(i.maintenance(), []byte("orphan"), 0644)
	if err := i.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(i.maintenance()); !os.IsNotExist(err) {
		t.Fatal("verified orphan marker retained", err)
	}
}
