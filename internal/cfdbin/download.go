package cfdbin

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
	"strings"
	"time"
)

// DefaultReleaseProxyBases are the equivalent GitHub-Release proxy domains
// (see docs/三方对接_RELEASE_API.md). All are content-identical and互为备用;
// the two .xyz are primary, the .qzz.io are backups. Tried in order with
// failover on 5xx/429/network errors.
var DefaultReleaseProxyBases = []string{
	"https://gh-raw.966788.xyz",
	"https://gh-raw.988669.xyz",
	"https://gh-raw.s03.qzz.io",
	"https://gh-raw.s04.qzz.io",
	"https://gh-raw.s05.qzz.io",
	"https://gh-raw.s06.qzz.io",
	"https://gh-raw.s07.qzz.io",
}

// DefaultReleaseProxyKey is the proxy config key mapped to
// cloudflare/cloudflared on the proxy backend.
const DefaultReleaseProxyKey = "cloudflared-releases"

// GitHubRepoURL is the upstream repo, used only to synthesise an html_url for
// the UI (the proxy does not return one).
const GitHubRepoURL = "https://github.com/cloudflare/cloudflared"

// Downloader resolves release metadata and fetches asset bytes through the
// self-hosted GitHub-Release proxy. It never talks to api.github.com
// directly — that is the whole point of the proxy (stable from CN networks,
// no GitHub token required).
type Downloader struct {
	HTTPClient *http.Client
	// BaseURLs is the ordered list of proxy domains; empty => DefaultReleaseProxyBases.
	BaseURLs []string
	// Key is the proxy config key; empty => DefaultReleaseProxyKey.
	Key string
}

func (d *Downloader) bases() []string {
	if len(d.BaseURLs) > 0 {
		return d.BaseURLs
	}
	return DefaultReleaseProxyBases
}

func (d *Downloader) key() string {
	if strings.TrimSpace(d.Key) != "" {
		return d.Key
	}
	return DefaultReleaseProxyKey
}

