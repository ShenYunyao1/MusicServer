package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallFailureRollsBackProgramAndDatabase(t *testing.T) {
	if os.Getenv("OS") == "Windows_NT" {
		t.Skip("atomic Unix symlink install is tested on Linux CI")
	}
	root := t.TempDir()
	old := filepath.Join(root, "releases", "old")
	next := filepath.Join(root, "releases", "next")
	for _, dir := range []string{old, next} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(old, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	oldBuild := Manifest{Version: "0.13.0", Commit: "1111111111111111111111111111111111111111", Schema: 7}
	if err := atomicJSON(filepath.Join(old, "build.json"), oldBuild, 0644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(root, "library.sqlite")
	os.WriteFile(db, []byte("original database"), 0600)
	restored := false
	starts := 0
	i := Installer{Config: InstallConfig{Root: root, State: filepath.Join(root, "state"), Database: db}, Stop: func(context.Context) error { return nil }, Start: func(context.Context) error { starts++; return nil }, Healthy: func(_ context.Context, m Manifest) error {
		if m.Version == "9.0.0" {
			os.WriteFile(db, []byte("startup modified database"), 0600)
			return errors.New("wrong health response")
		}
		return nil
	}, Backup: func(path string) error { return copyRegular(db, path) }, Restore: func(path string) error { restored = true; return copyRegular(path, db) }, Prepare: func(context.Context, string) (Manifest, string, error) {
		return Manifest{Version: "9.0.0", Commit: "2222222222222222222222222222222222222222", Schema: 7}, next, nil
	}}
	if err := i.Install(context.Background(), Request{ID: "01234567890123456789012345678901", Version: "9.0.0"}); err == nil {
		t.Fatal("broken release installed")
	}
	current, _ := filepath.EvalSymlinks(filepath.Join(root, "current"))
	data, _ := os.ReadFile(db)
	if current != old || string(data) != "original database" || !restored || starts != 2 {
		t.Fatalf("rollback incomplete: path=%s data=%q restored=%t starts=%d", current, data, restored, starts)
	}
	job, err := i.loadJob()
	if err != nil || job.Phase != "rolled_back" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
}

func TestVerifiedInstallRetainsSuccessWhenMaintenanceCleanupFails(t *testing.T) {
	if os.Getenv("OS") == "Windows_NT" {
		t.Skip("Unix installation test")
	}
	root := t.TempDir()
	old := filepath.Join(root, "releases", "old")
	next := filepath.Join(root, "releases", "next")
	os.MkdirAll(old, 0755)
	os.MkdirAll(next, 0755)
	os.Symlink(old, filepath.Join(root, "current"))
	atomicJSON(filepath.Join(old, "build.json"), Manifest{Version: "0.13.0", Schema: 7}, 0644)
	i := Installer{Config: InstallConfig{Root: root, State: filepath.Join(root, "state")}, Stop: func(context.Context) error { return nil }, Start: func(context.Context) error { return nil }, Backup: func(string) error { return nil }, Restore: func(string) error { t.Fatal("verified install must not roll back for cleanup failure"); return nil }, Prepare: func(context.Context, string) (Manifest, string, error) {
		return Manifest{Version: "9.0.0", Schema: 7}, next, nil
	}}
	i.Healthy = func(context.Context, Manifest) error {
		os.Remove(i.maintenance())
		os.Mkdir(i.maintenance(), 0755)
		return os.WriteFile(filepath.Join(i.maintenance(), "block-removal"), []byte("x"), 0600)
	}
	if err := i.Install(context.Background(), Request{ID: strings.Repeat("a", 32), Version: "9.0.0"}); err == nil {
		t.Fatal("expected maintenance cleanup error")
	}
	job, err := i.loadJob()
	if err != nil || job.Phase != "completed" {
		t.Fatalf("verified result lost: %+v %v", job, err)
	}
}
