package update

import "pikmelo-server/internal/version"

const containerUpdateMessage = "Updates are managed with Docker Compose. Stop and back up the data volume before recreating the service."

// ConfigureContainer is called before background workers start.
func (m *Manager) ConfigureContainer() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.container = true
	m.plan = nil
}

func (m *Manager) containerStatus() (Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{State: "external", CurrentVersion: version.Version, Schema: version.Schema, Message: containerUpdateMessage}, m.container
}
