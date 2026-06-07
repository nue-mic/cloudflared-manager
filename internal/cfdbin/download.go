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
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// DefaultGitHubAPI is the canonical releases endpoint. Tests override.
const DefaultGitHubAPI = "https://api.github.com/repos/cloudflare/cloudflared"

// Downloader resolves remote release metadata and fetches asset bytes.
type Downloader struct {
	HTTPClient  *http.Client
	GitHubAPI   string   // defaults to DefaultGitHubAPI
	Mirrors     []string // URL prefixes tried in order before plain github.com
	GitHubToken string   // optional, raises API rate limits when set
}

// AvailableRelease summarises one GitHub release as exposed via
// `GET /api/v1/binaries/available`.
type AvailableRelease struct {
	TagName     string    `json:"tag_name"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	AssetURL    string    `json:"asset_url,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
}

// Available returns the latest release plus the most recent N releases
// from GitHub. limit <= 0 defaults to 10.
func (d *Downloader) Available(ctx context.Context, limit int) ([]AvailableRelease, error) {
	if limit <= 0 {
		limit = 10
	}
	api := d.GitHubAPI
	if api == "" {
		api = DefaultGitHubAPI
	}
	body, err := d.getGitHubJSON(ctx, api+"/releases?per_page="+fmt.Sprint(limit))
	if err != nil {
		return nil, err
	}
	var raw []struct {
		TagName     string    `json:"tag_name"`
		PublishedAt time.Time `json:"published_at"`
		HTMLURL     string    `json:"html_url"`
		Body        string    `json:"body"`
		Assets      []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	assetName, _ := CurrentAssetName()
	out := make([]AvailableRelease, 0, len(raw))
	for _, r := range raw {
		entry := AvailableRelease{
			TagName:     r.TagName,
			PublishedAt: r.PublishedAt,
			HTMLURL:     r.HTMLURL,
		}
		for _, a := range r.Assets {
			if a.Name == assetName {
				entry.AssetURL = a.DownloadURL
				break
			}
		}
		entry.SHA256 = ParseSHA256(r.Body, assetName)
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

// Install downloads version, verifies SHA256, applies platform post-
// processing (chmod / xattr stubbed for PR-10), and persists to
// `<root>/<version>/`. Returns the verified VersionMeta on success.
// Activation is deliberately separate (callers may want to install in
// the background without flipping the active pointer).
func (s *Store) Install(ctx context.Context, d *Downloader, version string) (VersionMeta, error) {
	assetName, ok := CurrentAssetName()
	if !ok {
		return VersionMeta{}, fmt.Errorf("cfdbin: unsupported target %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// 1. fetch release metadata to get the official SHA256
	api := d.GitHubAPI
	if api == "" {
		api = DefaultGitHubAPI
	}
	relURL := api + "/releases/tags/" + version
	if v := strings.TrimSpace(version); v == "" || v == "latest" {
		relURL = api + "/releases/latest" // resolve the floating "latest" tag
	}
	body, err := d.getGitHubJSON(ctx, relURL)
	if err != nil {
		return VersionMeta{}, fmt.Errorf("release lookup: %w", err)
	}
	var rel struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		Assets  []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return VersionMeta{}, fmt.Errorf("decode release: %w", err)
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return VersionMeta{}, fmt.Errorf("release %q has no tag_name", version)
	}
	version = rel.TagName // pin to the concrete tag (also resolves "latest")
	wantSHA := ParseSHA256(rel.Body, assetName)
	if wantSHA == "" {
		return VersionMeta{}, fmt.Errorf("release %s has no SHA256 for %s", version, assetName)
	}
	directURL := ""
	for _, a := range rel.Assets {
		if a.Name == assetName {
			directURL = a.DownloadURL
			break
		}
	}
	if directURL == "" {
		return VersionMeta{}, fmt.Errorf("release %s has no asset %s", version, assetName)
	}

	// 2. download bytes via mirror chain
	tmp, err := os.CreateTemp("", "cloudflared-dl-*")
	if err != nil {
		return VersionMeta{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	tmp.Close()

	sha, size, mirror, err := d.downloadWithMirrors(ctx, directURL, tmp.Name())
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
	// platform post-processing (xattr / Unblock-File) is stubbed; see
	// spec §4.6. PR-10 wires it up alongside the install scripts.

	meta := VersionMeta{
		Version:      version,
		Platform:     runtime.GOOS,
		Arch:         runtime.GOARCH,
		AssetName:    assetName,
		SHA256:       verifiedSHA,
		SourceURL:    directURL,
		Mirror:       mirror,
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

// downloadWithMirrors tries each mirror prefix in front of the direct
// URL until one yields bytes that match SHA. Returns the computed SHA,
// downloaded size, and the mirror URL used (empty when direct).
func (d *Downloader) downloadWithMirrors(ctx context.Context, directURL, destPath string) (string, int64, string, error) {
	urls := make([]string, 0, len(d.Mirrors)+1)
	for _, m := range d.Mirrors {
		if m == "" {
			continue
		}
		if strings.HasSuffix(m, "/") {
			urls = append(urls, m+directURL)
		} else {
			urls = append(urls, m+"/"+directURL)
		}
	}
	urls = append(urls, directURL)

	var lastErr error
	for _, u := range urls {
		sha, size, err := d.downloadOne(ctx, u, destPath)
		if err == nil {
			mirror := ""
			if u != directURL {
				mirror = strings.TrimSuffix(u, directURL)
			}
			return sha, size, mirror, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no urls tried")
	}
	return "", 0, "", lastErr
}

func (d *Downloader) downloadOne(ctx context.Context, url, destPath string) (string, int64, error) {
	client := d.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("http %d from %s", resp.StatusCode, url)
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

// getGitHubJSON makes an authenticated GET against api.github.com,
// returning the decoded JSON bytes. Mirrors are NOT used for API calls
// because mirrors typically only proxy releases/download paths.
func (d *Downloader) getGitHubJSON(ctx context.Context, url string) ([]byte, error) {
	client := d.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "cfdmgrd-cfdbin")
	if d.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+d.GitHubToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
		return nil, fmt.Errorf("github api %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
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
