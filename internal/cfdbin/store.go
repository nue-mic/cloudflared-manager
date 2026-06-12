package cfdbin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// versionTagRE constrains a resolved version to a single safe path segment,
// preventing a crafted binaryVersion (e.g. "../../bin/sh") from escaping the
// store root and executing an arbitrary binary.
var versionTagRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validVersionTag(v string) bool { return versionTagRE.MatchString(v) }

// VersionMeta is what we persist next to each downloaded binary
// (`<root>/<version>/meta.json`).
type VersionMeta struct {
	Version      string    `json:"version"`
	Platform     string    `json:"platform"`
	Arch         string    `json:"arch"`
	AssetName    string    `json:"asset_name"`
	SHA256       string    `json:"sha256"`
	SourceURL    string    `json:"source_url"`
	Mirror       string    `json:"mirror,omitempty"`
	DownloadedAt time.Time `json:"downloaded_at"`
	SizeBytes    int64     `json:"size_bytes"`
	Verified     bool      `json:"verified"`
}

// activeFile is the on-disk shape of `<root>/active.json`.
type activeFile struct {
	Version string `json:"version"`
}

// Store owns the directory tree at `{data_dir}/bin/cloudflared/`. It is
// safe for concurrent use; mutations serialise on mu.
type Store struct {
	root string

	mu sync.Mutex
}

// ErrNotInstalled is returned by Resolve and Delete when the named
// version directory does not contain a verified binary.
var ErrNotInstalled = errors.New("cfdbin: version not installed")

// ErrNoActive is returned by Resolve when active.json is missing and the
// caller asked for "" / "current".
var ErrNoActive = errors.New("cfdbin: no active version")

// New constructs a Store rooted at the given directory. The directory
// is created lazily on first write; New itself does not touch the FS.
func New(rootDir string) *Store {
	return &Store{root: rootDir}
}

// Root returns the directory the store manages.
func (s *Store) Root() string { return s.root }

// versionDir returns the on-disk directory for a specific version tag.
func (s *Store) versionDir(version string) string {
	return filepath.Join(s.root, version)
}

// binaryPath returns the full path of the executable for a specific
// version (e.g. `.../2026.5.2/cloudflared` or `cloudflared.exe`).
func (s *Store) binaryPath(version string) string {
	return filepath.Join(s.versionDir(version), BinaryFilename(runtime.GOOS))
}

// activePath returns the canonical `active.json` path.
func (s *Store) activePath() string {
	return filepath.Join(s.root, "active.json")
}

// readActive returns the active version recorded in active.json. Returns
// ErrNoActive when the file does not exist or is malformed.
func (s *Store) readActive() (string, error) {
	b, err := os.ReadFile(s.activePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoActive
		}
		return "", err
	}
	var a activeFile
	if err := json.Unmarshal(b, &a); err != nil {
		return "", fmt.Errorf("active.json malformed: %w", err)
	}
	if a.Version == "" {
		return "", ErrNoActive
	}
	return a.Version, nil
}

// writeActive atomically replaces active.json. The temp+rename keeps a
// concurrent Resolve from reading a half-written file.
func (s *Store) writeActive(version string) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	tmp := s.activePath() + ".tmp"
	b, err := json.MarshalIndent(activeFile{Version: version}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.activePath())
}

// Resolve returns the absolute path to the cloudflared binary for the
// given version tag. "" or "current" means "use active.json". Returns
// ErrNotInstalled if the resolved version has no on-disk binary.
func (s *Store) Resolve(version string) (string, error) {
	v := strings.TrimSpace(version)
	if v == "" || v == "current" {
		var err error
		v, err = s.readActive()
		if err != nil {
			return "", err
		}
	}
	if !validVersionTag(v) {
		return "", ErrNotInstalled
	}
	p := s.binaryPath(v)
	st, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotInstalled
		}
		return "", err
	}
	if st.IsDir() {
		return "", ErrNotInstalled
	}
	return p, nil
}

// ActiveVersion returns the version tag recorded in active.json, or "" when
// none is set or the file is unreadable. Unlike Resolve it does NOT verify the
// binary exists on disk — callers use it for display ("which version is
// configured to run"), not for execution.
func (s *Store) ActiveVersion() string {
	v, err := s.readActive()
	if err != nil {
		return ""
	}
	return v
}

// InstalledVersion describes one entry returned by List.
type InstalledVersion struct {
	Version      string    `json:"version"`
	Path         string    `json:"path"`
	SHA256       string    `json:"sha256,omitempty"`
	SourceURL    string    `json:"source_url,omitempty"`
	Mirror       string    `json:"mirror,omitempty"`
	DownloadedAt time.Time `json:"downloaded_at,omitempty"`
	SizeBytes    int64     `json:"size_bytes,omitempty"`
	Verified     bool      `json:"verified"`
	IsActive     bool      `json:"is_active"`
}

// List returns all installed versions discovered under root, newest tag
// first (lexicographic descending — CalVer sorts correctly that way).
func (s *Store) List() ([]InstalledVersion, error) {
	active, _ := s.readActive() // ignore "no active"; treat as ""
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []InstalledVersion{}, nil
		}
		return nil, err
	}
	out := make([]InstalledVersion, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ver := e.Name()
		if ver == "current" {
			continue // symlink
		}
		bp := s.binaryPath(ver)
		st, err := os.Stat(bp)
		if err != nil || st.IsDir() {
			continue
		}
		iv := InstalledVersion{
			Version:   ver,
			Path:      bp,
			SizeBytes: st.Size(),
			IsActive:  ver == active,
		}
		if m, err := s.readMeta(ver); err == nil {
			iv.SHA256 = m.SHA256
			iv.SourceURL = m.SourceURL
			iv.Mirror = m.Mirror
			iv.DownloadedAt = m.DownloadedAt
			iv.Verified = m.Verified
			if m.SizeBytes > 0 {
				iv.SizeBytes = m.SizeBytes
			}
		}
		out = append(out, iv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

// Activate marks version as the current. Fails if version is not
// installed (Resolve must succeed). Updates active.json atomically and
// best-effort refreshes the `current` symlink on Linux/Darwin.
func (s *Store) Activate(version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.Resolve(version); err != nil {
		return err
	}
	if err := s.writeActive(version); err != nil {
		return err
	}
	// best-effort symlink refresh
	if runtime.GOOS != "windows" {
		link := filepath.Join(s.root, "current")
		_ = os.Remove(link)
		_ = os.Symlink(version, link)
	}
	return nil
}

// Delete removes the version directory. Fails if version is currently
// active or if the directory does not exist.
func (s *Store) Delete(version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	active, _ := s.readActive()
	if version == active {
		return fmt.Errorf("cfdbin: version %s is active; cannot delete", version)
	}
	dir := s.versionDir(version)
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotInstalled
		}
		return err
	}
	return os.RemoveAll(dir)
}

// metaPath returns the meta.json path for a version dir.
func (s *Store) metaPath(version string) string {
	return filepath.Join(s.versionDir(version), "meta.json")
}

func (s *Store) readMeta(version string) (VersionMeta, error) {
	var m VersionMeta
	b, err := os.ReadFile(s.metaPath(version))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, nil
}

func (s *Store) writeMeta(version string, m VersionMeta) error {
	if err := os.MkdirAll(s.versionDir(version), 0o755); err != nil {
		return err
	}
	tmp := s.metaPath(version) + ".tmp"
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.metaPath(version))
}
