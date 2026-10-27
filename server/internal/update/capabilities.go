package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"musicserver/internal/version"
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
	return workerCapabilities{Format: 1, Version: version.Version, Commit: commit, Schemas: []int{7, 8}, Migrations: []schemaMigration{{From: 7, To: 8}}}
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

// Called under m.mu. The app never writes this root-owned capability document.
func (m *Manager) compatibilityError(manifest Manifest) error {
	source := m.sourceSchema()
	if !compatibleFrom(manifest, source) {
		return errors.New("database schema is incompatible; manual upgrade required (data will not be cleared)")
	}
	return m.workerCapabilityError(source, manifest.Schema)
}

func (m *Manager) workerCapabilityError(source, target int) error {
	if !version.KnownSchema(source) || !version.KnownSchema(target) {
		return errors.New("unknown database schema")
	}
	// Original workers have no capability file and may continue schema-7 updates.
	if source == 7 && target == 7 {
		return nil
	}
	reader := readWorkerCapabilities
	if m.readCapabilities != nil {
		reader = m.readCapabilities
	}
	if m.workerState == "" {
		return errors.New(upgradeWorkerMessage)
	}
	c, err := reader(filepath.Join(filepath.Dir(m.workerState), capabilitiesName))
	if err != nil {
		return fmt.Errorf("%s: %w", upgradeWorkerMessage, err)
	}
	if !c.supports(source, target) {
		return errors.New(upgradeWorkerMessage)
	}
	return nil
}
