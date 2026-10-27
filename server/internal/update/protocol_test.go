package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestRejectsTamperingAndWrongKey(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	m := Manifest{Format: 1, Version: "9.0.0", Commit: strings.Repeat("a", 40), Schema: 7}
	data, _ := json.Marshal(m)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, data)))
	if _, err := VerifyManifest(data, sig, public); err != nil {
		t.Fatal(err)
	}
	for _, alter := range [][]byte{append(data, ' '), []byte(`{"format":1}`)} {
		if _, err := VerifyManifest(alter, sig, public); err == nil {
			t.Fatal("tampered manifest accepted")
		}
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := VerifyManifest(data, sig, other); err == nil {
		t.Fatal("wrong signing key accepted")
	}
}

func TestArchiveRejectsTraversalLinksAndWrongIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    byte
		content string
	}{{"../outside", tar.TypeReg, "x"}, {"/etc/x", tar.TypeReg, "x"}, {"musicserver", tar.TypeSymlink, ""}, {"musicserver-updater", tar.TypeSymlink, ""}, {"build.json", tar.TypeReg, `{"version":"0.0.0"}`}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "package.tar.gz")
			f, _ := os.Create(path)
			gz := gzip.NewWriter(f)
			w := tar.NewWriter(gz)
			w.WriteHeader(&tar.Header{Name: tc.name, Typeflag: tc.kind, Size: int64(len(tc.content)), Linkname: "/etc/passwd"})
			w.Write([]byte(tc.content))
			w.Close()
			gz.Close()
			f.Close()
			dest := filepath.Join(root, "extract")
			os.Mkdir(dest, 0700)
			if err := ExtractRelease(path, dest, Manifest{Version: "9.0.0"}); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestArchiveRequiresSignedMigrationIdentityAndUniqueUpdater(t *testing.T) {
	expected := Manifest{Version: "9.0.0", Commit: strings.Repeat("a", 40), Schema: 8, MigrateFrom: []int{7}}
	for _, tc := range []struct {
		name      string
		from      []int
		duplicate bool
		valid     bool
	}{
		{"matching", []int{7}, false, true},
		{"missing declaration", nil, false, false},
		{"extra declaration", []int{7, 7}, false, false},
		{"duplicate updater", []int{7}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "release.tar.gz")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(f)
			tr := tar.NewWriter(gz)
			build := expected
			build.MigrateFrom = tc.from
			identity, _ := json.Marshal(build)
			names := []string{"musicserver", "build.json", "musicserver-updater"}
			if tc.duplicate {
				names = append(names, "musicserver-updater")
			}
			for _, name := range names {
				data := []byte("executable")
				if name == "build.json" {
					data = identity
				}
				if err = tr.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(data))}); err != nil {
					t.Fatal(err)
				}
				if _, err = tr.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			for _, close := range []func() error{tr.Close, gz.Close, f.Close} {
				if err = close(); err != nil {
					t.Fatal(err)
				}
			}
			dest := filepath.Join(root, "extract")
			if err = os.Mkdir(dest, 0700); err != nil {
				t.Fatal(err)
			}
			err = ExtractRelease(path, dest, expected)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
			if tc.valid {
				if data, err := os.ReadFile(filepath.Join(dest, "musicserver-updater")); err != nil || string(data) != "executable" {
					t.Fatalf("helper missing: %q %v", data, err)
				}
			}
		})
	}
}

func TestHealthRequiresCommitAndSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok","version":"9.0.0","commit":"wrong","schema":7}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := HealthCheck(ctx, server.URL, Manifest{Version: "9.0.0", Commit: strings.Repeat("a", 40), Schema: 7}); err == nil {
		t.Fatal("health accepted wrong commit")
	}
}

func TestInstallUnavailableOnUnconfiguredServer(t *testing.T) {
	m := NewManager(t.TempDir(), func() string { return Repository })
	defer m.Close()
	if err := m.Apply("9.0.0"); err == nil {
		t.Fatal("unconfigured server accepted install")
	}
}

func TestStatusSurvivesRestartAndMergesWorkerResult(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "job.json")
	m := NewManager(root, func() string { return Repository })
	m.ConfigureInstaller("queue", state)
	m.finish(Status{Repository: Repository, State: "queued", JobID: strings.Repeat("a", 32), CurrentVersion: "0.13.0"}, nil)
	m.Close()
	m = NewManager(root, func() string { return Repository })
	defer m.Close()
	m.ConfigureInstaller("queue", state)
	atomicJSON(state, Job{ID: strings.Repeat("a", 32), Phase: "rolled_back", Message: "health failed", Backup: "database backup"}, 0644)
	s := m.Status()
	if s.State != "rolled_back" || s.Message != "health failed" || s.BackupPath != "database backup" {
		t.Fatalf("lost job: %+v", s)
	}
}
