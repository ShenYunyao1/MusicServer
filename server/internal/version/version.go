// Package version contains the server release identity shared by the binary
// and its HTTP API.
package version

import (
	"encoding/hex"
	"runtime/debug"
	"strings"
)

// Container builds omit .git from the build context and supply source identity
// explicitly. Ordinary Git builds continue to use Go's own VCS metadata.
var buildCommit, buildModified string

const (
	ProductName = "Pikmelo Server"
	ChineseName = "拾律服务器"
	BinaryName  = "pikmelo-server"
	UpdaterName = "pikmelo-server-updater"
	// Version is the current music server API and management UI version.
	Version = "0.21.19"
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
	if commit == "unknown" && buildCommit != "" {
		return containerSource(buildCommit, buildModified)
	}
	return
}

func containerSource(commit, modified string) (string, bool) {
	if len(commit) != 40 || commit != strings.ToLower(commit) || (modified != "true" && modified != "false") {
		return "unknown", true
	}
	if _, err := hex.DecodeString(commit); err != nil {
		return "unknown", true
	}
	return commit, modified == "true"
}
