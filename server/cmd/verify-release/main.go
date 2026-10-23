// Verify downloaded release assets against the public key in this trusted checkout.
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"musicserver/internal/update"
	"os"
	"path/filepath"
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
	data, err := os.ReadFile(filepath.Join(*dir, update.ManifestName))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(*dir, update.ManifestName+".sig"))
	if err != nil {
		return err
	}
	key, _ := base64.StdEncoding.DecodeString(update.ReleasePublicKey)
	m, err := update.VerifyManifest(data, sig, key)
	if err != nil {
		return err
	}
	count := 0
	for _, p := range m.Packages {
		f, e := os.Open(filepath.Join(*dir, p.Name))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		sum := sha256.New()
		n, e := io.Copy(sum, io.LimitReader(f, p.Size+1))
		f.Close()
		if e != nil {
			return e
		}
		if n != p.Size || hex.EncodeToString(sum.Sum(nil)) != p.SHA256 {
			return fmt.Errorf("size or SHA-256 mismatch: %s", p.Name)
		}
		fmt.Println("Verified:", p.Name)
		count++
	}
	if count == 0 {
		return fmt.Errorf("no package found to verify")
	}
	fmt.Printf("Verified signed release %s (%s), schema %d\n", m.Version, m.Commit, m.Schema)
	return nil
}
