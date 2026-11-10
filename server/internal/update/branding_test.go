package update

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pikmelo-server/internal/version"
)

func TestPikmeloAndLegacySignedPackages(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"musicserver", "pikmelo-server"} {
		t.Run(prefix, func(t *testing.T) {
			manifest := Manifest{Format: 1, Version: "9.0.0", Commit: strings.Repeat("a", 40), Schema: 9,
				Packages: []Package{{Name: prefix + "-linux-amd64.tar.gz", OS: "linux", Arch: "amd64", Size: 1, SHA256: strings.Repeat("a", 64)}}}
			data, _ := json.Marshal(manifest)
			sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, data)))
			if _, err := VerifyManifest(data, sig, public); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPikmeloOfficialRepositoryAliases(t *testing.T) {
	for _, repository := range []string{Repository, LegacyRepository} {
		if !officialRepository(repository) || ValidateRepository(repository) != nil {
			t.Fatalf("official repository rejected: %s", repository)
		}
	}
	for _, repository := range []string{"https://github.com/other/Pikmelo-Server", Repository + "-other", Repository + "/../Other", ""} {
		if officialRepository(repository) {
			t.Fatalf("untrusted repository accepted for automatic installation: %s", repository)
		}
	}
}

func TestPikmeloArchiveMaintainsExecutableAliases(t *testing.T) {
	expected := Manifest{Format: 1, Version: "9.0.0", Commit: strings.Repeat("a", 40), Schema: 9, MigrateFrom: []int{8}}
	identity, _ := json.Marshal(expected)
	for _, prefix := range []string{"musicserver", version.BinaryName} {
		t.Run(prefix, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "release.tar.gz")
			file, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(file)
			writer := tar.NewWriter(gz)
			for name, data := range map[string][]byte{"build.json": identity, prefix: []byte("server fixture"), prefix + "-updater": []byte("worker fixture")} {
				if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(data))}); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			for _, close := range []func() error{writer.Close, gz.Close, file.Close} {
				if err := close(); err != nil {
					t.Fatal(err)
				}
			}
			dest := t.TempDir()
			if err := ExtractRelease(archive, dest, expected); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"musicserver", version.BinaryName, "musicserver-updater", version.UpdaterName} {
				info, err := os.Lstat(filepath.Join(dest, name))
				if err != nil || !info.Mode().IsRegular() {
					t.Fatalf("missing regular executable alias %s: %v", name, err)
				}
				data, err := os.ReadFile(filepath.Join(dest, name))
				want := "server fixture"
				if strings.HasSuffix(name, "-updater") {
					want = "worker fixture"
				}
				if err != nil || string(data) != want {
					t.Fatalf("alias %s has wrong content: %v", name, err)
				}
			}
			blocked := t.TempDir()
			alias := version.BinaryName
			if prefix == version.BinaryName {
				alias = "musicserver"
			}
			aliasPath := filepath.Join(blocked, alias)
			if err := os.WriteFile(aliasPath, []byte("retain existing file"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := ExtractRelease(archive, blocked, expected); err == nil {
				t.Fatal("preexisting executable alias was overwritten")
			}
			if data, err := os.ReadFile(aliasPath); err != nil || string(data) != "retain existing file" {
				t.Fatal("preexisting alias content changed", err)
			}
		})
	}
}
