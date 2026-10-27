//go:build linux

package update

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createBridgeDatabase(t *testing.T, path string, schema int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(fmt.Sprintf("PRAGMA user_version=%d; CREATE TABLE meta(key TEXT PRIMARY KEY,value TEXT); INSERT INTO meta VALUES('schema_version','%d'); CREATE TABLE marker(value TEXT); INSERT INTO marker VALUES('original');", schema, schema))
	if err != nil {
		t.Fatal(err)
	}
}

func migrateBridgeDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("PRAGMA user_version=8; UPDATE meta SET value='8' WHERE key='schema_version'; UPDATE marker SET value='migrated'"); err != nil {
		t.Fatal(err)
	}
}

func bridgeInstaller(t *testing.T, failure string) (*Installer, string, string) {
	t.Helper()
	root := t.TempDir()
	old, next := filepath.Join(root, "releases", "old"), filepath.Join(root, "releases", "next")
	for _, dir := range []string{old, next} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(old, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(old, "build.json"), Manifest{Version: "1.0.0", Schema: 7}, 0644); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Version: "9.0.0", Schema: 8, MigrateFrom: []int{7}}
	if err := atomicJSON(filepath.Join(next, "build.json"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(root, "library.sqlite")
	createBridgeDatabase(t, database, 7)
	i := &Installer{Config: InstallConfig{Root: root, State: filepath.Join(root, "state"), Database: database}, Stop: func(context.Context) error { return nil }, Prepare: func(context.Context, string) (Manifest, string, error) { return manifest, next, nil }, Backup: func(p string) error { return backupDatabase(database, p) }, Restore: func(p string) error { return restoreDatabase(p, database) }, CheckDatabase: verifyDatabaseSchema}
	i.Start = func(context.Context) error {
		current, err := filepath.EvalSymlinks(filepath.Join(root, "current"))
		if err != nil {
			return err
		}
		if current == next {
			migrateBridgeDatabase(t, database)
			if failure == "start" {
				return errors.New("migration startup failed")
			}
		}
		return nil
	}
	i.Healthy = func(_ context.Context, m Manifest) error {
		if m.Schema == 8 && failure == "health" {
			return errors.New("new release health failed")
		}
		return verifyDatabaseSchema(database, m.Schema)
	}
	return i, old, next
}

func TestSchemaBridgeInstallSuccessAndRollback(t *testing.T) {
	for _, failure := range []string{"", "start", "health"} {
		t.Run("failure_"+failure, func(t *testing.T) {
			i, old, next := bridgeInstaller(t, failure)
			err := i.Install(context.Background(), Request{ID: strings.Repeat("a", 32), Version: "9.0.0"})
			wantPath, wantSchema, wantPhase := next, 8, "completed"
			if failure != "" {
				wantPath, wantSchema, wantPhase = old, 7, "rolled_back"
				if err == nil {
					t.Fatal("broken migration installed")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			current, _ := filepath.EvalSymlinks(filepath.Join(i.Config.Root, "current"))
			if current != wantPath {
				t.Fatalf("current=%s want=%s", current, wantPath)
			}
			if err = verifyDatabaseSchema(i.Config.Database, wantSchema); err != nil {
				t.Fatal(err)
			}
			j, err := i.loadJob()
			if err != nil || j.Phase != wantPhase {
				t.Fatalf("job=%+v error=%v", j, err)
			}
			if _, err = os.Stat(i.maintenance()); !os.IsNotExist(err) {
				t.Fatalf("maintenance flag retained: %v", err)
			}
			if err = verifyDatabaseSchema(j.Backup, 7); err != nil {
				t.Fatalf("original schema snapshot lost: %v", err)
			}
		})
	}
}

func TestSchemaBridgeRequiresExactSourceBackupAndCheck(t *testing.T) {
	for _, failure := range []string{"missing-check", "wrong-backup", "undeclared", "unknown", "reverse", "changed-installed"} {
		t.Run(failure, func(t *testing.T) {
			i, old, next := bridgeInstaller(t, "")
			switch failure {
			case "missing-check":
				i.CheckDatabase = nil
			case "wrong-backup":
				i.Backup = func(path string) error {
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						return err
					}
					createBridgeDatabase(t, path, 8)
					return nil
				}
			case "undeclared":
				i.Prepare = func(context.Context, string) (Manifest, string, error) {
					return Manifest{Version: "9.0.0", Schema: 8}, next, nil
				}
			case "unknown":
				i.Prepare = func(context.Context, string) (Manifest, string, error) {
					return Manifest{Version: "9.0.0", Schema: 999, MigrateFrom: []int{7}}, next, nil
				}
			case "reverse":
				atomicJSON(filepath.Join(old, "build.json"), Manifest{Version: "1.0.0", Schema: 8}, 0644)
				i.Prepare = func(context.Context, string) (Manifest, string, error) {
					return Manifest{Version: "9.0.0", Schema: 7, MigrateFrom: []int{8}}, next, nil
				}
			case "changed-installed":
				i.Prepare = func(context.Context, string) (Manifest, string, error) {
					if err := atomicJSON(filepath.Join(old, "build.json"), Manifest{Version: "1.0.1", Schema: 7}, 0644); err != nil {
						return Manifest{}, "", err
					}
					return Manifest{Version: "9.0.0", Schema: 8, MigrateFrom: []int{7}}, next, nil
				}
			}
			if err := i.Install(context.Background(), Request{ID: strings.Repeat("b", 32), Version: "9.0.0"}); err == nil {
				t.Fatal("unsafe bridge installed")
			}
			current, _ := filepath.EvalSymlinks(filepath.Join(i.Config.Root, "current"))
			if current != old {
				t.Fatal("program switched before source validation")
			}
			if err := verifyDatabaseSchema(i.Config.Database, 7); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSchemaBridgeInterruptedWorkerRestoresOldSchema(t *testing.T) {
	i, old, next := bridgeInstaller(t, "")
	j := Job{ID: strings.Repeat("c", 32), Version: "9.0.0", Previous: old, Target: next, Backup: filepath.Join(i.Config.State, "backups", "snapshot.sqlite"), RestoreRequired: true}
	if err := i.Backup(j.Backup); err != nil {
		t.Fatal(err)
	}
	if err := replaceLink(i.Config.Root, next); err != nil {
		t.Fatal(err)
	}
	migrateBridgeDatabase(t, i.Config.Database)
	if err := i.save(&j, "starting", "interrupted"); err != nil {
		t.Fatal(err)
	}
	if err := i.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, _ := filepath.EvalSymlinks(filepath.Join(i.Config.Root, "current"))
	if current != old {
		t.Fatal("previous program not restored")
	}
	if err := verifyDatabaseSchema(i.Config.Database, 7); err != nil {
		t.Fatal(err)
	}
	j, err := i.loadJob()
	if err != nil || j.Phase != "rolled_back" {
		t.Fatalf("job=%+v error=%v", j, err)
	}
}

func TestVerifyDatabaseRequiresKnownMatchingSchemaIdentity(t *testing.T) {
	for _, schema := range []int{7, 8, 999} {
		path := filepath.Join(t.TempDir(), "library.sqlite")
		createBridgeDatabase(t, path, schema)
		err := verifyDatabase(path)
		if (err == nil) != (schema != 999) {
			t.Fatalf("schema=%d error=%v", schema, err)
		}
		if schema != 999 {
			if err := verifyDatabaseSchema(path, 15-schema); err == nil {
				t.Fatal("wrong source identity accepted")
			}
		}
	}
	path := filepath.Join(t.TempDir(), "library.sqlite")
	createBridgeDatabase(t, path, 7)
	db, _ := sql.Open("sqlite", path)
	db.Exec("UPDATE meta SET value='8' WHERE key='schema_version'")
	db.Close()
	if err := verifyDatabase(path); err == nil {
		t.Fatal("PRAGMA/meta disagreement accepted")
	}
}

func TestWorkerCapabilityFileCannotBeForgedByApp(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, capabilitiesName)
	if err := atomicJSON(path, currentCapabilities(), 0644); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if _, err := readWorkerCapabilities(path); err == nil {
			t.Fatal("app-owned capability accepted")
		}
		return
	}
	if _, err := readWorkerCapabilities(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkerCapabilities(path); err == nil {
		t.Fatal("writable capability accepted")
	}
	os.Chmod(path, 0644)
	if err := os.Chown(path, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkerCapabilities(path); err == nil {
		t.Fatal("app-owned forged capability accepted")
	}
	os.Chown(path, 0, 0)
	if err := os.Rename(path, path+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+".real", path); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkerCapabilities(path); err == nil {
		t.Fatal("symlink capability accepted")
	}
	os.Remove(path)
	os.Rename(path+".real", path)
	os.Chmod(state, 0777)
	if _, err := readWorkerCapabilities(path); err == nil {
		t.Fatal("app-writable state accepted")
	}
	os.Chmod(state, 0700)
}

func TestApplyRechecksWorkerCapabilitiesBeforeQueueing(t *testing.T) {
	root := t.TempDir()
	m := NewManager(filepath.Join(root, "app"), func() string { return Repository })
	defer m.Close()
	m.ConfigureInstaller(filepath.Join(root, "queue"), filepath.Join(root, "state", "job.json"))
	m.status = Status{State: "available", Repository: Repository, LatestVersion: "9.0.0", Schema: 8, Compatible: true}
	m.plan = &releasePlan{Manifest: Manifest{Version: "9.0.0", Schema: 8, MigrateFrom: []int{7}}, Repository: Repository}
	if err := m.Apply("9.0.0"); err == nil || !strings.Contains(err.Error(), upgradeWorkerMessage) {
		t.Fatalf("direct apply bypassed worker gate: %v", err)
	}
	if s := m.Status(); s.Compatible || s.CanInstall || s.State != "incompatible" || !strings.Contains(s.Message, upgradeWorkerMessage) {
		t.Fatalf("status hid missing worker: %+v", s)
	}
	m.readCapabilities = func(string) (workerCapabilities, error) { return currentCapabilities(), nil }
	if s := m.Status(); !s.Compatible || !s.CanInstall || s.State != "available" {
		t.Fatalf("updated worker unavailable: %+v", s)
	}
	if err := m.Apply("9.0.0"); err != nil {
		t.Fatal(err)
	}
}
