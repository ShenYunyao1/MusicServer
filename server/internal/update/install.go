package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var jobID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Request struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}
type Job struct {
	ID              string    `json:"id"`
	Version         string    `json:"version"`
	Phase           string    `json:"phase"`
	Message         string    `json:"message,omitempty"`
	Backup          string    `json:"backup,omitempty"`
	Previous        string    `json:"previous,omitempty"`
	Target          string    `json:"target,omitempty"`
	Commit          string    `json:"commit,omitempty"`
	RestoreRequired bool      `json:"restore_required,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (j Job) terminal() bool {
	return j.Phase == "completed" || j.Phase == "rolled_back" || j.Phase == "failed"
}

// Config is installed by root, never populated from an HTTP request.
type InstallConfig struct {
	Root      string `json:"root"`
	State     string `json:"state"`
	Queue     string `json:"queue"`
	Database  string `json:"database"`
	HealthURL string `json:"health_url"`
	Service   string `json:"service"`
}
type Installer struct {
	Config  InstallConfig
	Stop    func(context.Context) error
	Start   func(context.Context) error
	Healthy func(context.Context, Manifest) error
	Prepare func(context.Context, string) (Manifest, string, error)
	Backup  func(string) error
	Restore func(string) error
}

func (i *Installer) loadJob() (Job, error) {
	var j Job
	data, err := os.ReadFile(filepath.Join(i.Config.State, "job.json"))
	if err == nil {
		err = json.Unmarshal(data, &j)
	}
	return j, err
}
func (i *Installer) save(j *Job, phase, message string) error {
	j.Phase = phase
	j.Message = message
	j.UpdatedAt = time.Now().UTC()
	return atomicJSON(filepath.Join(i.Config.State, "job.json"), j, 0644)
}
func (i *Installer) maintenance() string { return filepath.Join(i.Config.State, "maintenance") }
func (i *Installer) clearMaintenance() error {
	err := os.Remove(i.maintenance())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (i *Installer) previous() (string, Manifest, error) {
	var build Manifest
	current, err := filepath.EvalSymlinks(filepath.Join(i.Config.Root, "current"))
	if err != nil {
		return "", build, err
	}
	releases := filepath.Join(i.Config.Root, "releases") + string(os.PathSeparator)
	if !strings.HasPrefix(current, releases) {
		return "", build, errors.New("current release is outside managed releases directory")
	}
	data, err := os.ReadFile(filepath.Join(current, "build.json"))
	if err == nil {
		err = json.Unmarshal(data, &build)
	}
	if err == nil && (!versionTag.MatchString(build.Version) || build.Schema < 1) {
		err = errors.New("invalid installed build identity")
	}
	return current, build, err
}

// Install journals before every destructive step. A restarted worker rolls back an interrupted transaction.
func (i *Installer) Install(ctx context.Context, r Request) (err error) {
	if !jobID.MatchString(r.ID) || !versionTag.MatchString(r.Version) || strings.HasPrefix(r.Version, "v") {
		return errors.New("invalid update request")
	}
	if previous, e := i.loadJob(); e == nil && !previous.terminal() {
		return errors.New("unfinished update must be recovered first")
	}
	j := Job{ID: r.ID, Version: r.Version}
	if err = i.save(&j, "downloading", ""); err != nil {
		return err
	}
	stopped := false
	defer func() {
		if err == nil {
			return
		}
		if j.Phase == "completed" {
			return
		} // Recovery only needs to remove the maintenance flag.
		if stopped {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if e := i.rollback(rollbackCtx, &j, err.Error()); e != nil {
				err = fmt.Errorf("%w; rollback: %v", err, e)
			}
		} else {
			_ = i.save(&j, "failed", err.Error())
		}
	}()
	previous, current, err := i.previous()
	if err != nil {
		return err
	}
	j.Previous = previous
	manifest, target, err := i.Prepare(ctx, r.Version)
	if err != nil {
		return err
	}
	if manifest.Version != r.Version || manifest.Schema != current.Schema {
		return errors.New("release version or database schema is incompatible")
	}
	order, err := compareVersions(manifest.Version, current.Version)
	if err != nil || order <= 0 {
		return errors.New("update must be newer than installed release")
	}
	if !strings.HasPrefix(target, filepath.Join(i.Config.Root, "releases")+string(os.PathSeparator)) {
		return errors.New("prepared release is outside release directory")
	}
	j.Target, j.Commit = target, manifest.Commit
	j.Backup = filepath.Join(i.Config.State, "backups", j.ID+".sqlite")
	if err = i.save(&j, "stopping", ""); err != nil {
		return err
	}
	// The flag blocks API traffic and startup scans until verification or rollback completes.
	if err = atomicWrite(i.maintenance(), []byte(j.ID), 0644); err != nil {
		return err
	}
	stopped = true
	if err = i.Stop(ctx); err != nil {
		return err
	}
	if err = i.save(&j, "backing_up", ""); err != nil {
		return err
	}
	if err = i.Backup(j.Backup); err != nil {
		return fmt.Errorf("final database backup: %w", err)
	}
	j.RestoreRequired = true
	if err = i.save(&j, "switching", ""); err != nil {
		return err
	}
	if err = replaceLink(i.Config.Root, j.Target); err != nil {
		return err
	}
	if err = i.save(&j, "starting", ""); err != nil {
		return err
	}
	if err = i.Start(ctx); err != nil {
		return err
	}
	if err = i.save(&j, "verifying", ""); err != nil {
		return err
	}
	if err = i.Healthy(ctx, manifest); err != nil {
		return err
	}
	// Record success first. Recovery can then safely clear a leftover maintenance flag.
	if err = i.save(&j, "completed", "update installed and verified"); err != nil {
		return err
	}
	stopped = false
	return i.clearMaintenance()
}

func (i *Installer) rollback(ctx context.Context, j *Job, reason string) error {
	restore := j.RestoreRequired
	if err := i.save(j, "rolling_back", reason); err != nil {
		return err
	}
	fail := func(err error) error { _ = i.save(j, "recovery_failed", reason+"; "+err.Error()); return err }
	if err := i.Stop(ctx); err != nil {
		return fail(err)
	}
	if j.Previous == "" || !strings.HasPrefix(j.Previous, filepath.Join(i.Config.Root, "releases")+string(os.PathSeparator)) {
		return fail(errors.New("missing previous release"))
	}
	if err := replaceLink(i.Config.Root, j.Previous); err != nil {
		return fail(err)
	}
	if restore {
		if err := i.Restore(j.Backup); err != nil {
			return fail(err)
		}
	}
	if err := i.Start(ctx); err != nil {
		return fail(err)
	}
	var old Manifest
	data, err := os.ReadFile(filepath.Join(j.Previous, "build.json"))
	if err != nil {
		return fail(err)
	}
	if err = json.Unmarshal(data, &old); err != nil {
		return fail(err)
	}
	if err = i.Healthy(ctx, old); err != nil {
		return fail(err)
	}
	if err = i.save(j, "rolled_back", reason); err != nil {
		return fail(err)
	}
	return i.clearMaintenance()
}

func (i *Installer) Recover(ctx context.Context) error {
	j, err := i.loadJob()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if j.terminal() {
		return i.clearMaintenance()
	}
	if j.Phase == "downloading" {
		return i.save(&j, "failed", "preparation interrupted; original service retained")
	}
	if err = atomicWrite(i.maintenance(), []byte(j.ID), 0644); err != nil {
		return err
	}
	return i.rollback(ctx, &j, "update worker interrupted; restoring previous release")
}

// RunQueued consumes only bounded, validated JSON requests. Job paths never come from the request body.
func (i *Installer) RunQueued(ctx context.Context) error {
	if err := i.Recover(ctx); err != nil {
		return err
	}
	entries, err := os.ReadDir(i.Config.Queue)
	if err != nil {
		return err
	}
	queue, err := os.OpenRoot(i.Config.Queue)
	if err != nil {
		return err
	}
	defer queue.Close()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !jobID.MatchString(id) {
			continue
		}
		var r Request
		f, e := queue.Open(name)
		if e != nil {
			return e
		}
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
			f.Close()
			return errors.New("invalid queue entry")
		}
		e = json.NewDecoder(io.LimitReader(f, 4097)).Decode(&r)
		f.Close()
		if e != nil || r.ID != id || !versionTag.MatchString(r.Version) || strings.HasPrefix(r.Version, "v") {
			_ = queue.Remove(name)
			continue
		}
		// Persist a recoverable preparation marker before removing the request.
		j := Job{ID: r.ID, Version: r.Version}
		if err = i.save(&j, "downloading", ""); err != nil {
			return err
		}
		if err = queue.Remove(name); err != nil {
			return err
		}
		// Install expects no active transaction; preparation has no side effects yet.
		if err = i.save(&j, "failed", "starting queued job"); err != nil {
			return err
		}
		if err = i.Install(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

func replaceLink(root, target string) error {
	temporary := filepath.Join(root, ".current-next")
	if err := os.Remove(temporary); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(target, temporary); err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Rename(temporary, filepath.Join(root, "current")); err != nil {
		return err
	}
	return syncDir(root)
}

func copyRegular(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("database/backup must be a regular file")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = io.Copy(f, input)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), target); err != nil {
		return err
	}
	return syncDir(filepath.Dir(target))
}

// ExtractRelease accepts explicit regular files only, with bounded expansion and no links.
func ExtractRelease(archive, dest string, expected Manifest) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(io.LimitReader(gz, maxPackageSize+1))
	seen := map[string]bool{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag == tar.TypeDir && name == "deploy/" {
			continue
		}
		allowed := name == "musicserver" || name == "musicserver-updater" || name == "build.json" || name == "README.md" || strings.HasPrefix(name, "deploy/") && !strings.Contains(strings.TrimPrefix(name, "deploy/"), "/") && !strings.Contains(name, "..")
		if !allowed || seen[name] || h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > maxPackageSize {
			return fmt.Errorf("unsafe release archive member %q", name)
		}
		seen[name] = true
		if name != "musicserver" && name != "build.json" {
			continue
		}
		mode := os.FileMode(0644)
		if name == "musicserver" {
			mode = 0755
		}
		out, e := os.OpenFile(filepath.Join(dest, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if e != nil {
			return e
		}
		_, e = io.Copy(out, tr)
		if e == nil {
			// The root worker uses UMask=0027. Explicit permissions are needed
			// so the unprivileged service can read and execute this release.
			e = out.Chmod(mode)
		}
		if e == nil {
			e = out.Sync()
		}
		ce := out.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	if !seen["musicserver"] || !seen["build.json"] {
		return errors.New("release archive is incomplete")
	}
	var build Manifest
	data, err := os.ReadFile(filepath.Join(dest, "build.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &build); err != nil {
		return err
	}
	if build.Version != expected.Version || build.Commit != expected.Commit || build.Schema != expected.Schema {
		return errors.New("archive build identity differs from signed manifest")
	}
	return syncDir(dest)
}

func HealthCheck(ctx context.Context, address string, expected Manifest) error {
	deadline, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("health redirects forbidden") }}
	var last error
	for {
		req, err := http.NewRequestWithContext(deadline, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			var h struct {
				Status  string
				Version string
				Commit  string
				Schema  int
			}
			err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&h)
			resp.Body.Close()
			if err == nil && resp.StatusCode == 200 && h.Status == "ok" && h.Version == expected.Version && h.Commit == expected.Commit && h.Schema == expected.Schema {
				return nil
			}
			err = errors.New("health version, commit or schema mismatch")
		}
		last = err
		select {
		case <-deadline.Done():
			return fmt.Errorf("new release health check: %w (%v)", deadline.Err(), last)
		case <-time.After(500 * time.Millisecond):
		}
	}
}
