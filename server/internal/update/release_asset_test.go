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
	// Stage the verified helper in the isolated release directory. Activation at
	// the independent systemd path is a separate explicit root operation.
	for _, name := range []string{"musicserver", "musicserver-updater", "build.json"} {
		info, err := os.Lstat(filepath.Join(dest, name))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("missing extracted regular %s: %v", name, err)
		}
	}
}
