package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workerRefreshFixture(t *testing.T) (*Installer, workerRefreshOptions, string) {
	t.Helper()
	// Hosted Windows runners expose TEMP through an 8.3 alias (RUNNER~1).
	// The production guard deliberately rejects aliases/symlinks, so prepare
	// the fixture under its canonical parent instead of weakening that guard.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	i := &Installer{Config: InstallConfig{Root: filepath.Join(root, "app"), State: filepath.Join(root, "state")}}
	next := filepath.Join(i.Config.Root, "releases", "candidate")
	target := filepath.Join(root, "bin", "musicserver-updater")
	for _, dir := range []string{next, i.Config.State, filepath.Dir(target)} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	own := Manifest{Format: 1, Version: "9.0.0", Commit: strings.Repeat("b", 40), Schema: 7}
	if err := atomicJSON(filepath.Join(next, "build.json"), own, 0644); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{target: "old worker", filepath.Join(next, "musicserver-updater"): "verified new worker", filepath.Join(i.Config.State, capabilitiesName): "old trusted capabilities"} {
		if err := os.WriteFile(path, []byte(data), 0755); err != nil {
			t.Fatal(err)
		}
	}
	i.Prepare = func(context.Context, string) (Manifest, string, error) { return own, next, nil }
	for _, action := range []*func(context.Context) error{&i.Stop, &i.Start} {
		*action = func(context.Context) error { t.Error("maintenance touched the application service"); return nil }
	}
	i.Backup = func(string) error { t.Error("maintenance touched the application database"); return nil }
	i.Restore = i.Backup
	options := workerRefreshOptions{Target: target, Own: own, Current: Manifest{Version: "8.0.0", Schema: 7}, Trusted: func(path string, directory bool) error {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory {
			return errors.New("untrusted path")
		}
		return nil
	}}
	options.Publish = func() error {
		data, _ := os.ReadFile(target)
		if string(data) != "verified new worker" {
			t.Error("capabilities advertised before worker replacement")
		}
		return atomicJSON(filepath.Join(i.Config.State, capabilitiesName), workerCapabilities{Format: 1, Version: own.Version, Commit: own.Commit, Schemas: []int{7, 8}, Migrations: []schemaMigration{{From: 7, To: 8}}}, 0644)
	}
	options.Unchanged = func() error { return nil }
	return i, options, next
}

func TestWorkerRefreshPreservesApplicationAndPublishesAfterReplacement(t *testing.T) {
	i, options, _ := workerRefreshFixture(t)
	if err := i.refreshWorkerAt(context.Background(), "9.0.0", options); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(options.Target)
	if err != nil || string(data) != "verified new worker" {
		t.Fatal("worker not installed", string(data), err)
	}
	old, err := os.ReadFile(filepath.Join(i.Config.State, "worker-previous"))
	if err != nil || string(old) != "old worker" {
		t.Fatal("previous worker not retained", string(old), err)
	}
	data, _ = os.ReadFile(filepath.Join(i.Config.State, capabilitiesName))
	var capabilities workerCapabilities
	if json.Unmarshal(data, &capabilities) != nil || capabilities.Commit != options.Own.Commit {
		t.Fatal("capabilities not published after replacement")
	}
}

