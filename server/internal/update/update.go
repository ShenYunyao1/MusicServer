// Package update checks GitHub Releases and stages verified server packages.
package update

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"musicserver/internal/version"
)

const maxPackageSize = 512 << 20

var repositoryPath = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9_.-]+$`)
var versionTag = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

type Status struct {
	State          string    `json:"state"`
	Repository     string    `json:"repository,omitempty"`
	CurrentVersion string    `json:"current_version"`
	LatestVersion  string    `json:"latest_version,omitempty"`
	ReleaseURL     string    `json:"release_url,omitempty"`
	PackagePath    string    `json:"package_path,omitempty"`
	BackupPath     string    `json:"backup_path,omitempty"`
	Message        string    `json:"message,omitempty"`
	CheckedAt      time.Time `json:"checked_at,omitzero"`
	Commit         string    `json:"commit,omitempty"`
	Schema         int       `json:"schema,omitempty"`
	Notes          string    `json:"notes,omitempty"`
	Compatible     bool      `json:"compatible"`
	CanInstall     bool      `json:"can_install"`
	JobID          string    `json:"job_id,omitempty"`
}

type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
	Assets  []struct {
		Name   string `json:"name"`
		APIURL string `json:"url"`
		URL    string `json:"browser_download_url"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

type Manager struct {
	mu               sync.Mutex
	status           Status
	dir              string
	repository       func() string
	client           *http.Client
	apiBase          string
	allowHTTP        bool // Test servers only; production URLs must use HTTPS.
	publicKey        []byte
	plan             *releasePlan
	queueDir         string
	workerState      string
	installedSchema  int                                      // Worker uses the installed build identity, not its compiled schema.
	readCapabilities func(string) (workerCapabilities, error) // Private test seam; production validates root ownership.
	context          context.Context
	cancel           context.CancelFunc
	workers          sync.WaitGroup
	closed           bool
}

func NewManager(dir string, repository func() string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	key, _ := base64.StdEncoding.DecodeString(ReleasePublicKey)
	m := &Manager{
		context: ctx, cancel: cancel, publicKey: key,
		dir: dir, repository: repository, apiBase: "https://api.github.com",
		client: &http.Client{CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request.URL.Scheme != "https" {
				return errors.New("update redirect must use HTTPS")
			}
			if len(via) >= 5 {
				return errors.New("too many update redirects")
			}
			if request.URL.Host != "api.github.com" {
				request.Header.Del("Authorization")
			}
			return nil
		}},
		status: Status{State: "disabled", CurrentVersion: version.Version},
	}
	if data, err := os.ReadFile(filepath.Join(dir, "status.json")); err == nil {
		_ = json.Unmarshal(data, &m.status)
		m.status.CurrentVersion = version.Version
		if m.status.State == "checking" || m.status.State == "downloading" {
			m.status.State = "error"
			m.status.Message = "previous preparation was interrupted; check again"
		}
	}
	return m
}

type releasePlan struct {
	Manifest   Manifest
	Package    Package
	Address    string
	Repository string
}

func (m *Manager) ConfigureInstaller(queue, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueDir = queue
	m.workerState = state
}

func (m *Manager) recoverOrphanedQueueLocked() {
	if m.status.State != "queued" || m.status.JobID == "" || m.queueDir == "" || m.workerState == "" {
		return
	}
	var job Job
	if data, err := os.ReadFile(m.workerState); err == nil && json.Unmarshal(data, &job) == nil && job.ID == m.status.JobID {
		return
	}
	if m.jobActiveLocked() {
		return
	}
	m.status.State = "error"
	m.status.Message = "queued update request was interrupted before publication; check again"
	m.status.JobID = ""
	_ = atomicJSON(filepath.Join(m.dir, "status.json"), m.status, 0600)
}

