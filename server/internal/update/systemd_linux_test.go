//go:build linux

package update

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSystemdInstallAndRollback(t *testing.T) {
	if os.Getenv("MUSIC_SERVER_SYSTEMD_TEST") != "1" {
		t.Skip("enable on an isolated Linux systemd runner")
	}
	if os.Geteuid() != 0 {
		t.Fatal("integration fixture requires root to install its isolated unit")
	}
	root := t.TempDir()
	os.Chmod(root, 0755)
	os.Chmod(filepath.Dir(root), 0755)
	account := fmt.Sprintf("musicserver-test-%d", os.Getpid())
	if out, err := exec.Command("useradd", "--system", "--no-create-home", account).CombinedOutput(); err != nil {
		t.Fatalf("create fixture user: %v %s", err, out)
	}
	defer exec.Command("userdel", account).Run()
	identity, err := user.Lookup(account)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(identity.Uid)
	gid, _ := strconv.Atoi(identity.Gid)
	releases := filepath.Join(root, "releases")
	os.Mkdir(releases, 0755)
	unit := fmt.Sprintf("musicserver-update-test-%d.service", os.Getpid())
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
		os.Remove(unitPath)
		exec.Command("systemctl", "daemon-reload").Run()
	}()
	binary := filepath.Join(root, "fixture")
	if prebuilt := os.Getenv("MUSIC_SERVER_TEST_FIXTURE"); prebuilt != "" {
		data, err := os.ReadFile(prebuilt)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(binary, data, 0755); err != nil {
			t.Fatal(err)
		}
	} else {
		cmd := exec.Command("go", "build", "-o", binary, "../../tests/fixtures/update-service")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture build: %v %s", err, out)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	dataDir := filepath.Join(root, "data")
	os.Mkdir(dataDir, 0700)
	os.Chown(dataDir, uid, gid)
	database := filepath.Join(dataDir, "library.sqlite")
	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE marker(value TEXT); INSERT INTO marker VALUES('original');")
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	os.Chown(database, uid, gid)
	old := Manifest{Version: "0.13.0", Commit: strings.Repeat("1", 40), Schema: 7}
	prepare := func(_ context.Context, v string) (Manifest, string, error) {
		manifest := Manifest{Version: v, Commit: strings.Repeat("2", 40), Schema: 7}
		if v == old.Version {
			manifest = old
		}
		dir := filepath.Join(releases, v)
		os.Mkdir(dir, 0755)
		data, e := os.ReadFile(binary)
		if e != nil {
			return Manifest{}, "", e
		}
		if e = os.WriteFile(filepath.Join(dir, "musicserver"), data, 0755); e != nil {
			return Manifest{}, "", e
		}
		e = atomicJSON(filepath.Join(dir, "build.json"), manifest, 0644)
		return manifest, dir, e
	}
	_, previous, err := prepare(context.Background(), old.Version)
	if err != nil {
		t.Fatal(err)
	}
	os.Symlink(previous, filepath.Join(root, "current"))
	body := fmt.Sprintf("[Service]\nUser=%s\nExecStart=%s/current/musicserver\nEnvironment=TEST_DATABASE=%s\nEnvironment=TEST_ADDRESS=%s\nProtectSystem=strict\nNoNewPrivileges=true\nReadWritePaths=%s\n", account, root, database, address, dataDir)
	if err = os.WriteFile(unitPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		t.Fatalf("daemon reload: %v %s", err, out)
	}
	i := Installer{Config: InstallConfig{Root: root, State: filepath.Join(root, "state"), Database: database}, Stop: command("stop"), Start: command("start"), Prepare: prepare, Backup: func(p string) error { return backupDatabase(database, p) }, Restore: func(p string) error { return restoreDatabase(p, database) }}
	i.Healthy = func(ctx context.Context, m Manifest) error {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return HealthCheck(ctx, "http://"+address+"/api/v2/health", m)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err = i.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err = i.Healthy(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err = i.Install(ctx, Request{ID: strings.Repeat("a", 32), Version: "9.0.0"}); err != nil {
		t.Fatal(err)
	}
	job, _ := i.loadJob()
	if job.Phase != "completed" {
		t.Fatalf("install failed %+v", job)
	}
	if err = i.Install(ctx, Request{ID: strings.Repeat("b", 32), Version: "10.0.0"}); err == nil {
		t.Fatal("bad health was accepted")
	}
	job, _ = i.loadJob()
	if job.Phase != "rolled_back" {
		t.Fatalf("did not recover %+v", job)
	}
	db, err = sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	var marker string
	err = db.QueryRow("SELECT value FROM marker").Scan(&marker)
	db.Close()
	if err != nil || marker != "original" {
		t.Fatalf("database rollback: %s %v", marker, err)
	}
	// Crash after switching: a fresh worker must restore the preceding program and database.
	job.ID = strings.Repeat("c", 32)
	job.Previous = filepath.Join(releases, "9.0.0")
	job.Backup = filepath.Join(i.Config.State, "backups", strings.Repeat("b", 32)+".sqlite")
	job.RestoreRequired = true
	if err = i.save(&job, "starting", "simulate worker crash"); err != nil {
		t.Fatal(err)
	}
	if err = replaceLink(root, filepath.Join(releases, "10.0.0")); err != nil {
		t.Fatal(err)
	}
	if err = i.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	job, _ = i.loadJob()
	if job.Phase != "rolled_back" {
		t.Fatal(errors.New("interrupted install was not recovered"))
	}
}