func (d *Downloader) jsonClient() *http.Client {
	if d.HTTPClient != nil {
		return d.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// ── proxy JSON shapes (docs/三方对接_RELEASE_API.md §4) ──

type proxyAsset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	Download    string `json:"download"` // ready-to-use proxy URL (incl. token if protected)
}

type proxyRelease struct {
	Tag         string       `json:"tag"`
	Name        string       `json:"name"`
	Prerelease  bool         `json:"prerelease"`
	PublishedAt time.Time    `json:"published_at"`
	Body        string       `json:"body"` // only on single-version endpoint; carries SHA256 section
	Assets      []proxyAsset `json:"assets"`
}

type proxyList struct {
	Repo     string         `json:"repo"`
	Count    int            `json:"count"`
	Releases []proxyRelease `json:"releases"`
}

// AvailableRelease summarises one release as exposed via
// `GET /api/v1/binaries/available`.
type AvailableRelease struct {
	TagName     string    `json:"tag_name"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	AssetURL    string    `json:"asset_url,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
}

// Available returns the most recent N releases from the proxy. limit <= 0
// defaults to 10. SHA256 is empty here (the list endpoint carries no release
// body); the real checksum is fetched + verified at Install time.
func (d *Downloader) Available(ctx context.Context, limit int) ([]AvailableRelease, error) {
	if limit <= 0 {
		limit = 10
	}
	body, err := d.proxyGetJSON(ctx, fmt.Sprintf("/%s?per_page=%d", d.key(), limit))
	if err != nil {
		return nil, err
	}
	var list proxyList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	assetName, _ := CurrentAssetName()
	out := make([]AvailableRelease, 0, len(list.Releases))
	for _, r := range list.Releases {
		entry := AvailableRelease{
			TagName:     r.Tag,
			PublishedAt: r.PublishedAt,
			HTMLURL:     GitHubRepoURL + "/releases/tag/" + r.Tag,
		}
		for _, a := range r.Assets {
			if a.Name == assetName {
				entry.AssetURL = a.Download
				break
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// shaLineRE captures one "filename: hex64" line from a release body's
// "### SHA256 Checksums" section.
var shaLineRE = regexp.MustCompile(`(?m)^([A-Za-z0-9._\-]+):\s+([a-fA-F0-9]{64})\s*$`)

// ParseSHA256 extracts the SHA256 for assetName from a release body's
// markdown. Returns empty string when not found — callers must treat
// missing checksum as a hard error before persisting.
func ParseSHA256(releaseBody, assetName string) string {
	for _, m := range shaLineRE.FindAllStringSubmatch(releaseBody, -1) {
		if m[1] == assetName {
			return strings.ToLower(m[2])
		}
	}
	return ""
}

// Install downloads version through the proxy, verifies SHA256, applies
// platform post-processing, and persists to `<root>/<version>/`. Returns the
// verified VersionMeta on success. version "" or "latest" resolves to the
// proxy's latest release tag. Activation is deliberately separate.
func (s *Store) Install(ctx context.Context, d *Downloader, version string) (VersionMeta, error) {
	assetName, ok := CurrentAssetName()
	if !ok {
		return VersionMeta{}, fmt.Errorf("cfdbin: unsupported target %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// 1. fetch single-release metadata (carries body with SHA256 + per-asset
	//    proxy download URLs). "" / "latest" resolve to the latest tag.
	tag := strings.TrimSpace(version)
	if tag == "" {
		tag = "latest"
	}
	body, err := d.proxyGetJSON(ctx, "/"+d.key()+"/"+tag)
	if err != nil {
		return VersionMeta{}, fmt.Errorf("release lookup: %w", err)
	}
	var rel proxyRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return VersionMeta{}, fmt.Errorf("decode release: %w", err)
	}
	if strings.TrimSpace(rel.Tag) == "" {
		return VersionMeta{}, fmt.Errorf("release %q has no tag", version)
	}
	version = rel.Tag // pin to the concrete tag (also resolves "latest")

	wantSHA := ParseSHA256(rel.Body, assetName)
	if wantSHA == "" {
		return VersionMeta{}, fmt.Errorf("release %s has no SHA256 for %s", version, assetName)
	}
	dlURL := ""
	for _, a := range rel.Assets {
		if a.Name == assetName {
			dlURL = a.Download
			break
		}
	}
	if dlURL == "" {
		return VersionMeta{}, fmt.Errorf("release %s has no asset %s", version, assetName)
	}

	// Idempotent: if this exact version is already installed with the correct
	// checksum, return the existing meta without re-downloading — and without
	// touching the (possibly running, Windows-locked) binary on disk.
	if cur, herr := sha256File(s.binaryPath(version)); herr == nil && cur == wantSHA {
		if m, merr := s.readMeta(version); merr == nil {
			return m, nil
		}
		return VersionMeta{
			Version: version, Platform: runtime.GOOS, Arch: runtime.GOARCH,
			AssetName: assetName, SHA256: wantSHA, SourceURL: dlURL,
			DownloadedAt: time.Now().UTC(), Verified: true,
		}, nil
	}

	// 2. download bytes (proxy URL first, then the same path on the other
	//    proxy domains for failover).
	tmp, err := os.CreateTemp("", "cloudflared-dl-*")
	if err != nil {
		return VersionMeta{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	tmp.Close()

	sha, size, usedURL, err := d.downloadWithFailover(ctx, dlURL, tmp.Name())
	if err != nil {
		return VersionMeta{}, fmt.Errorf("download: %w", err)
	}
	// For RAW assets (linux/windows) the release-body checksum is the sha of
	// the bare downloaded file, so gate here. For ARCHIVE assets (darwin
	// .tgz) the body checksum is the sha of the binary INSIDE the tarball,
	// not the archive itself — that is verified after extraction below.
	if !IsArchive(assetName) && sha != wantSHA {
		return VersionMeta{}, fmt.Errorf("sha256 mismatch: got %s want %s", sha, wantSHA)
	}

	// 3. extract if archive; otherwise move bytes as-is
	if err := os.MkdirAll(s.versionDir(version), 0o755); err != nil {
		return VersionMeta{}, err
	}
	finalBin := s.binaryPath(version)
	verifiedSHA := sha
	if IsArchive(assetName) {
		if err := extractDarwinTGZ(tmp.Name(), finalBin); err != nil {
			_ = os.RemoveAll(s.versionDir(version))
			return VersionMeta{}, fmt.Errorf("extract: %w", err)
		}
		innerSHA, herr := sha256File(finalBin)
		if herr != nil {
			_ = os.RemoveAll(s.versionDir(version))
			return VersionMeta{}, fmt.Errorf("hash extracted binary: %w", herr)
		}
		if innerSHA != wantSHA {
			_ = os.RemoveAll(s.versionDir(version))
			return VersionMeta{}, fmt.Errorf("sha256 mismatch (extracted binary): got %s want %s", innerSHA, wantSHA)
		}
		verifiedSHA = innerSHA // persist the sha that actually matched the release body
	} else {
		if err := os.Rename(tmp.Name(), finalBin); err != nil {
			// cross-device rename can fail; fall back to copy
			if err := copyFile(tmp.Name(), finalBin); err != nil {
				return VersionMeta{}, err
			}
		}
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(finalBin, 0o755)
	}

	meta := VersionMeta{
		Version:      version,
		Platform:     runtime.GOOS,
		Arch:         runtime.GOARCH,
		AssetName:    assetName,
		SHA256:       verifiedSHA,
		SourceURL:    dlURL,
		Mirror:       mirrorHost(usedURL),
		DownloadedAt: time.Now().UTC(),
		SizeBytes:    size,
		Verified:     true,
	}
	if err := s.writeMeta(version, meta); err != nil {
		return meta, err
	}
	_ = os.WriteFile(filepath.Join(s.versionDir(version), ".verified"), nil, 0o644)
	return meta, nil
}

// downloadWithFailover downloads to destPath, trying downloadURL first and
// then the same path re-homed onto every other proxy base. Returns the
// computed SHA, size, and the URL that actually served the bytes.
func (d *Downloader) downloadWithFailover(ctx context.Context, downloadURL, destPath string) (string, int64, string, error) {
	candidates := d.failoverURLs(downloadURL)
	var lastErr error = errors.New("no urls tried")
	for _, u := range candidates {
		sha, size, err := d.downloadOne(ctx, u, destPath)
		if err == nil {
			return sha, size, u, nil
		}
		lastErr = err
	}
	return "", 0, "", lastErr
}

// failoverURLs returns downloadURL followed by the same path+query re-homed
// onto each other proxy base domain. Using the path verbatim preserves any
// access-token segment a protected key embeds in the download URL.
func (d *Downloader) failoverURLs(downloadURL string) []string {
	out := []string{downloadURL}
	u, err := url.Parse(downloadURL)
	if err != nil {
		return out
	}
	pathq := u.EscapedPath()
	if u.RawQuery != "" {
		pathq += "?" + u.RawQuery
	}
	origin := u.Scheme + "://" + u.Host
	for _, b := range d.bases() {
		b = strings.TrimRight(b, "/")
		if b == origin || b == "" {
			continue
		}
		out = append(out, b+pathq)
	}
	return out
}

func (d *Downloader) downloadOne(ctx context.Context, dlURL, destPath string) (string, int64, error) {
	client := d.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", "cfdmgrd-cfdbin")
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("http %d from %s", resp.StatusCode, dlURL)
	}
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	hasher := sha256.New()
	mw := io.MultiWriter(f, hasher)
	n, err := io.Copy(mw, resp.Body)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), n, nil
}

// proxyGetJSON GETs a proxy path (e.g. "/cloudflared-releases/latest") across
// the base domains in order. Definitive 4xx (except 429) short-circuit; 5xx /
// 429 / network errors fail over to the next domain.
func (d *Downloader) proxyGetJSON(ctx context.Context, path string) ([]byte, error) {
	var lastErr error = errors.New("no proxy bases configured")
	for _, base := range d.bases() {
		base = strings.TrimRight(base, "/")
		if base == "" {
			continue
		}
		b, status, err := d.getOne(ctx, base+path)
		if err == nil {
			return b, nil
		}
		lastErr = err
		// 4xx (except rate-limit) is the same on every equivalent domain.
		if status >= 400 && status < 500 && status != http.StatusTooManyRequests {
			return nil, lastErr
		}
	}
	return nil, lastErr
}

// getOne performs a single GET. The proxy returns errors as text/plain, so we
// surface the status code and a trimmed body for diagnostics.
func (d *Downloader) getOne(ctx context.Context, fullURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cfdmgrd-cfdbin")
	resp, err := d.jsonClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		txt, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
		return nil, resp.StatusCode, fmt.Errorf("proxy %d from %s: %s", resp.StatusCode, fullURL, strings.TrimSpace(string(txt)))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return b, http.StatusOK, err
}

// mirrorHost returns the scheme://host of a URL for diagnostics, or "".
func mirrorHost(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return ""
	}
	return p.Scheme + "://" + p.Host
}

// sha256File computes the hex sha256 of a file on disk. Used to verify the
// binary extracted from a .tgz archive against the release-body checksum.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyFile is a tiny cross-device-safe rename fallback.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
