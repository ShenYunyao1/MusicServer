//go:build linux

package update

import (
	"context"
	"errors"
	"os"

	"musicserver/internal/version"
)

const independentWorkerPath = "/usr/local/lib/musicserver/musicserver-updater"

func verifyInstalledWorker() error {
	executing, err := os.Executable()
	if err != nil {
		return err
	}
	return verifySameWorkerFiles(executing, independentWorkerPath, rootOwned)
}

// RefreshWorker is an explicit root maintenance operation, never an HTTP/API
// operation. Run it from a verified same-schema bridge release. It updates only
// the independently installed worker, leaving the app, current link and DB alone.
func (i *Installer) RefreshWorker(ctx context.Context, releaseVersion string) error {
	if os.Geteuid() != 0 {
		return errors.New("independent updater maintenance requires root")
	}
	commit, dirty := version.Source()
	if commit == "unknown" || dirty {
		return errors.New("run the updater from a verified clean release package")
	}
	if err := rootOwned(i.Config.State, true); err != nil {
		return err
	}
	unlock, err := WorkerLock(i.Config.State)
	if err != nil {
		return err
	}
	defer unlock()
	previous, current, err := i.previous()
	if err != nil {
		return err
	}
	return i.refreshWorkerAt(ctx, releaseVersion, workerRefreshOptions{
		Target: independentWorkerPath,
		Own: Manifest{Format: 1, Version: version.Version, Commit: commit, Schema: version.Schema,
			MigrateFrom: version.MigrationSources(version.Schema)},
		Current: current, Trusted: rootOwned, Publish: i.WriteCapabilities,
		Unchanged: func() error {
			path, latest, err := i.previous()
			if err != nil {
				return err
			}
			if path != previous || latest.Version != current.Version || latest.Commit != current.Commit || latest.Schema != current.Schema {
				return errors.New("installed application changed during updater maintenance; retry from the current bridge release")
			}
			return nil
		},
	})
}