func ValidateRepository(raw string) error {
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.EscapedPath() != parsed.Path || !repositoryPath.MatchString(strings.Trim(parsed.Path, "/")) {
		return errors.New("update_repository must be a GitHub repository URL such as https://github.com/owner/repo")
	}
	return nil
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recoverOrphanedQueueLocked()
	status := m.status
	if m.plan == nil && status.Schema != 0 {
		if err := m.workerCapabilityError(m.sourceSchema(), status.Schema); err != nil {
			status.Compatible = false
			if status.State == "available" || status.State == "incompatible" {
				status.State, status.Message = "incompatible", err.Error()
			}
		}
	}
	if m.plan != nil && m.plan.Manifest.Version == status.LatestVersion && m.plan.Repository == status.Repository {
		if err := m.compatibilityError(m.plan.Manifest); err != nil {
			status.Compatible = false
			status.CanInstall = false
			if status.State == "available" || status.State == "incompatible" {
				status.State, status.Message = "incompatible", err.Error()
			}
		} else {
			status.Compatible = true
			if status.State == "incompatible" {
				status.State, status.Message = "available", ""
			}
		}
	}
	status.CanInstall = runtime.GOOS == "linux" && m.queueDir != "" && m.workerState != "" && status.Repository == Repository && status.Compatible && m.plan != nil
	if status.JobID != "" && m.workerState != "" {
		var job Job
		if data, err := os.ReadFile(m.workerState); err == nil && json.Unmarshal(data, &job) == nil && job.ID == status.JobID {
			status.State = job.Phase
			status.Message = job.Message
			status.BackupPath = job.Backup
		}
	}
	repository := m.repository()
	if status.Repository != repository {
		status = Status{State: "disabled", Repository: repository, CurrentVersion: version.Version}
		if repository != "" {
			status.State = "checking"
		}
	}
	return status
}

func (m *Manager) begin() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.status.State == "checking" || m.status.State == "downloading" || m.jobActiveLocked() {
		return false
	}
	m.status.State = "checking"
	m.status.Message = ""
	m.workers.Add(1)
	return true
}

func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	m.workers.Wait()
}

func (m *Manager) finish(status Status, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		status.State = "error"
		status.Message = err.Error()
	}
	status.CheckedAt = time.Now().UTC()
	m.status = status
	if saveErr := atomicJSON(filepath.Join(m.dir, "status.json"), status, 0600); saveErr != nil {
		m.status.State = "error"
		m.status.Message = "persist update status: " + saveErr.Error()
	}
}

func (m *Manager) Check(ctx context.Context) error {
	if !m.begin() {
		return errors.New("update check is already running")
	}
	return m.run(ctx)
}

func (m *Manager) run(ctx context.Context) error {
	defer m.workers.Done()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(m.context, cancel)
	defer stop()
	status, err := m.check(ctx)
	m.finish(status, err)
	return err
}

func (m *Manager) Trigger() bool {
	if !m.begin() {
		return false
	}
	go func() { _ = m.run(context.Background()) }()
	return true
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.workers.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.workers.Done()
		m.mu.Lock()
		m.recoverOrphanedQueueLocked()
		hasJob := m.status.JobID != ""
		m.mu.Unlock()
		if !hasJob {
			_ = m.Check(ctx)
		}
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = m.Check(ctx)
			case <-ctx.Done():
				return
			case <-m.context.Done():
				return
			}
		}
	}()
}

func packageName() string {
	return PackageName(runtime.GOOS, runtime.GOARCH)
}

func compareVersions(latest, current string) (int, error) {
	parts := func(value string) ([3]uint64, error) {
		var values [3]uint64
		match := versionTag.FindStringSubmatch(value)
		if match == nil {
			return values, fmt.Errorf("invalid release version %q", value)
		}
		for i := range values {
			parsed, err := strconv.ParseUint(match[i+1], 10, 64)
			if err != nil {
				return values, err
			}
			values[i] = parsed
		}
		return values, nil
	}
	a, err := parts(latest)
	if err != nil {
		return 0, err
	}
	b, err := parts(current)
	if err != nil {
		return 0, err
	}
	for i := range a {
		if a[i] > b[i] {
			return 1, nil
		}
		if a[i] < b[i] {
			return -1, nil
		}
	}
	return 0, nil
}

