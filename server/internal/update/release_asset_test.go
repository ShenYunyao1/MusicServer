package update

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// Packaging CI verifies the actual signed output, including accepted tar members and identity.
func TestPublishedLinuxArchive(t *testing.T) {
	archive := os.Getenv("MUSIC_SERVER_RELEASE_ARCHIVE")
	if archive == "" {
		t.Skip("enable after building signed release assets")
	}
	dir := filepath.Dir(archive)
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile(filepath.Join(dir, ManifestName+".sig"))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := base64.StdEncoding.DecodeString(ReleasePublicKey)
	m, err := VerifyManifest(data, sig, key)
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err = ExtractRelease(archive, dest, m); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"musicserver", "build.json"} {
		info, err := os.Lstat(filepath.Join(dest, name))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("missing extracted regular %s: %v", name, err)
		}
	}
	if _, err = os.Stat(filepath.Join(dest, "musicserver-updater")); !os.IsNotExist(err) {
		t.Fatal("main-program update must not replace the privileged installer")
	}
}
