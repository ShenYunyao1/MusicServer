//go:build linux

package update

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerRefreshLinuxOwnershipAndLock(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("exercise real ownership checks in the isolated root Linux fixture")
	}
	i, options, next := workerRefreshFixture(t)
	options.Trusted = rootOwned
	unlock, err := WorkerLock(i.Config.State)
	if err != nil {
		t.Fatal(err)
	}
	if release, err := WorkerLock(i.Config.State); err == nil {
		release()
		t.Fatal("concurrent worker maintenance lock accepted")
	}
	unlock()
	source := filepath.Join(next, "musicserver-updater")
	if err := os.Chmod(source, 0777); err != nil {
		t.Fatal(err)
	}
	if err := i.refreshWorkerAt(context.Background(), "9.0.0", options); err == nil {
		t.Fatal("writable candidate worker accepted")
	}
	os.Chmod(source, 0755)
	if err := i.refreshWorkerAt(context.Background(), "9.0.0", options); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(options.Target)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("installed updater executable permissions are wrong", err)
	}
}

func TestWorkerRefreshLinuxRejectsLinks(t *testing.T) {
	for _, kind := range []string{"candidate", "installed", "capabilities"} {
		t.Run(kind, func(t *testing.T) {
			i, options, next := workerRefreshFixture(t)
			path := filepath.Join(next, "musicserver-updater")
			if kind == "installed" {
				path = options.Target
			} else if kind == "capabilities" {
				path = filepath.Join(i.Config.State, capabilitiesName)
			}
			original := path + ".original"
			if err := os.Rename(path, original); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(original, path); err != nil {
				t.Fatal(err)
			}
			if err := i.refreshWorkerAt(context.Background(), "9.0.0", options); err == nil {
				t.Fatal("linked maintenance file accepted")
			}
		})
	}
}
