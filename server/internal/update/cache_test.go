package update

import (
	"musicserver/internal/version"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheCleanupPreservesProtectedUnknownAndBusy(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, func() string { return "" })
	defer m.Close()
	m.status.LatestVersion = "99.0.0"
	for _, v := range []string{version.Version, "99.0.0", "0.0.1"} {
		os.Mkdir(filepath.Join(root, v), 0700)
		os.WriteFile(filepath.Join(root, v, PackageName("linux", "amd64")), []byte("archive"), 0600)
	}
	unknown := filepath.Join(root, "0.0.1", "personal.txt")
	os.WriteFile(unknown, []byte("keep"), 0600)
	m.status.State = "downloading"
	if _, err := m.CleanupCache(); err == nil {
		t.Fatal("cleaned active cache")
	}
	m.status.State = "available"
	freed, err := m.CleanupCache()
	if err != nil || freed != 7 {
		t.Fatalf("cleanup %d %v", freed, err)
	}
	for _, p := range []string{unknown, filepath.Join(root, version.Version, PackageName("linux", "amd64")), filepath.Join(root, "99.0.0", PackageName("linux", "amd64"))} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("protected file removed", p)
		}
	}
}
