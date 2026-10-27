//go:build linux

package update

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func testReleaseArchive(t *testing.T, binary []byte, manifest Manifest) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tr := tar.NewWriter(gz)
	identity, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"musicserver": binary, "musicserver-updater": []byte("verified updater"), "build.json": identity} {
		if err := tr.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0777, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	for _, close := range []func() error{tr.Close, gz.Close, file.Close} {
		if err := close(); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func extractRestricted(t *testing.T, archive, dest string, manifest Manifest) {
	t.Helper()
	identity, _ := json.Marshal(manifest)
	child := exec.Command(os.Args[0], "-test.run=^TestExtractReleaseWithRestrictedUmask$")
	child.Env = append(os.Environ(), "MUSIC_EXTRACT_CHILD=1", "MUSIC_EXTRACT_ARCHIVE="+archive, "MUSIC_EXTRACT_DEST="+dest, "MUSIC_EXTRACT_MANIFEST="+string(identity))
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("restricted-umask extraction: %v %s", err, out)
	}
}

func TestExtractReleaseWithRestrictedUmask(t *testing.T) {
	if os.Getenv("MUSIC_EXTRACT_CHILD") == "1" {
		syscall.Umask(0027)
		var manifest Manifest
		if err := json.Unmarshal([]byte(os.Getenv("MUSIC_EXTRACT_MANIFEST")), &manifest); err != nil {
			t.Fatal(err)
		}
		dest := os.Getenv("MUSIC_EXTRACT_DEST")
		if err := ExtractRelease(os.Getenv("MUSIC_EXTRACT_ARCHIVE"), dest, manifest); err != nil {
			t.Fatal(err)
		}
		for name, mode := range map[string]os.FileMode{"musicserver": 0755, "musicserver-updater": 0755, "build.json": 0644} {
			info, err := os.Stat(filepath.Join(dest, name))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != mode {
				t.Fatalf("%s mode=%04o, want %04o independent of worker umask", name, info.Mode().Perm(), mode)
			}
		}
		if data, err := os.ReadFile(filepath.Join(dest, "musicserver-updater")); err != nil || string(data) != "verified updater" {
			t.Fatalf("updater content differs: %q %v", data, err)
		}
		return
	}
	manifest := Manifest{Version: "9.0.0", Commit: "1111111111111111111111111111111111111111", Schema: 7}
	extractRestricted(t, testReleaseArchive(t, []byte("test executable"), manifest), t.TempDir(), manifest)
}
