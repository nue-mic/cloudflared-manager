// Package selfupdate queries the latest GitHub release of cloudflared-manager,
// compares it with the running version, detects how the daemon is deployed,
// and (where possible) launches a detached process that upgrades the binary
// in place and restarts the service.
//
// The actual upgrade work is delegated to the existing install.sh /
// install.ps1 scripts (which already handle every platform, init system,
// proxy fallback and checksum verification) — this package only orchestrates
// querying, version comparison and spawning the detached updater.
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultRepo       = "mia-clark/cloudflared-manager"
	defaultInstallSh  = "https://raw.githubusercontent.com/mia-clark/cloudflared-manager/main/scripts/install.sh"
	defaultInstallPs1 = "https://raw.githubusercontent.com/mia-clark/cloudflared-manager/main/scripts/install.ps1"
	cacheTTL          = time.Hour
	httpTimeout       = 12 * time.Second
)

// apiMirrors are prepended to the GitHub API URL when a direct request fails
// (helpful from networks where api.github.com is blocked). They mirror the
// CFDM_DOWNLOAD_MIRRORS default used by the binary downloader so the two
// GitHub-proxy paths stay consistent.
var apiMirrors = []string{
	"https://gh-proxy.org/",
	"https://gh-proxy.com/",
}

// Release is the subset of a GitHub release surfaced to the UI.
type Release struct {
	Tag         string `json:"tag"`
	Changelog   string `json:"changelog"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
}

// Config configures an Updater.
type Config struct {
	// Repo is the "owner/name" GitHub repo. Defaults to mia-clark/cloudflared-manager.
	Repo string
	// InstallShURL / InstallPs1URL point at the installer scripts the spawned
	// updater downloads. Empty values fall back to the official raw URLs,
	// overridable via CFDM_INSTALL_SH_URL / CFDM_INSTALL_PS1_URL.
	InstallShURL  string
	InstallPs1URL string
	// DataDir is where update.log is written.
	DataDir string
}

// Updater queries the latest release and orchestrates self-update.
type Updater struct {
	cfg  Config
	http *http.Client

	mu       sync.Mutex
	cached   *Release
	cachedAt time.Time
}

// New builds an Updater, filling in defaults for any unset Config fields.
func New(cfg Config) *Updater {
	if cfg.Repo == "" {
		cfg.Repo = defaultRepo
	}
	if cfg.InstallShURL == "" {
		cfg.InstallShURL = env("CFDM_INSTALL_SH_URL", defaultInstallSh)
	}
	if cfg.InstallPs1URL == "" {
		cfg.InstallPs1URL = env("CFDM_INSTALL_PS1_URL", defaultInstallPs1)
	}
	return &Updater{
		cfg:  cfg,
		http: &http.Client{Timeout: httpTimeout},
	}
}

// CheckLatest returns the latest release, served from a ~1h in-memory cache
// unless force is true. On a fetch error a previously cached value is
// returned if available (so transient GitHub outages don't blank the UI).
func (u *Updater) CheckLatest(ctx context.Context, force bool) (*Release, error) {
	u.mu.Lock()
	if !force && u.cached != nil && time.Since(u.cachedAt) < cacheTTL {
		r := u.cached
		u.mu.Unlock()
		return r, nil
	}
	stale := u.cached
	u.mu.Unlock()

	rel, err := u.fetchLatest(ctx)
	if err != nil {
		if stale != nil {
			return stale, nil
		}
		return nil, err
	}

	u.mu.Lock()
	u.cached = rel
	u.cachedAt = time.Now()
	u.mu.Unlock()
	return rel, nil
}

func (u *Updater) fetchLatest(ctx context.Context) (*Release, error) {
	base := "https://api.github.com/repos/" + u.cfg.Repo + "/releases/latest"
	urls := append([]string{base}, prefixMirrors(base)...)
	var lastErr error
	for _, raw := range urls {
		rel, err := u.fetchOne(ctx, raw)
		if err == nil {
			return rel, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("query latest release failed: %w", lastErr)
}

func (u *Updater) fetchOne(ctx context.Context, url string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "cfdmgrd-selfupdate")

	resp, err := u.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	var payload struct {
		TagName     string `json:"tag_name"`
		Body        string `json:"body"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.TagName) == "" {
		return nil, fmt.Errorf("empty tag_name in release response")
	}
	return &Release{
		Tag:         payload.TagName,
		Changelog:   payload.Body,
		HTMLURL:     payload.HTMLURL,
		PublishedAt: payload.PublishedAt,
	}, nil
}

// StartUpdate validates the deployment and launches a detached updater
// process that swaps the binary and restarts the service. It returns as soon
// as the updater is spawned — the daemon itself is about to be restarted.
func (u *Updater) StartUpdate(targetVersion string) error {
	mode := DetectDeployment()
	if ok, reason := CanSelfUpdate(mode); !ok {
		return fmt.Errorf("%s", reason)
	}
	return spawnUpdater(u, mode, targetVersion)
}

func (u *Updater) logPath() string {
	dir := u.cfg.DataDir
	if dir == "" {
		dir = tempDir()
	}
	return filepath.Join(dir, "update.log")
}

// HasUpdate reports whether latest is strictly newer than current.
func HasUpdate(current, latest string) bool {
	return CompareVersions(current, latest) < 0
}

// CompareVersions does a 3-segment numeric semver compare, tolerant of a
// leading "v" and any pre-release/build suffix. Returns -1, 0 or 1.
func CompareVersions(a, b string) int {
	pa, pb := parseVer(a), parseVer(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVer(s string) [3]int {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+ "); i >= 0 {
		s = s[:i]
	}
	var out [3]int
	for i, part := range strings.SplitN(s, ".", 3) {
		if i >= 3 {
			break
		}
		out[i], _ = strconv.Atoi(strings.TrimSpace(part))
	}
	return out
}

func prefixMirrors(u string) []string {
	out := make([]string, 0, len(apiMirrors))
	for _, m := range apiMirrors {
		out = append(out, m+u)
	}
	return out
}
