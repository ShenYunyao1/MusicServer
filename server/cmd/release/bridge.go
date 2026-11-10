package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"pikmelo-server/internal/update"
)

// A schema-9 release needs an older signed schema-8 bridge. Branch artifacts
// make no assertion that this formal upgrade path has already been published.
func validateBridgeAssets(dir string, public []byte, target, tag, commit string) error {
	data, err := os.ReadFile(filepath.Join(dir, update.ManifestName))
	if err != nil {
		return fmt.Errorf("schema-9 release needs signed schema-8 bridge assets: %w", err)
	}
	sig, err := os.ReadFile(filepath.Join(dir, update.ManifestName+".sig"))
	if err != nil {
		return err
	}
	m, err := update.VerifyManifest(data, sig, public)
	if err != nil {
		return err
	}
	if m.Schema != 8 || tag != "v"+m.Version || commit != m.Commit || !bridgeBefore(m.Version, target) || !bridgeBefore("0.21.14", m.Version) {
		return fmt.Errorf("bridge must be a pinned schema-8 release newer than 0.21.14 and older than %s", target)
	}
	if len(m.Packages) != 2 {
		return fmt.Errorf("bridge needs both fixed-name amd64 packages")
	}
	for _, platform := range []string{"linux", "windows"} {
		p, err := m.Platform(platform, "amd64")
		if err != nil {
			return err
		}
		f, err := os.Open(filepath.Join(dir, p.Name))
		if err != nil {
			return err
		}
		hash := sha256.New()
		n, e := io.Copy(hash, io.LimitReader(f, p.Size+1))
		f.Close()
		if e != nil {
			return e
		}
		if n != p.Size || hex.EncodeToString(hash.Sum(nil)) != p.SHA256 {
			return fmt.Errorf("bridge package digest mismatch: %s", p.Name)
		}
	}
	// Inspect bounded metadata from the verified Linux archive without extracting
	// or executing package contents.
	f, err := os.Open(filepath.Join(dir, update.PackageName("linux", "amd64")))
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(io.LimitReader(gz, 512<<20))
	metadata := map[string][]byte{}
	for {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if h.Name != "build.json" && h.Name != "deploy/updater-capabilities.json" {
			continue
		}
		if h.Typeflag != tar.TypeReg || h.Size < 1 || h.Size > 65536 || metadata[h.Name] != nil {
			return fmt.Errorf("invalid bridge metadata")
		}
		metadata[h.Name], e = io.ReadAll(io.LimitReader(reader, 65537))
		if e != nil {
			return e
		}
	}
	var build update.Manifest
	if json.Unmarshal(metadata["build.json"], &build) != nil || build.Version != m.Version || build.Commit != m.Commit || build.Schema != 8 {
		return fmt.Errorf("bridge build identity mismatch")
	}
	return update.ValidateReleaseCapabilities(metadata["deploy/updater-capabilities.json"], m, 8, 9)
}

func bridgeBefore(a, b string) bool {
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	if len(x) != 3 || len(y) != 3 {
		return false
	}
	for i := range x {
		xi, e1 := strconv.ParseUint(x[i], 10, 64)
		yi, e2 := strconv.ParseUint(y[i], 10, 64)
		if e1 != nil || e2 != nil {
			return false
		}
		if xi != yi {
			return xi < yi
		}
	}
	return false
}
