//go:build linux

package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyPersistsVersionOnlyRequestAndRejectsConcurrentApply(t *testing.T) {
	root := t.TempDir()
	m := NewManager(filepath.Join(root, "app"), func() string { return Repository })
	defer m.Close()
	m.ConfigureInstaller(filepath.Join(root, "queue"), filepath.Join(root, "job.json"))
	m.status = Status{Repository: Repository, State: "available", Compatible: true, LatestVersion: "9.0.0"}
	if err := m.Apply("8.0.0"); err == nil {
		t.Fatal("stale selection accepted")
	}
	if err := m.Apply("9.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply("9.0.0"); err == nil {
		t.Fatal("concurrent apply accepted")
	}
	paths, err := filepath.Glob(filepath.Join(root, "queue", "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("queue: %v %v", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]string
	if err = json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if len(request) != 2 || request["version"] != "9.0.0" || !jobID.MatchString(request["id"]) {
		t.Fatalf("unsafe request payload: %s", data)
	}
	persisted := NewManager(filepath.Join(root, "app"), func() string { return Repository })
	defer persisted.Close()
	persisted.ConfigureInstaller(filepath.Join(root, "queue"), filepath.Join(root, "job.json"))
	if persisted.Status().JobID != request["id"] {
		t.Fatal("accepted job lost on restart")
	}
}