func (m *Manager) request(ctx context.Context, address string, asset bool) (*http.Response, error) {
	parsed, err := url.Parse(address)
	if err != nil || (parsed.Scheme != "https" && !(m.allowHTTP && parsed.Scheme == "http")) {
		return nil, errors.New("update URL must use HTTPS")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "musicserver/"+version.Version)
	if asset {
		request.Header.Set("Accept", "application/octet-stream")
	} else {
		request.Header.Set("Accept", "application/vnd.github+json")
	}
	if token := os.Getenv("MUSIC_SERVER_GITHUB_TOKEN"); token != "" && (parsed.Host == "api.github.com" || m.allowHTTP) {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return m.client.Do(request)
}

func (m *Manager) check(ctx context.Context) (Status, error) {
	repo := m.repository()
	status := Status{Repository: repo, CurrentVersion: version.Version}
	if repo == "" {
		status.State = "disabled"
		return status, nil
	}
	if err := ValidateRepository(repo); err != nil {
		return status, err
	}
	path := strings.Trim(strings.TrimPrefix(repo, "https://github.com/"), "/")
	return m.checkRelease(ctx, status, path, "latest", true)
}

func (m *Manager) checkRelease(ctx context.Context, status Status, path, ref string, compare bool) (Status, error) {
	response, err := m.request(ctx, m.apiBase+"/repos/"+path+"/releases/"+ref, false)
	if err != nil {
		return status, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return status, fmt.Errorf("GitHub release check returned HTTP %d", response.StatusCode)
	}
	var latest release
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&latest); err != nil {
		return status, fmt.Errorf("decode release: %w", err)
	}
	order, err := compareVersions(latest.TagName, version.Version)
	if err != nil {
		return status, err
	}
	status.LatestVersion = strings.TrimPrefix(latest.TagName, "v")
	status.ReleaseURL = latest.HTMLURL
	if compare && order <= 0 {
		status.State = "current"
		return status, nil
	}
	manifest, err := m.manifest(ctx, latest)
	if err != nil {
		return status, err
	}
	if manifest.Version != status.LatestVersion {
		return status, errors.New("manifest version differs from release tag")
	}
	m.mu.Lock()
	compatibilityErr := m.compatibilityError(manifest)
	m.mu.Unlock()
	status.Commit, status.Schema, status.Notes, status.Compatible = manifest.Commit, manifest.Schema, latest.Body, compatibilityErr == nil
	pkg, err := manifest.Platform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return status, err
	}
	name := pkg.Name
	for _, asset := range latest.Assets {
		if asset.Name != name {
			continue
		}
		if asset.Size != pkg.Size {
			return status, errors.New("release package size is invalid")
		}
		if asset.Digest != "" && !strings.EqualFold(asset.Digest, "sha256:"+pkg.SHA256) {
			return status, errors.New("GitHub asset digest differs from signed manifest")
		}
		address := asset.URL
		if asset.APIURL != "" {
			address = asset.APIURL
		}
		m.mu.Lock()
		m.plan = &releasePlan{manifest, pkg, address, status.Repository}
		m.mu.Unlock()
		status.State = "available"
		if !status.Compatible {
			status.State = "incompatible"
			status.Message = compatibilityErr.Error()
		}
		return status, nil
	}
	return status, fmt.Errorf("release has no package named %s", name)
}

func (m *Manager) manifest(ctx context.Context, latest release) (Manifest, error) {
	read := func(name string) ([]byte, error) {
		for _, a := range latest.Assets {
			if a.Name != name {
				continue
			}
			if a.Size <= 0 || a.Size > 65536 {
				return nil, errors.New("release metadata size is invalid")
			}
			address := a.URL
			if a.APIURL != "" {
				address = a.APIURL
			}
			r, e := m.request(ctx, address, true)
			if e != nil {
				return nil, e
			}
			defer r.Body.Close()
			if r.StatusCode != 200 {
				return nil, fmt.Errorf("metadata download HTTP %d", r.StatusCode)
			}
			data, e := io.ReadAll(io.LimitReader(r.Body, 65537))
			if e != nil {
				return nil, e
			}
			if int64(len(data)) != a.Size {
				return nil, errors.New("metadata size mismatch")
			}
			return data, nil
		}
		return nil, fmt.Errorf("release has no signed metadata %s", name)
	}
	data, err := read(ManifestName)
	if err != nil {
		return Manifest{}, err
	}
	sig, err := read(ManifestName + ".sig")
	if err != nil {
		return Manifest{}, err
	}
	return VerifyManifest(data, sig, m.publicKey)
}

