// Package version contains the server release identity shared by the binary
// and its HTTP API.
package version

import "runtime/debug"

const (
	ProductName = "Pikmelo Server"
	ChineseName = "拾律服务器"
	BinaryName  = "pikmelo-server"
	UpdaterName = "pikmelo-server-updater"
	// Version is the current music server API and management UI version.
	Version = "0.21.18"
	Schema  = 9
)

// Source identifies the source used for this binary without changing its name.
func Source() (commit string, modified bool) {
	commit = "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				commit = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
	}
	return
}
