package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testPackageName() string {
	extension := ".tar.gz"
	if runtime.GOOS == "windows" {
		extension = ".zip"
	}
	return "musicserver-" + runtime.GOOS + "-" + runtime.GOARCH + extension
}

func testManager(t *testing.T, tag, digest string, payload []byte) (*Manager, *httptest.Server) {
	t.Helper()
	name := testPackageName()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("MUSIC_SERVER_GITHUB_TOKEN") != "" && r.Header.Get("Authorization") != "Bearer fixture-token" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/repos/example/musicserver/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://github.com/example/musicserver/releases/tag/%s","assets":[{"name":%q,"url":"http://%s/asset","browser_download_url":"http://%s/browser","size":%d,"digest":%q}]}`, tag, tag, name, r.Host, r.Host, len(payload), digest)
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
	return manager, server
}

func TestCheckDownloadsVerifiedRelease(t *testing.T) {
	payload := []byte("test release package")
	sum := sha256.Sum256(payload)
	manager, _ := testManager(t, "v9.0.0", "sha256:"+hex.EncodeToString(sum[:]), payload)
	if err := manager.Check(context.Background()); err != nil {
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
	if err := manager.Check(context.Background()); err == nil {
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
