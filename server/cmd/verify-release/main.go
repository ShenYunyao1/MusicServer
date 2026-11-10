// Verify downloaded release assets against the public key in this trusted checkout.
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"pikmelo-server/internal/update"
	"slices"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dir := flag.String("dir", ".", "downloaded release directory")
	flag.Parse()
	var identity *update.Manifest
	for _, name := range []string{update.DistributionManifestName, update.ManifestName} {
		if _, err := os.Stat(filepath.Join(*dir, name)); os.IsNotExist(err) {
			continue
		}
		manifest, err := verifyManifest(*dir, name)
		if err != nil {
			return err
		}
		if identity != nil && (identity.Version != manifest.Version || identity.Commit != manifest.Commit || identity.Schema != manifest.Schema || !slices.Equal(identity.MigrateFrom, manifest.MigrateFrom)) {
			return fmt.Errorf("branded and compatibility manifests have different release identities")
		}
		identity = &manifest
	}
	if identity == nil {
		return fmt.Errorf("no release manifest found")
	}
	return nil
}

func verifyManifest(dir, name string) (update.Manifest, error) {
	var empty update.Manifest
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return empty, err
	}
	sig, err := os.ReadFile(filepath.Join(dir, name+".sig"))
	if err != nil {
		return empty, err
	}
	key, _ := base64.StdEncoding.DecodeString(update.ReleasePublicKey)
	m, err := update.VerifyManifest(data, sig, key)
	if err != nil {
		return empty, err
	}
	count := 0
	for _, p := range m.Packages {
		f, e := os.Open(filepath.Join(dir, p.Name))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return empty, e
		}
		sum := sha256.New()
		n, e := io.Copy(sum, io.LimitReader(f, p.Size+1))
		f.Close()
		if e != nil {
			return empty, e
		}
		if n != p.Size || hex.EncodeToString(sum.Sum(nil)) != p.SHA256 {
			return empty, fmt.Errorf("size or SHA-256 mismatch: %s", p.Name)
		}
		fmt.Println("Verified:", p.Name)
		count++
	}
	if count == 0 {
		return empty, fmt.Errorf("no package found to verify")
	}
	if count != len(m.Packages) {
		fmt.Printf("Verified signed manifest %s (%s), schema %d; verified %d of %d declared packages (partial verification)\n", m.Version, m.Commit, m.Schema, count, len(m.Packages))
	} else {
		fmt.Printf("Verified signed release %s (%s), schema %d; all %d packages verified\n", m.Version, m.Commit, m.Schema, count)
	}
	return m, nil
}