// Prepare downloads a verified package without installing it. Checks never call this.
func (m *Manager) Prepare(ctx context.Context) error {
	m.mu.Lock()
	if m.closed || (m.status.State != "available" && m.status.State != "incompatible") || m.plan == nil || m.plan.Repository != m.repository() {
		m.mu.Unlock()
		return errors.New("check a compatible release first")
	}
	plan := *m.plan
	if err := m.compatibilityError(plan.Manifest); err != nil {
		m.mu.Unlock()
		return err
	}
	status := m.status
	m.status.State = "downloading"
	m.workers.Add(1)
	m.mu.Unlock()
	defer m.workers.Done()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	stop := context.AfterFunc(m.context, cancel)
	defer stop()
	digest, _ := hex.DecodeString(plan.Package.SHA256)
	path, err := m.download(ctx, plan.Manifest.Version, plan.Address, plan.Package.Name, plan.Package.Size, digest)
	status.State = "downloaded"
	status.PackagePath = path
	m.finish(status, err)
	return err
}

func (m *Manager) jobActiveLocked() bool {
	if m.status.JobID == "" {
		return false
	}
	var job Job
	if data, err := os.ReadFile(m.workerState); err == nil && json.Unmarshal(data, &job) == nil && job.ID == m.status.JobID {
		return !job.terminal()
	}
	if m.status.State == "queued" {
		_, err := os.Stat(filepath.Join(m.queueDir, m.status.JobID+".json"))
		return err == nil || !os.IsNotExist(err)
	}
	return false
}

// Apply queues only a version, never a shell command or a caller-supplied path.
func (m *Manager) Apply(expected string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || runtime.GOOS != "linux" || m.queueDir == "" || m.workerState == "" || m.repository() != Repository {
		return errors.New("automatic installation requires the Ubuntu systemd updater; Windows supports release checks and manual installation")
	}
	if m.plan == nil {
		return errors.New("check a verified compatible release first")
	}
	if err := m.compatibilityError(m.plan.Manifest); err != nil {
		return err
	}
	if m.jobActiveLocked() || (m.status.State != "available" && m.status.State != "incompatible") || m.status.LatestVersion != expected || m.status.Repository != m.repository() || m.plan.Manifest.Version != expected || m.plan.Repository != m.repository() {
		return errors.New("release changed or update is busy; check again")
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	id := hex.EncodeToString(token)
	status := m.status
	status.JobID = id
	status.State = "queued"
	// Persist first: an accepted request remains traceable if the service stops immediately.
	if err := atomicJSON(filepath.Join(m.dir, "status.json"), status, 0600); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(m.queueDir, id+".json"), Request{ID: id, Version: expected}, 0640); err != nil {
		_ = atomicJSON(filepath.Join(m.dir, "status.json"), m.status, 0600)
		return err
	}
	m.status = status
	return nil
}

func (m *Manager) download(ctx context.Context, releaseVersion, address, name string, size int64, digest []byte) (string, error) {
	dir := filepath.Join(m.dir, releaseVersion)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if existing, err := os.Open(path); err == nil {
		hash := sha256.New()
		_, copyErr := io.Copy(hash, io.LimitReader(existing, maxPackageSize+1))
		_ = existing.Close()
		info, statErr := os.Stat(path)
		if copyErr == nil && statErr == nil && info.Size() == size && strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(digest)) {
			return path, nil
		}
	}
	response, err := m.request(ctx, address, true)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release download returned HTTP %d", response.StatusCode)
	}
	file, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxPackageSize+1))
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written != size || written > maxPackageSize {
		return "", errors.New("release package size mismatch")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(digest)) {
		return "", errors.New("release package SHA-256 mismatch")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
