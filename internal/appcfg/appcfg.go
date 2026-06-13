package appcfg

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"time"
)

// Config is the daemon's own runtime configuration, populated from env vars.
type Config struct {
	HTTPAddr    string
	APIToken    string
	CORSOrigins []string
	DataDir     string
	ProfilesDir string
	LogsDir     string
	StoresDir   string
	MetaFile    string
	// CFStoreFile holds Cloudflare accounts + instance bindings (secrets
	// encrypted at rest). Defaults to {DataDir}/cf-store.json.
	CFStoreFile string
	// SecretKeyFile is the data-encryption key for cf-store secrets,
	// auto-generated on first use. Defaults to {DataDir}/secret.key.
	SecretKeyFile string
	LogLevel      string
	DocsEnabled   bool
	// SelfUpdateEnabled gates the web-triggered self-update endpoint
	// (POST /api/v1/system/update). It maps to CFDM_SELF_UPDATE_ENABLED
	// and defaults to true. Operators running immutable deployments can set
	// it to false to disable in-place upgrades from the UI.
	SelfUpdateEnabled bool
	// BinariesDir is the root directory managed by cfdbin.Store.
	// Defaults to {DataDir}/bin/cloudflared.
	BinariesDir string
	// DownloadMirrors is a legacy list of GitHub mirror prefixes. Retained
	// for the self-update path; the cloudflared binary downloader no longer
	// uses it (it goes through ReleaseProxyBases instead).
	DownloadMirrors []string
	// GitHubToken is an optional GitHub personal-access-token (legacy; the
	// release proxy needs no token).
	GitHubToken string
	// ReleaseProxyBases is the ordered list of GitHub-Release proxy domains
	// the cloudflared binary downloader uses (see docs/三方对接_RELEASE_API.md).
	// All are equivalent; failover is automatic. CSV via env, may override.
	ReleaseProxyBases []string
	// ReleaseProxyKey is the proxy config key mapped to cloudflare/cloudflared.
	ReleaseProxyKey string
	// CloudflaredDefaultVersion is the version string that /api/v1/binaries
	// Install uses when the caller omits the "version" field.
	// Default: "latest".
	CloudflaredDefaultVersion string
	ShutdownWait              time.Duration
}

// Load reads configuration from environment variables. Required fields
// without sensible defaults will return an error.
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:    getEnv("CFDM_HTTP_ADDR", ":8080"),
		APIToken:    os.Getenv("CFDM_API_TOKEN"),
		CORSOrigins: splitCSV(getEnv("CFDM_CORS_ORIGINS", "*")),
		DataDir:     getEnv("CFDM_DATA_DIR", defaultDataDir()),
		LogLevel:    strings.ToLower(getEnv("CFDM_LOG_LEVEL", "info")),
		DocsEnabled: parseBool(getEnv("CFDM_DOCS_ENABLED", "true"), true),

		SelfUpdateEnabled: parseBool(getEnv("CFDM_SELF_UPDATE_ENABLED", "true"), true),

		DownloadMirrors: splitCSV(getEnv("CFDM_DOWNLOAD_MIRRORS", "https://gh-proxy.org/,https://gh-proxy.com/")),
		GitHubToken:     os.Getenv("CFDM_GITHUB_TOKEN"),
		ReleaseProxyBases: splitCSV(getEnv("CFDM_RELEASE_PROXY_BASES",
			"https://gh-raw.966788.xyz,https://gh-raw.988669.xyz,https://gh-raw.s03.qzz.io,https://gh-raw.s04.qzz.io,https://gh-raw.s05.qzz.io,https://gh-raw.s06.qzz.io,https://gh-raw.s07.qzz.io")),
		ReleaseProxyKey:           getEnv("CFDM_RELEASE_PROXY_KEY", "cloudflared-releases"),
		CloudflaredDefaultVersion: getEnv("CFDM_CLOUDFLARED_DEFAULT_VERSION", "latest"),
		ShutdownWait:              10 * time.Second,
	}
	cfg.ProfilesDir = cfg.DataDir + "/profiles"
	cfg.LogsDir = cfg.DataDir + "/logs"
	cfg.StoresDir = cfg.DataDir + "/stores"
	cfg.MetaFile = cfg.DataDir + "/meta.json"
	cfg.CFStoreFile = cfg.DataDir + "/cf-store.json"
	cfg.SecretKeyFile = cfg.DataDir + "/secret.key"
	cfg.BinariesDir = getEnv("CFDM_BINARIES_DIR", cfg.DataDir+"/bin/cloudflared")

	if cfg.APIToken == "" {
		return nil, errors.New("CFDM_API_TOKEN is required")
	}
	return cfg, nil
}

// EnsureDirs creates the data subdirectories if they do not exist.
func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.ProfilesDir, c.LogsDir, c.StoresDir, c.BinariesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// defaultDataDir picks a sane default per OS. The installer scripts and
// Dockerfile override CFDM_DATA_DIR explicitly so this only matters when
// users run cfdmgrd by hand without env vars set.
func defaultDataDir() string {
	// Windows: %ProgramData%\cfdmgrd 由安装脚本注入；缺失时回 C:\cfdmgrd
	// Linux/Darwin: /var/lib/cfdmgrd
	if runtime.GOOS == "windows" {
		if p := os.Getenv("ProgramData"); p != "" {
			return p + `\cfdmgrd`
		}
		return `C:\cfdmgrd`
	}
	return "/var/lib/cfdmgrd"
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseBool(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on", "y":
		return true
	case "0", "false", "no", "off", "n":
		return false
	default:
		return def
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
