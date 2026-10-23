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
	}{{"../outside", tar.TypeReg, "x"}, {"/etc/x", tar.TypeReg, "x"}, {"musicserver", tar.TypeSymlink, ""}, {"build.json", tar.TypeReg, `{"version":"0.0.0"}`}} {
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
