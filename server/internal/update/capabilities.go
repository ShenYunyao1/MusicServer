package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"pikmelo-server/internal/version"
)

const capabilitiesName = "capabilities.json"
const upgradeWorkerMessage = "upgrade the independent systemd updater before installing this database schema"

type schemaMigration struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type workerCapabilities struct {
	Format     int               `json:"format"`
	Version    string            `json:"version,omitempty"`
	Commit     string            `json:"commit,omitempty"`
	Schemas    []int             `json:"schemas"`
	Migrations []schemaMigration `json:"migrations"`
}

func currentCapabilities() workerCapabilities {
	commit, _ := version.Source()
	return workerCapabilities{Format: 1, Version: version.Version, Commit: commit, Schemas: []int{7, 8, 9}, Migrations: []schemaMigration{{From: 7, To: 8}, {From: 8, To: 9}}}
}

// ReleaseCapabilities describes the signed package. It is not the root-owned
// live capability file, which only the independent worker writes.
func ReleaseCapabilities() ([]byte, error) { return json.Marshal(currentCapabilities()) }

func ValidateReleaseCapabilities(data []byte, identity Manifest, source, target int) error {
	c, err := decodeCapabilities(data)
	if err != nil {
		return err
	}
	if c.Version != identity.Version || c.Commit != identity.Commit || !c.supports(source, target) {
		return errors.New("bridge updater capabilities do not match identity or required migration")
	}
	return nil
}

func (c workerCapabilities) supports(source, target int) bool {
	if c.Format != 1 {
		return false
	}
	schemas := map[int]bool{}
	for _, schema := range c.Schemas {
		if !version.KnownSchema(schema) || schemas[schema] {
			return false
		}
		schemas[schema] = true
	}
	seen := map[schemaMigration]bool{}
	for _, migration := range c.Migrations {
		if seen[migration] || !schemas[migration.From] || !schemas[migration.To] || !version.CanMigrateSchema(migration.From, migration.To) {
			return false
		}
		seen[migration] = true
	}
	return schemas[source] && schemas[target] && (source == target || seen[schemaMigration{From: source, To: target}])
}

func decodeCapabilities(data []byte) (workerCapabilities, error) {
	var c workerCapabilities
	if len(data) > 65536 {
		return c, errors.New("updater capabilities are too large")
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	return c, nil
}

func (m *Manager) sourceSchema() int {
	if m.installedSchema != 0 {
		return m.installedSchema
	}
	return version.Schema
}

func (m *Manager) compatibilityError(manifest Manifest) error {
	snapshot := m.readCapabilitySnapshot()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.compatibilityErrorLocked(manifest, snapshot)
}

// The app never writes the root-owned capability document. Capture its path
// under the lock, read outside it, then revalidate current state under the lock.
type capabilitySnapshot struct {
	state string
	value workerCapabilities
	err   error
}

func (m *Manager) readCapabilitySnapshot() capabilitySnapshot {
	m.mu.Lock()
	state, reader := m.workerState, m.readCapabilities
	m.mu.Unlock()
	if reader == nil {
		reader = readWorkerCapabilities
	}
	snapshot := capabilitySnapshot{state: state}
	if state == "" {
		snapshot.err = errors.New(upgradeWorkerMessage)
		return snapshot
	}
	snapshot.value, snapshot.err = reader(filepath.Join(filepath.Dir(state), capabilitiesName))
	return snapshot
}
func (m *Manager) compatibilityErrorLocked(manifest Manifest, snapshot capabilitySnapshot) error {
	source := m.sourceSchema()
	if !compatibleFrom(manifest, source) {
		return errors.New("database schema is incompatible; manual upgrade required (data will not be cleared)")
	}
	return m.workerCapabilityErrorLocked(source, manifest.Schema, snapshot)
}

func (m *Manager) workerCapabilityErrorLocked(source, target int, snapshot capabilitySnapshot) error {
	if !version.KnownSchema(source) || !version.KnownSchema(target) {
		return errors.New("unknown database schema")
	}
	// Original workers have no capability file and may continue schema-7 updates.
	if source == 7 && target == 7 {
		return nil
	}
	if m.workerState == "" || m.workerState != snapshot.state {
		return errors.New(upgradeWorkerMessage)
	}
	if snapshot.err != nil {
		return fmt.Errorf("%s: %w", upgradeWorkerMessage, snapshot.err)
	}
	if !snapshot.value.supports(source, target) {
		return errors.New(upgradeWorkerMessage)
	}
	return nil
}
