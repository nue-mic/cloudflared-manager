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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultRepo       = "nue-mic/cloudflared-manager"
	defaultInstallSh  = "https://raw.githubusercontent.com/nue-mic/cloudflared-manager/main/scripts/install.sh"
	defaultInstallPs1 = "https://raw.githubusercontent.com/nue-mic/cloudflared-manager/main/scripts/install.ps1"
	// defaultSelfUpdateKey is the self-hosted Release-proxy key for the
	// MANAGER's own releases (the "check for update" path). The install
	// scripts use a separate key (cfd-mgr) for the binary download.
	defaultSelfUpdateKey = "cfd-mgr-releases"
	cacheTTL             = time.Hour
	httpTimeout          = 12 * time.Second
)

// defaultProxyBases are the self-hosted GitHub-Release proxy domains
// (docs/三方对接_RELEASE_API.md). All equivalent; tried in order with
// failover. Self-update fetches {base}/{key}/latest to learn the newest
// manager version — it never talks to api.github.com directly.
var defaultProxyBases = []string{
	"https://gh-raw.966788.xyz",
	"https://gh-raw.988669.xyz",
	"https://gh-raw.s03.qzz.io",
	"https://gh-raw.s04.qzz.io",
	"https://gh-raw.s05.qzz.io",
	"https://gh-raw.s06.qzz.io",
	"https://gh-raw.s07.qzz.io",
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
	// Repo is the "owner/name" GitHub repo. Defaults to nue-mic/cloudflared-manager.
	Repo string
	// InstallShURL / InstallPs1URL point at the installer scripts the spawned
	// updater downloads. Empty values fall back to the official raw URLs,
	// overridable via CFDM_INSTALL_SH_URL / CFDM_INSTALL_PS1_URL.
	InstallShURL  string
	InstallPs1URL string
	// DataDir is where update.log is written.
	DataDir string
	// ProxyBases is the ordered list of Release-proxy domains used for the
	// update check; empty => env CFDM_RELEASE_PROXY_BASES or defaultProxyBases.
	ProxyBases []string
	// ProxyKey is the proxy config key for the manager's releases;
	// empty => env CFDM_SELFUPDATE_PROXY_KEY or defaultSelfUpdateKey.
	ProxyKey string
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
	if len(cfg.ProxyBases) == 0 {
		cfg.ProxyBases = splitCSV(env("CFDM_RELEASE_PROXY_BASES", ""))
		if len(cfg.ProxyBases) == 0 {
			cfg.ProxyBases = defaultProxyBases
		}
	}
	if cfg.ProxyKey == "" {
		cfg.ProxyKey = env("CFDM_SELFUPDATE_PROXY_KEY", defaultSelfUpdateKey)
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
	var lastErr error
	for _, base := range u.cfg.ProxyBases {
		base = strings.TrimRight(strings.TrimSpace(base), "/")
		if base == "" {
			continue
		}
		rel, err := u.fetchOne(ctx, base+"/"+u.cfg.ProxyKey+"/latest")
		if err == nil {
			return rel, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no release-proxy bases configured")
	}
	return nil, fmt.Errorf("query latest release failed: %w", lastErr)
}

func (u *Updater) fetchOne(ctx context.Context, url string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cfdmgrd-selfupdate")

	resp, err := u.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// proxy errors are text/plain; surface a trimmed snippet for diagnostics.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<12))
		return nil, fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, url, strings.TrimSpace(string(body)))
	}

	// Release-proxy single-version shape (docs/三方对接_RELEASE_API.md §3.2):
	// uses "tag" (not "tag_name") and carries no html_url.
	var payload struct {
		Tag         string `json:"tag"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.Tag) == "" {
		return nil, fmt.Errorf("empty tag in release response")
	}
	return &Release{
		Tag:         payload.Tag,
		Changelog:   payload.Body,
		HTMLURL:     "https://github.com/" + u.cfg.Repo + "/releases/tag/" + payload.Tag,
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

// ResetLog truncates update.log and writes a fresh header, so each update run
// starts with a clean log that the web UI can stream as live progress (the
// spawned updater appends its step output to the same file).
func (u *Updater) ResetLog(from, to string) {
	f, err := os.Create(u.logPath())
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[*] 准备自更新: %s -> %s\n", from, to)
}

// ReadLog returns the tail of update.log (best-effort, capped) so the web UI
// can show the current update's progress. Empty string when absent.
func (u *Updater) ReadLog() string {
	b, err := os.ReadFile(u.logPath())
	if err != nil {
		return ""
	}
	const maxLog = 64 << 10 // 64 KiB tail is more than enough
	if len(b) > maxLog {
		b = b[len(b)-maxLog:]
	}
	return string(b)
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

// splitCSV splits a comma-separated string into trimmed, non-empty parts.
func splitCSV(s string) []string {
	out := make([]string, 0)
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
