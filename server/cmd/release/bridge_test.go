package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pikmelo-server/internal/update"
)

func TestSchemaNineFormalReleaseRequiresSignedCapableBridge(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	for _, kind := range []string{"valid", "missing", "unsigned", "old-worker", "wrong-identity", "tampered", "not-older", "wrong-schema"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			m := update.Manifest{Format: 1, Version: "0.21.15", Commit: strings.Repeat("a", 40), Schema: 8}
			if kind == "not-older" {
				m.Version = "0.22.0"
			}
			if kind == "wrong-schema" {
				m.Schema = 9
			}
			caps := map[string]any{"format": 1, "version": m.Version, "commit": m.Commit, "schemas": []int{8, 9}, "migrations": []map[string]int{{"from": 8, "to": 9}}}
			if kind == "old-worker" {
				caps["migrations"] = []int{}
			}
			if kind == "wrong-identity" {
				caps["commit"] = strings.Repeat("b", 40)
			}
			identity, _ := json.Marshal(m)
			capData, _ := json.Marshal(caps)
			for _, platform := range []string{"windows", "linux"} {
				name := update.PackageName(platform, "amd64")
				if err := archive(filepath.Join(dir, name), platform, map[string][]byte{"build.json": identity, "deploy/updater-capabilities.json": capData}); err != nil {
					t.Fatal(err)
				}
				data, _ := os.ReadFile(filepath.Join(dir, name))
				sum := sha256.Sum256(data)
				m.Packages = append(m.Packages, update.Package{Name: name, OS: platform, Arch: "amd64", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
			}
			data, _ := json.Marshal(m)
			os.WriteFile(filepath.Join(dir, update.ManifestName), data, 0600)
			os.WriteFile(filepath.Join(dir, update.ManifestName+".sig"), []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, data))), 0600)
			if kind == "missing" {
				dir = t.TempDir()
			}
			if kind == "unsigned" {
				os.Remove(filepath.Join(dir, update.ManifestName+".sig"))
			}
			if kind == "tampered" {
				os.WriteFile(filepath.Join(dir, update.PackageName("linux", "amd64")), []byte("tampered"), 0600)
			}
			err := validateBridgeAssets(dir, public, "0.22.0", "v"+m.Version, m.Commit)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("bridge %s: %v", kind, err)
			}
		})
	}
}
