package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"musicserver/internal/version"
)

const Repository = "https://github.com/ShenYunyao1/MusicServer"

// The corresponding private seed exists only in the repository's Actions secret.
const ReleasePublicKey = "y9th8IYGEvvdkvagc6sldsk8JopF1TDNhlqWGZPLXcw="
const ManifestName = "release-manifest.json"

type Package struct {
	Name   string `json:"name"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Format   int       `json:"format"`
	Version  string    `json:"version"`
	Commit   string    `json:"commit"`
	Schema   int       `json:"schema"`
	Packages []Package `json:"packages"`
}

func VerifyManifest(data, signature, key []byte) (Manifest, error) {
	var result Manifest
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signature)))
	if err != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, data, raw) {
		return result, errors.New("release manifest signature is invalid")
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	if result.Format != 1 || !versionTag.MatchString(result.Version) || strings.HasPrefix(result.Version, "v") || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(result.Commit) || result.Schema < 1 {
		return result, errors.New("invalid release manifest identity")
	}
	seen := map[string]bool{}
	for _, p := range result.Packages {
		if (p.OS != "windows" && p.OS != "linux") || (p.Arch != "amd64" && p.Arch != "arm64") || p.Name != PackageName(p.OS, p.Arch) || p.Size <= 0 || p.Size > maxPackageSize || seen[p.Name] {
			return result, errors.New("invalid release manifest package")
		}
		digest, e := hex.DecodeString(p.SHA256)
		if e != nil || len(digest) != 32 {
			return result, errors.New("invalid manifest package digest")
		}
		seen[p.Name] = true
	}
	return result, nil
}

func PackageName(platform, arch string) string {
	extension := ".tar.gz"
	if platform == "windows" {
		extension = ".zip"
	}
	return "musicserver-" + platform + "-" + arch + extension
}
func (m Manifest) Platform(platform, arch string) (Package, error) {
	for _, p := range m.Packages {
		if p.OS == platform && p.Arch == arch {
			return p, nil
		}
	}
	return Package{}, fmt.Errorf("release has no package for %s/%s", platform, arch)
}

func atomicJSON(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data, mode)
}
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Windows cannot rename over an existing file; update metadata is not an install journal there.
	if runtime.GOOS == "windows" {
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return syncDir(filepath.Dir(path))
	}
	return nil
}
func syncDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func compatible(manifest Manifest) bool { return manifest.Schema == version.Schema }
