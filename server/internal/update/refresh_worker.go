package update

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"musicserver/internal/version"
)

type workerRefreshOptions struct {
	Target    string
	Own       Manifest
	Current   Manifest
	Trusted   func(string, bool) error
	Publish   func() error
	Unchanged func() error
}

// The Linux entry holds WorkerLock and supplies real root ownership checks,
// installed/current identity and the running verified candidate's build identity.
func (i *Installer) refreshWorkerAt(ctx context.Context, requested string, options workerRefreshOptions) error {
	if options.Trusted == nil || options.Publish == nil || options.Unchanged == nil || i.Prepare == nil || !versionTag.MatchString(requested) || strings.HasPrefix(requested, "v") || options.Own.Version != requested || options.Own.Format != 1 || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(options.Own.Commit) {
		return errors.New("updater maintenance requires the matching verified release executable")
	}
	if !version.KnownSchema(options.Current.Schema) || options.Current.Schema != options.Own.Schema {
		return errors.New("use a same-schema bridge release to refresh the independent updater")
	}
	if order, err := compareVersions(requested, options.Current.Version); err != nil || order < 0 {
		return errors.New("updater maintenance cannot downgrade the installed application release")
	}
	for _, path := range []string{i.Config.Root, i.Config.State, filepath.Dir(options.Target)} {
		if !filepath.IsAbs(path) {
			return errors.New("updater maintenance paths must be absolute")
		}
		if err := options.Trusted(path, true); err != nil {
			return err
		}
	}
	if !filepath.IsAbs(options.Target) || filepath.Base(options.Target) != "musicserver-updater" {
		return errors.New("invalid independent updater destination")
	}
	if err := options.Trusted(options.Target, false); err != nil {
		return err
	}
	if j, err := i.loadJob(); err == nil && !j.terminal() {
		return errors.New("finish or recover the active application update before updater maintenance")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot read existing update state: %w", err)
	}
	if _, err := os.Lstat(i.maintenance()); err == nil {
		return errors.New("application update maintenance is active")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	manifest, candidate, err := i.Prepare(ctx, requested)
	if err != nil {
		return err
	}
	if !sameWorkerIdentity(manifest, options.Own) || manifest.Schema != options.Current.Schema {
		return errors.New("prepared updater differs from the running verified release identity")
	}
	base, err := filepath.EvalSymlinks(filepath.Join(i.Config.Root, "releases"))
	if err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(base, real)
	if err != nil || !filepath.IsAbs(candidate) || real != filepath.Clean(candidate) || relative == "." || !filepath.IsLocal(relative) {
		return errors.New("prepared updater is outside the root-owned release directory")
	}
	for _, path := range []string{base, real} {
		if err := options.Trusted(path, true); err != nil {
			return err
		}
	}
	buildPath := filepath.Join(real, "build.json")
	workerPath := filepath.Join(real, "musicserver-updater")
	for _, path := range []string{buildPath, workerPath} {
		if err := options.Trusted(path, false); err != nil {
			return err
		}
	}
	buildData, err := readWorkerRegular(buildPath, 65536)
	if err != nil {
		return err
	}
	var build Manifest
	if json.Unmarshal(buildData, &build) != nil || !sameWorkerIdentity(build, manifest) {
		return errors.New("prepared updater package identity is inconsistent")
	}
	newWorker, err := readWorkerRegular(workerPath, 64<<20)
	if err != nil || len(newWorker) == 0 {
		return errors.New("prepared independent updater is missing, empty or too large")
	}
	oldWorker, err := readWorkerRegular(options.Target, 64<<20)
	if err != nil {
		return err
	}
	capPath := filepath.Join(i.Config.State, capabilitiesName)
	var oldCapabilities []byte
	capPresent := false
	if _, err := os.Lstat(capPath); err == nil {
		if err = options.Trusted(capPath, false); err != nil {
			return err
		}
		oldCapabilities, err = readWorkerRegular(capPath, 65536)
		if err != nil {
			return err
		}
		capPresent = true
		if old, decodeErr := decodeCapabilities(oldCapabilities); decodeErr == nil && old.Version != "" {
			order, compareErr := compareVersions(requested, old.Version)
			if compareErr != nil || order < 0 || order == 0 && old.Commit != "" && old.Commit != manifest.Commit {
				return errors.New("refusing to downgrade or replace a different immutable updater release")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := options.Unchanged(); err != nil {
		return err
	}
	backup := filepath.Join(i.Config.State, "worker-previous")
	if _, err := os.Lstat(backup); err == nil {
		if err = options.Trusted(backup, false); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := atomicWrite(backup, oldWorker, 0700); err != nil {
		return err
	}
	if err := atomicWrite(options.Target, newWorker, 0755); err != nil {
		// A directory fsync can fail after rename, so restore even when the
		// atomic writer returns an error rather than assuming nothing changed.
		return errors.Join(err, atomicWrite(options.Target, oldWorker, 0755))
	}
	if err := options.Publish(); err != nil {
		// Restore conservative capability information before restoring an older
		// executable. If that is impossible, retain the new executable; never
		// advertise a new capability while running the previous worker.
		if restoreErr := restoreWorkerCapabilities(capPath, oldCapabilities, capPresent); restoreErr != nil {
			return fmt.Errorf("new updater retained; capabilities recovery needs root maintenance: %w", errors.Join(err, restoreErr))
		}
		return errors.Join(err, atomicWrite(options.Target, oldWorker, 0755))
	}
	return nil
}

func sameWorkerIdentity(a, b Manifest) bool {
	return a.Format == b.Format && a.Version == b.Version && a.Commit == b.Commit && a.Schema == b.Schema && slices.Equal(a.MigrateFrom, b.MigrateFrom)
}

func readWorkerRegular(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maximum {
		return nil, errors.New("worker maintenance accepts bounded regular files only")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if int64(len(data)) > maximum {
		return nil, errors.New("worker maintenance file exceeds its limit")
	}
	return data, err
}

func restoreWorkerCapabilities(path string, data []byte, present bool) error {
	if present {
		return atomicWrite(path, data, 0644)
	}
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// A verified candidate may be run from its extracted directory, but it cannot
// advertise capabilities until the systemd worker actually contains its bytes.
func verifySameWorkerFiles(executing, installed string, trusted func(string, bool) error) error {
	for _, path := range []string{executing, installed} {
		if err := trusted(filepath.Dir(path), true); err != nil {
			return err
		}
		if err := trusted(path, false); err != nil {
			return err
		}
	}
	running, err := readWorkerRegular(executing, 64<<20)
	if err != nil {
		return err
	}
	active, err := readWorkerRegular(installed, 64<<20)
	if err != nil {
		return err
	}
	if len(running) == 0 || sha256.Sum256(running) != sha256.Sum256(active) {
		return errors.New("independent updater is not installed yet; run verified same-schema root maintenance first")
	}
	return nil
}
