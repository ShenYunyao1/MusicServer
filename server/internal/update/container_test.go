package update

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestContainerUpdatesAreManagedExternally(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "updates")
	manager := NewManager(directory, func() string { t.Fatal("container contacted release repository"); return Repository })
	defer manager.Close()
	manager.ConfigureContainer()
	manager.ConfigureInstaller(filepath.Join(directory, "queue"), filepath.Join(directory, "job.json"))
	manager.Start(context.Background())
	if status := manager.Status(); status.State != "external" || status.CanInstall || status.JobID != "" {
		t.Fatalf("invalid container update status: %+v", status)
	}
	if manager.Trigger() {
		t.Fatal("container triggered native updater")
	}
	if err := manager.Check(context.Background()); err == nil {
		t.Fatal("container checked native releases")
	}
	if err := manager.Apply("9.0.0"); err == nil {
		t.Fatal("container applied native update")
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("container wrote native updater state", err)
	}
}
