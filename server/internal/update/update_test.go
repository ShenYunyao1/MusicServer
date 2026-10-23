package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"musicserver/internal/version"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testPackageName() string {
	extension := ".tar.gz"
	if runtime.GOOS == "windows" {
		extension = ".zip"
	}
	return "musicserver-" + runtime.GOOS + "-" + runtime.GOARCH + extension
}

func testManager(t *testing.T, tag, digest string, payload []byte, schemas ...int) (*Manager, *httptest.Server) {
	t.Helper()
	name := testPackageName()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	schema := version.Schema
	if len(schemas) > 0 {
		schema = schemas[0]
	}
	manifest, _ := json.Marshal(Manifest{Format: 1, Version: strings.TrimPrefix(tag, "v"), Commit: strings.Repeat("a", 40), Schema: schema, Packages: []Package{{Name: name, OS: runtime.GOOS, Arch: runtime.GOARCH, Size: int64(len(payload)), SHA256: strings.TrimPrefix(digest, "sha256:")}}})
	signature := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, manifest)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("MUSIC_SERVER_GITHUB_TOKEN") != "" && r.Header.Get("Authorization") != "Bearer fixture-token" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/repos/example/musicserver/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://github.com/example/musicserver/releases/tag/%s","assets":[{"name":%q,"url":"http://%s/asset","browser_download_url":"http://%s/browser","size":%d,"digest":%q},{"name":"release-manifest.json","browser_download_url":"http://%s/manifest","size":%d},{"name":"release-manifest.json.sig","browser_download_url":"http://%s/signature","size":%d}]}`, tag, tag, name, r.Host, r.Host, len(payload), digest, r.Host, len(manifest), r.Host, len(signature))
		case "/manifest":
			w.Write(manifest)
		case "/signature":
			w.Write(signature)
		case "/asset":
			if os.Getenv("MUSIC_SERVER_GITHUB_TOKEN") == "" {
				http.Error(w, "private asset", http.StatusForbidden)
				return
			}
			_, _ = w.Write(payload)
		case "/browser":
			if os.Getenv("MUSIC_SERVER_GITHUB_TOKEN") != "" {
				http.Error(w, "private asset", http.StatusForbidden)
				return
			}
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	manager := NewManager(t.TempDir(), func() string { return "https://github.com/example/musicserver" })
	manager.apiBase = server.URL
	manager.allowHTTP = true
	manager.publicKey = pub
	return manager, server
}

func TestCheckDownloadsVerifiedRelease(t *testing.T) {
	payload := []byte("test release package")
	sum := sha256.Sum256(payload)
	manager, _ := testManager(t, "v9.0.0", "sha256:"+hex.EncodeToString(sum[:]), payload)
	if err := manager.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	if status.State != "downloaded" || status.LatestVersion != "9.0.0" || status.PackagePath == "" {
		t.Fatalf("unexpected update status: %+v", status)
	}
	if filepath.Base(status.PackagePath) != testPackageName() || !strings.Contains(status.PackagePath, "9.0.0") {
		t.Fatalf("unexpected package path: %s", status.PackagePath)
	}
	actual, err := os.ReadFile(status.PackagePath)
	if err != nil || string(actual) != string(payload) {
		t.Fatalf("downloaded package = %q, %v", actual, err)
	}
	if err := manager.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRejectsBadDigest(t *testing.T) {
	manager, _ := testManager(t, "9.0.0", "sha256:"+strings.Repeat("0", 64), []byte("bad package"))
	if err := manager.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Prepare(context.Background()); err == nil {
		t.Fatal("expected checksum failure")
	}
	if manager.Status().State != "error" || manager.Status().PackagePath != "" {
		t.Fatalf("bad package was accepted: %+v", manager.Status())
	}
}

func TestPrivateReleaseUsesTokenOnlyForGitHubAPI(t *testing.T) {
	t.Setenv("MUSIC_SERVER_GITHUB_TOKEN", "fixture-token")
	payload := []byte("private release package")
	sum := sha256.Sum256(payload)
	manager, _ := testManager(t, "9.0.0", "sha256:"+hex.EncodeToString(sum[:]), payload)
	if err := manager.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.Status().State != "downloaded" {
		t.Fatalf("private download failed: %+v", manager.Status())
	}
}

func TestCheckIgnoresCurrentVersion(t *testing.T) {
	manager, _ := testManager(t, "v0.6.2", "sha256:"+strings.Repeat("0", 64), []byte("old package"))
	if err := manager.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.Status().State != "current" || manager.Status().PackagePath != "" {
		t.Fatalf("current release triggered download: %+v", manager.Status())
	}
}

func TestRepositoryValidation(t *testing.T) {
	for _, invalid := range []string{"http://github.com/example/repo", "https://github.com/example/repo/tree/main", "https://evil.invalid/example/repo", "https://github.com/example/repo?x=1"} {
		if err := ValidateRepository(invalid); err == nil {
			t.Fatalf("accepted invalid repository %q", invalid)
		}
	}
	if err := ValidateRepository("https://github.com/example/musicserver"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRefusesIncompatibleSchemaWithoutDownload(t *testing.T) {
	sum := sha256.Sum256([]byte("future schema"))
	m, _ := testManager(t, "9.0.0", "sha256:"+hex.EncodeToString(sum[:]), []byte("future schema"), 999)
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := m.Status()
	if s.State != "incompatible" || s.Compatible || s.PackagePath != "" {
		t.Fatalf("incompatible update accepted: %+v", s)
	}
	if err := m.Prepare(context.Background()); err == nil {
		t.Fatal("downloaded incompatible release")
	}
}

func TestCheckRefusesUnsignedMetadata(t *testing.T) {
	sum := sha256.Sum256([]byte("package"))
	m, _ := testManager(t, "9.0.0", "sha256:"+hex.EncodeToString(sum[:]), []byte("package"))
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	m.publicKey = other
	if err := m.Check(context.Background()); err == nil {
		t.Fatal("unsigned release accepted")
	}
	if m.Status().PackagePath != "" {
		t.Fatal("downloaded untrusted release")
	}
}

func TestDownloadDoesNotClaimFinalBackup(t *testing.T) {
	payload := []byte("new package")
	sum := sha256.Sum256(payload)
	manager, _ := testManager(t, "9.0.0", "sha256:"+hex.EncodeToString(sum[:]), payload)
	if err := manager.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.Status().BackupPath != "" || manager.Status().State != "downloaded" {
		t.Fatalf("missing upgrade backup: %+v", manager.Status())
	}
}

func TestCloseCancelsAndWaitsForUpdate(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer server.Close()
	manager := NewManager(t.TempDir(), func() string { return "https://github.com/example/musicserver" })
	manager.apiBase, manager.allowHTTP = server.URL, true
	if !manager.Trigger() {
		t.Fatal("update did not start")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("update request did not start")
	}
	closed := make(chan struct{})
	go func() { manager.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("close did not cancel update")
	}
	if manager.Trigger() {
		t.Fatal("closed updater started another job")
	}
}
