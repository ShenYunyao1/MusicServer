package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestRestrictsSignedMigrationDeclarations(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	for _, tc := range []struct {
		name   string
		schema int
		from   string
		valid  bool
	}{
		{"old same schema", 7, "[]", true},
		{"bridge declaration", 8, "[7]", true},
		{"new same schema", 8, "[]", true},
		{"unknown target", 999, "[]", false},
		{"duplicate", 8, "[7,7]", false},
		{"unknown source", 8, "[6]", false},
		{"reverse", 7, "[8]", false},
		{"self", 7, "[7]", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(fmt.Sprintf(`{"format":1,"version":"9.0.0","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","schema":%d,"migrate_from":%s}`, tc.schema, tc.from))
			signature := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, data)))
			_, err := VerifyManifest(data, signature, public)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
}

func TestCompatibilityRequiresSignedBridgeAndIndependentWorker(t *testing.T) {
	m := NewManager(t.TempDir(), func() string { return Repository })
	defer m.Close()
	m.workerState = filepath.Join(t.TempDir(), "job.json")
	m.readCapabilities = func(string) (workerCapabilities, error) {
		return workerCapabilities{}, errors.New("missing capabilities")
	}
	bridge := Manifest{Version: "9.0.0", Schema: 8, MigrateFrom: []int{7}}
	if !compatibleFrom(bridge, 7) {
		t.Fatal("signed bridge refused")
	}
	if compatibleFrom(Manifest{Schema: 8}, 7) {
		t.Fatal("unsigned migration declaration accepted")
	}
	if err := m.compatibilityError(bridge); err == nil || !strings.Contains(err.Error(), upgradeWorkerMessage) {
		t.Fatalf("missing capability accepted: %v", err)
	}
	if err := m.compatibilityError(Manifest{Schema: 7}); err != nil {
		t.Fatalf("legacy worker refused same schema 7: %v", err)
	}
	m.readCapabilities = func(string) (workerCapabilities, error) { return currentCapabilities(), nil }
	if err := m.compatibilityError(bridge); err != nil {
		t.Fatal(err)
	}
	m.installedSchema = 8
	if err := m.compatibilityError(Manifest{Schema: 8}); err != nil {
		t.Fatal(err)
	}
	if err := m.compatibilityError(Manifest{Schema: 7}); err == nil {
		t.Fatal("reverse migration accepted")
	}
	m.readCapabilities = func(string) (workerCapabilities, error) {
		return workerCapabilities{}, errors.New("removed capabilities")
	}
	if err := m.compatibilityError(Manifest{Schema: 8}); err == nil {
		t.Fatal("schema 8 accepted old worker")
	}
}

func TestCapabilityDeclarationsAreFiniteAndDirectional(t *testing.T) {
	for _, c := range []workerCapabilities{
		{Format: 2, Schemas: []int{7, 8}, Migrations: []schemaMigration{{7, 8}}},
		{Format: 1, Schemas: []int{7, 8, 999}, Migrations: []schemaMigration{{7, 8}}},
		{Format: 1, Schemas: []int{7, 8, 7}, Migrations: []schemaMigration{{7, 8}}},
		{Format: 1, Schemas: []int{7, 8}, Migrations: []schemaMigration{{8, 7}}},
		{Format: 1, Schemas: []int{7, 8}, Migrations: []schemaMigration{{7, 8}, {7, 8}}},
		{Format: 1, Schemas: []int{7, 8}},
	} {
		if c.supports(7, 8) {
			t.Fatalf("invalid capability accepted: %+v", c)
		}
	}
}

func TestPrepareRechecksWorkerCapabilities(t *testing.T) {
	m := NewManager(t.TempDir(), func() string { return Repository })
	defer m.Close()
	m.status = Status{State: "available", Repository: Repository, Compatible: true, LatestVersion: "9.0.0"}
	m.plan = &releasePlan{Manifest: Manifest{Version: "9.0.0", Schema: 8, MigrateFrom: []int{7}}, Repository: Repository}
	if err := m.Prepare(context.Background()); err == nil || !strings.Contains(err.Error(), upgradeWorkerMessage) {
		t.Fatalf("direct prepare bypassed capability: %v", err)
	}
}

func TestCheckSignedBridgeRequiresIndependentWorkerCapability(t *testing.T) {
	payload := []byte("signed bridge package")
	sum := sha256.Sum256(payload)
	m, _ := testManager(t, "9.0.0", "sha256:"+hex.EncodeToString(sum[:]), payload, 8, 7)
	defer m.Close()
	m.ConfigureInstaller("queue", filepath.Join(t.TempDir(), "job.json"))
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(); s.State != "incompatible" || s.Compatible || s.CanInstall || !strings.Contains(s.Message, upgradeWorkerMessage) {
		t.Fatalf("missing worker was not explained: %+v", s)
	}
	if err := m.Prepare(context.Background()); err == nil {
		t.Fatal("package prepared with old worker")
	}
	m.readCapabilities = func(string) (workerCapabilities, error) { return currentCapabilities(), nil }
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(); s.State != "available" || !s.Compatible {
		t.Fatalf("upgraded worker refused bridge: %+v", s)
	}
}

func TestCachedSchemaEightStatusRechecksIndependentWorker(t *testing.T) {
	m := NewManager(t.TempDir(), func() string { return Repository })
	defer m.Close()
	m.ConfigureInstaller("queue", filepath.Join(t.TempDir(), "job.json"))
	m.status = Status{State: "available", Repository: Repository, LatestVersion: "9.0.0", Schema: 8, Compatible: true}
	if s := m.Status(); s.Compatible || s.CanInstall || s.State != "incompatible" || !strings.Contains(s.Message, upgradeWorkerMessage) {
		t.Fatalf("cached status trusted absent worker: %+v", s)
	}
}
