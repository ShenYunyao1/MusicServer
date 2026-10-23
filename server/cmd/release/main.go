// Command release creates fixed-name distribution packages and a signed manifest.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"musicserver/internal/update"
	"musicserver/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	input := flag.String("input", "build", "compiled binaries directory")
	output := flag.String("output", "build/packages", "fixed release output directory")
	sign := flag.Bool("sign", false, "sign with UPDATE_SIGNING_KEY (Actions only)")
	show := flag.Bool("version", false, "print release version")
	flag.Parse()
	if *show {
		fmt.Println(version.Version)
		return nil
	}
	commit, modified := version.Source()
	if commit == "unknown" || modified {
		return fmt.Errorf("release packaging requires a clean, committed checkout")
	}
	if err := os.MkdirAll(*output, 0755); err != nil {
		return err
	}
	m := update.Manifest{Format: 1, Version: version.Version, Commit: commit, Schema: version.Schema}
	identity, _ := json.MarshalIndent(m, "", "  ")
	deploy, err := filepath.Glob("deploy/*")
	if err != nil {
		return err
	}
	files := map[string][]byte{"build.json": identity}
	for _, path := range append([]string{"README.md"}, deploy...) {
		info, e := os.Stat(path)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		files[filepath.ToSlash(path)] = data
	}
	// Sort archive members for reproducible membership; runtime directories are never inputs.
	for _, platform := range []string{"windows", "linux"} {
		entries := map[string][]byte{}
		for k, v := range files {
			entries[k] = v
		}
		names := []string{"musicserver"}
		if platform == "windows" {
			names = []string{"musicserver.exe"}
		} else {
			names = append(names, "musicserver-updater")
		}
		for _, name := range names {
			info, e := buildinfo.ReadFile(filepath.Join(*input, name))
			if e != nil {
				return e
			}
			revision, dirty := "", ""
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					revision = setting.Value
				}
				if setting.Key == "vcs.modified" {
					dirty = setting.Value
				}
			}
			if revision != commit || dirty != "false" {
				return fmt.Errorf("%s is stale or built from uncommitted changes; rebuild from this clean commit", name)
			}
			data, e := os.ReadFile(filepath.Join(*input, name))
			if e != nil {
				return e
			}
			entries[name] = data
		}
		name := update.PackageName(platform, "amd64")
		path := filepath.Join(*output, name)
		if err = archive(path, platform, entries); err != nil {
			return err
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(data)
		m.Packages = append(m.Packages, update.Package{Name: name, OS: platform, Arch: "amd64", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(*output, update.ManifestName), data, 0644); err != nil {
		return err
	}
	signaturePath := filepath.Join(*output, update.ManifestName+".sig")
	if *sign {
		seed, e := base64.StdEncoding.DecodeString(os.Getenv("UPDATE_SIGNING_KEY"))
		if e != nil || len(seed) != ed25519.SeedSize {
			return fmt.Errorf("invalid Actions UPDATE_SIGNING_KEY seed")
		}
		key := ed25519.NewKeyFromSeed(seed)
		public, _ := base64.StdEncoding.DecodeString(update.ReleasePublicKey)
		if !key.Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(public)) {
			return fmt.Errorf("signing secret differs from the pinned release public key")
		}
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(key, data))
		if err = os.WriteFile(signaturePath, []byte(signature), 0644); err != nil {
			return err
		}
	} else {
		if e := os.Remove(signaturePath); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	fmt.Println("PACKAGING SUCCEEDED:", *output)
	if !*sign {
		fmt.Println("Unsigned local packages: automatic installers will reject them.")
	}
	return nil
}
