// Package update checks GitHub Releases and stages verified server packages.
package update

import (
	"context"
	"crypto/sha256"
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
	Message        string    `json:"message,omitempty"`
	CheckedAt      time.Time `json:"checked_at,omitzero"`
}

type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name   string `json:"name"`
		APIURL string `json:"url"`
		URL    string `json:"browser_download_url"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

type Manager struct {
	mu         sync.Mutex
	status     Status
	dir        string
	repository func() string
	client     *http.Client
	apiBase    string
	allowHTTP  bool // Test servers only; production URLs must use HTTPS.
}

func NewManager(dir string, repository func() string) *Manager {
	return &Manager{
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
	status := m.status
	repository := m.repository()
	if status.Repository != repository {
		status = Status{State: "disabled", Repository: repository, CurrentVersion: version.Version}
		if repository != "" { status.State = "checking" }
	}
	return status
}

func (m *Manager) begin() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State == "checking" || m.status.State == "downloading" {
		return false
	}
	m.status.State = "checking"
	m.status.Message = ""
	return true
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
}

func (m *Manager) Check(ctx context.Context) error {
	if !m.begin() {
		return errors.New("update check is already running")
	}
	return m.run(ctx)
}

func (m *Manager) run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
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
	go func() {
		m.Trigger()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.Trigger()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func packageName() string {
	extension := ".tar.gz"
	if runtime.GOOS == "windows" {
		extension = ".zip"
	}
	return "musicserver-" + runtime.GOOS + "-" + runtime.GOARCH + extension
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
	response, err := m.request(ctx, m.apiBase+"/repos/"+path+"/releases/latest", false)
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
	if order <= 0 {
		status.State = "current"
		return status, nil
	}
	name := packageName()
	for _, asset := range latest.Assets {
		if asset.Name != name {
			continue
		}
		if asset.Size <= 0 || asset.Size > maxPackageSize {
			return status, errors.New("release package size is invalid")
		}
		if !strings.HasPrefix(asset.Digest, "sha256:") || len(asset.Digest) != 71 {
			return status, errors.New("release package is missing a SHA-256 digest")
		}
		digest, err := hex.DecodeString(strings.TrimPrefix(asset.Digest, "sha256:"))
		if err != nil {
			return status, fmt.Errorf("invalid release digest: %w", err)
		}
		status.State = "downloading"
		m.mu.Lock()
		m.status = status
		m.mu.Unlock()
		address := asset.URL
		if os.Getenv("MUSIC_SERVER_GITHUB_TOKEN") != "" && asset.APIURL != "" {
			address = asset.APIURL
		}
		packagePath, err := m.download(ctx, status.LatestVersion, address, name, asset.Size, digest)
		if err != nil {
			return status, err
		}
		status.State = "downloaded"
		status.PackagePath = packagePath
		return status, nil
	}
	return status, fmt.Errorf("release has no package named %s", name)
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
