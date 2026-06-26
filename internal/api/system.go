package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/nue-mic/cloudflared-manager/internal/sysinfo"
	"github.com/nue-mic/cloudflared-manager/pkg/version"
)

// SystemHandler exposes /health, /version and /api/v1/system/*.
//
// /health does not require authentication so container probes can hit it
// freely. Everything under /api/v1/system/ is gated by the bearer middleware.
type SystemHandler struct {
	startedAt time.Time
	dataDir   string
}

// NewSystemHandler creates a SystemHandler stamped with the current time.
// dataDir is reported alongside disk usage so the API caller can see
// where the persistent volume lives.
func NewSystemHandler(dataDir string) *SystemHandler {
	return &SystemHandler{startedAt: time.Now(), dataDir: dataDir}
}

// Health responds with a small JSON body suitable for liveness probes.
func (s *SystemHandler) Health(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"uptime_s": int64(time.Since(s.startedAt).Seconds()),
	})
}

// Version reports the daemon version and build date. The cloudflared
// binary version is reported by /api/v1/binaries (added in PR-05).
func (s *SystemHandler) Version(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{
		"daemon":     version.Number,
		"build_date": version.BuildDate,
	})
}

// Info returns an aggregate snapshot useful for a dashboard landing page:
// host info + cpu + memory + disk + interfaces + connection summary +
// daemon process. Each block is best-effort — if one collector fails the
// rest are still returned.
func (s *SystemHandler) Info(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{
		"uptime_s": int64(time.Since(s.startedAt).Seconds()),
		"data_dir": s.dataDir,
	}
	if v, err := sysinfo.Host(ctx); err == nil {
		out["host"] = v
	}
	if v, err := sysinfo.CPU(ctx, 200*time.Millisecond); err == nil {
		out["cpu"] = v
	}
	if v, err := sysinfo.Memory(ctx); err == nil {
		out["memory"] = v
	}
	if v, err := sysinfo.Disk(ctx, s.diskPaths()...); err == nil {
		out["disk"] = v
	}
	if v, err := sysinfo.Interfaces(ctx); err == nil {
		out["network"] = v
	}
	if v, err := sysinfo.Connections(ctx); err == nil {
		out["connections"] = v
	}
	if v, err := sysinfo.Process(ctx); err == nil {
		out["process"] = v
	}
	WriteJSON(w, http.StatusOK, out)
}

// CPU exposes CPU metrics. `window` query param (default 200ms) controls
// the sampling window; pass `?window=1s` for a more stable reading.
func (s *SystemHandler) CPU(w http.ResponseWriter, r *http.Request) {
	window := parseDuration(r.URL.Query().Get("window"), 200*time.Millisecond)
	v, err := sysinfo.CPU(r.Context(), window)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return
	}
	WriteJSON(w, http.StatusOK, v)
}

// Memory exposes virtual + swap memory.
func (s *SystemHandler) Memory(w http.ResponseWriter, r *http.Request) {
	v, err := sysinfo.Memory(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return
	}
	WriteJSON(w, http.StatusOK, v)
}

// Disk reports usage for the data dir plus root. Custom paths can be
// added via `?paths=/foo,/bar` (comma-separated, absolute).
func (s *SystemHandler) Disk(w http.ResponseWriter, r *http.Request) {
	paths := s.diskPaths()
	if extra := r.URL.Query().Get("paths"); extra != "" {
		for _, p := range strings.Split(extra, ",") {
			if t := strings.TrimSpace(p); t != "" {
				paths = append(paths, t)
			}
		}
	}
	v, err := sysinfo.Disk(r.Context(), paths...)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": v})
}

// Network exposes per-interface byte and packet counters.
func (s *SystemHandler) Network(w http.ResponseWriter, r *http.Request) {
	v, err := sysinfo.Interfaces(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": v})
}

// Connections returns the host-wide socket count summary plus a daemon-
// owned subset.
func (s *SystemHandler) Connections(w http.ResponseWriter, r *http.Request) {
	v, err := sysinfo.Connections(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return
	}
	WriteJSON(w, http.StatusOK, v)
}

// Process returns information about the daemon process itself.
func (s *SystemHandler) Process(w http.ResponseWriter, r *http.Request) {
	v, err := sysinfo.Process(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return
	}
	WriteJSON(w, http.StatusOK, v)
}

func (s *SystemHandler) diskPaths() []string {
	if s.dataDir == "" {
		return []string{"/"}
	}
	if s.dataDir == "/" {
		return []string{"/"}
	}
	return []string{"/", s.dataDir}
}

func parseDuration(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > 5*time.Second {
		return def
	}
	return d
}
