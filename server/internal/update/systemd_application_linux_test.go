//go:build linux

package update

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Exercise the actual application's startup migration under a restricted,
// non-root unit. These preview binaries are never distributed as release assets.
// Signing and strict VCS health identity have their own installer tests.
func TestSystemdRealApplicationSchemaMigrationAndRollback(t *testing.T) {
	if os.Getenv("MUSIC_SERVER_SYSTEMD_TEST") != "1" {
		t.Skip("requires isolated root systemd runner")
	}
	if os.Geteuid() != 0 {
		t.Fatal("requires root to install isolated unit")
	}
	oldBinary, newBinary := os.Getenv("MUSIC_SERVER_SCHEMA7_BINARY"), os.Getenv("MUSIC_SERVER_SCHEMA8_BINARY")
	if oldBinary == "" || newBinary == "" {
		t.Fatal("real schema 7 and 8 preview binaries are required")
	}
	oldVersion, newVersion := os.Getenv("MUSIC_SERVER_SCHEMA7_VERSION"), os.Getenv("MUSIC_SERVER_SCHEMA8_VERSION")
	if oldVersion == "" || newVersion == "" {
		t.Fatal("preview binary versions are required")
	}
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Chmod(root, 0755))
	must(os.Chmod(filepath.Dir(root), 0755))
	account := fmt.Sprintf("musicserver-app-test-%d", os.Getpid())
	out, err := exec.Command("useradd", "--system", "--no-create-home", account).CombinedOutput()
	if err != nil {
		t.Fatalf("create user: %v %s", err, out)
	}
	defer exec.Command("userdel", account).Run()
	identity, err := user.Lookup(account)
	must(err)
	uid, err := strconv.Atoi(identity.Uid)
	must(err)
	gid, err := strconv.Atoi(identity.Gid)
	must(err)
	dataDir := filepath.Join(root, "data")
	media := filepath.Join(dataDir, "media")
	must(os.MkdirAll(media, 0700))
	must(os.Chown(dataDir, uid, gid))
	must(os.Chown(media, uid, gid))
	database := filepath.Join(dataDir, "library.sqlite")
	releases := filepath.Join(root, "releases")
	must(os.Mkdir(releases, 0755))
	old := Manifest{Version: oldVersion, Commit: strings.Repeat("1", 40), Schema: 7}
	next := Manifest{Version: newVersion, Commit: strings.Repeat("2", 40), Schema: 8, MigrateFrom: []int{7}}
	prepared := map[string]string{}
	prepare := func(_ context.Context, v string) (Manifest, string, error) {
		m, binary := next, newBinary
		if v == old.Version {
			m, binary = old, oldBinary
		}
		if dir := prepared[v]; dir != "" {
			return m, dir, nil
		}
		dir := filepath.Join(releases, v)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return m, "", err
		}
		bytes, err := os.ReadFile(binary)
		if err != nil {
			return m, "", err
		}
		extractRestricted(t, testReleaseArchive(t, bytes, m), dir, m)
		prepared[v] = dir
		return m, dir, nil
	}
	_, previous, err := prepare(context.Background(), old.Version)
	must(err)
	must(os.Symlink(previous, filepath.Join(root, "current")))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	address := l.Addr().String()
	must(l.Close())
	unit := fmt.Sprintf("musicserver-app-update-test-%d.service", os.Getpid())
	unitPath := filepath.Join("/etc/systemd/system", unit)
	command := func(action string) func(context.Context) error {
		return func(ctx context.Context) error {
			out, err := exec.CommandContext(ctx, "systemctl", action, unit).CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s: %w %s", action, err, out)
			}
			return nil
		}
	}
	defer func() {
		_ = command("stop")(context.Background())
		_ = os.Remove(unitPath)
		_ = exec.Command("systemctl", "daemon-reload").Run()
	}()
	body := fmt.Sprintf("[Service]\nUser=%s\nExecStart=%s/current/musicserver -addr %s -data %s -media-root %s -no-scan\nProtectSystem=strict\nNoNewPrivileges=true\nReadWritePaths=%s\n", account, root, address, database, media, dataDir)
	must(os.WriteFile(unitPath, []byte(body), 0644))
	out, err = exec.Command("systemctl", "daemon-reload").CombinedOutput()
	if err != nil {
		t.Fatalf("reload: %v %s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	health := func(ctx context.Context, m Manifest) error {
		deadline := time.NewTimer(8 * time.Second)
		defer deadline.Stop()
		client := http.Client{Timeout: time.Second}
		for {
			r, err := client.Get("http://" + address + "/api/v2/health")
			if err == nil {
				var actual struct {
					Status, Version string
					Schema          int
				}
				err = json.NewDecoder(r.Body).Decode(&actual)
				r.Body.Close()
				if err == nil && r.StatusCode == 200 && actual.Status == "ok" && actual.Version == m.Version && actual.Schema == m.Schema {
					return nil
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return errors.New("actual application did not become healthy")
			case <-time.After(30 * time.Millisecond):
			}
		}
	}
	i := Installer{Config: InstallConfig{Root: root, State: filepath.Join(root, "state"), Database: database}, Stop: command("stop"), Start: command("start"), Healthy: health, Prepare: prepare, Backup: func(p string) error { return backupDatabase(database, p) }, Restore: func(p string) error { return restoreDatabase(p, database) }, CheckDatabase: verifyDatabaseSchema}
	must(i.Start(ctx))
	must(health(ctx, old))
	must(i.Stop(ctx))
	db, err := sql.Open("sqlite", database)
	must(err)
	_, err = db.Exec(`CREATE TABLE legacy_application_marker(id INTEGER PRIMARY KEY,value TEXT NOT NULL); INSERT INTO legacy_application_marker VALUES(1,'preserve old data'); INSERT INTO settings VALUES('opaque-upgrade-marker','{"keep":true}');`)
	must(err)
	must(db.Close())
	must(i.Start(ctx))
	must(health(ctx, old))
	checkData := func(schema int) {
		t.Helper()
		must(verifyDatabaseSchema(database, schema))
		db, err := sql.Open("sqlite", database)
		must(err)
		defer db.Close()
		var marker, setting string
		must(db.QueryRow("SELECT value FROM legacy_application_marker WHERE id=1").Scan(&marker))
		must(db.QueryRow("SELECT value FROM settings WHERE key='opaque-upgrade-marker'").Scan(&setting))
		if marker != "preserve old data" || setting != `{"keep":true}` {
			t.Fatal("legacy data changed")
		}
		if schema == 8 {
			var n int
			must(db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('tags','tag_groups','track_tags','saved_track_filters','track_tag_imports','tag_batches')").Scan(&n))
			if n != 6 {
				t.Fatal("real tag migration was not executed", n)
			}
			must(db.QueryRow("SELECT count(*) FROM schema_migrations WHERE from_version=7 AND to_version=8").Scan(&n))
			if n != 1 {
				t.Fatal("migration is not idempotent", n)
			}
		}
	}
	// Force failure only after the actual candidate has migrated and served health.
	i.Healthy = func(ctx context.Context, m Manifest) error {
		if err := health(ctx, m); err != nil {
			return err
		}
		if m.Schema == 8 {
			checkData(8)
			return errors.New("injected failure after real migration")
		}
		return nil
	}
	if err := i.Install(ctx, Request{ID: strings.Repeat("d", 32), Version: newVersion}); err == nil {
		t.Fatal("injected failure accepted")
	}
	checkData(7)
	must(health(ctx, old))
	i.Healthy = health
	must(i.Install(ctx, Request{ID: strings.Repeat("a", 32), Version: newVersion}))
	checkData(8)
	must(i.Stop(ctx))
	must(i.Start(ctx))
	must(health(ctx, next))
	checkData(8)
	// A worker interrupted after switching must restore the matching old DB,
	// not start the old schema 7 process against a migrated schema 8 database.
	job, err := i.loadJob()
	must(err)
	job.Previous = previous
	job.RestoreRequired = true
	must(i.save(&job, "starting", "simulate interruption after real migration"))
	must(i.Recover(ctx))
	checkData(7)
	must(health(ctx, old))
}