func TestWorkerRefreshRejectsUnsafeOrActivePlans(t *testing.T) {
	for _, scenario := range []string{"untrusted", "active-job", "corrupt-job", "maintenance", "prepare-error", "identity", "package-identity", "outside", "missing-helper", "schema-change", "application-downgrade", "worker-downgrade", "changed-application", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			i, options, next := workerRefreshFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "untrusted":
				options.Trusted = func(string, bool) error { return errors.New("untrusted owner") }
			case "active-job":
				atomicJSON(filepath.Join(i.Config.State, "job.json"), Job{Phase: "backing_up"}, 0644)
			case "corrupt-job":
				os.WriteFile(filepath.Join(i.Config.State, "job.json"), []byte("invalid"), 0644)
			case "maintenance":
				os.WriteFile(i.maintenance(), []byte("active"), 0644)
			case "prepare-error":
				i.Prepare = func(context.Context, string) (Manifest, string, error) {
					return Manifest{}, "", errors.New("signature rejected")
				}
			case "identity":
				bad := options.Own
				bad.Commit = strings.Repeat("c", 40)
				i.Prepare = func(context.Context, string) (Manifest, string, error) { return bad, next, nil }
			case "package-identity":
				atomicJSON(filepath.Join(next, "build.json"), Manifest{Version: "1.0.0"}, 0644)
			case "outside":
				i.Prepare = func(context.Context, string) (Manifest, string, error) { return options.Own, t.TempDir(), nil }
			case "missing-helper":
				os.Remove(filepath.Join(next, "musicserver-updater"))
			case "schema-change":
				options.Current.Schema = 8
			case "application-downgrade":
				options.Current.Version = "10.0.0"
			case "worker-downgrade":
				atomicJSON(filepath.Join(i.Config.State, capabilitiesName), workerCapabilities{Format: 1, Version: "10.0.0"}, 0644)
			case "cancelled":
				cancel()
			case "changed-application":
				options.Unchanged = func() error { return errors.New("installed application changed") }
			}
			before, _ := os.ReadFile(filepath.Join(i.Config.State, capabilitiesName))
			published := false
			options.Publish = func() error { published = true; return nil }
			if err := i.refreshWorkerAt(ctx, "9.0.0", options); err == nil {
				t.Fatal("unsafe maintenance accepted")
			}
			after, _ := os.ReadFile(options.Target)
			caps, _ := os.ReadFile(filepath.Join(i.Config.State, capabilitiesName))
			if string(after) != "old worker" || string(caps) != string(before) || published {
				t.Fatal("rejected maintenance modified installed state")
			}
		})
	}
}

func TestWorkerRefreshCapabilityFailureRestoresConservativeState(t *testing.T) {
	for _, hadCapabilities := range []bool{true, false} {
		t.Run(fmt.Sprint(hadCapabilities), func(t *testing.T) {
			i, options, _ := workerRefreshFixture(t)
			path := filepath.Join(i.Config.State, capabilitiesName)
			if !hadCapabilities {
				os.Remove(path)
			}
			options.Publish = func() error {
				if err := atomicWrite(path, []byte("new capabilities"), 0644); err != nil {
					t.Fatal(err)
				}
				return errors.New("directory synchronization failed after capability rename")
			}
			if err := i.refreshWorkerAt(context.Background(), "9.0.0", options); err == nil {
				t.Fatal("capability publication failure ignored")
			}
			worker, _ := os.ReadFile(options.Target)
			data, err := os.ReadFile(path)
			if string(worker) != "old worker" || hadCapabilities && (err != nil || string(data) != "old trusted capabilities") || !hadCapabilities && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("incomplete rollback: worker=%q capabilities=%q error=%v", worker, data, err)
			}
		})
	}
}

func TestWorkerRefreshKeepsNewExecutableWhenCapabilityRecoveryFails(t *testing.T) {
	i, options, _ := workerRefreshFixture(t)
	path := filepath.Join(i.Config.State, capabilitiesName)
	options.Publish = func() error {
		os.Remove(path)
		os.Mkdir(path, 0700)
		os.WriteFile(filepath.Join(path, "block-restore"), []byte("fixture"), 0600)
		return errors.New("publication failure")
	}
	err := i.refreshWorkerAt(context.Background(), "9.0.0", options)
	if err == nil || !strings.Contains(err.Error(), "new updater retained") {
		t.Fatal("capabilities recovery failure hidden", err)
	}
	data, _ := os.ReadFile(options.Target)
	if string(data) != "verified new worker" {
		t.Fatal("restored old executable while new capabilities could remain")
	}
}

func TestWorkerRefreshCannotAdvertiseAnUninstalledExecutable(t *testing.T) {
	i, options, next := workerRefreshFixture(t)
	prepared := filepath.Join(next, "musicserver-updater")
	if err := verifySameWorkerFiles(prepared, options.Target, options.Trusted); err == nil {
		t.Fatal("uninstalled candidate could advertise new capabilities")
	}
	if err := i.refreshWorkerAt(context.Background(), "9.0.0", options); err != nil {
		t.Fatal(err)
	}
	if err := verifySameWorkerFiles(prepared, options.Target, options.Trusted); err != nil {
		t.Fatal("installed verified candidate not recognized", err)
	}
}

func TestWorkerRefreshLegacyWithoutCapabilities(t *testing.T) {
	i, options, _ := workerRefreshFixture(t)
	path := filepath.Join(i.Config.State, capabilitiesName)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := i.refreshWorkerAt(context.Background(), "9.0.0", options); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := decodeCapabilities(data)
	if err != nil || !c.supports(7, 8) {
		t.Fatal("legacy independent updater was not prepared for the declared migration", err)
	}
}
