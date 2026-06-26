package api

import (
	"log/slog"
	"net/http"

	"github.com/nue-mic/cloudflared-manager/internal/cfdbin"
	"github.com/nue-mic/cloudflared-manager/internal/manager"
	"github.com/nue-mic/cloudflared-manager/pkg/cfdconfig"
	"github.com/nue-mic/cloudflared-manager/pkg/cfdflags"
)

// ProjectionHandler serves GET /api/v1/configs/{id}/projection — the real
// TUNNEL_* environment and argv that cfdmgrd projects this instance's YAML
// config onto when it spawns cloudflared. It lets operators see exactly "what
// parameters this tunnel actually runs with". The connector token is always
// masked; the plaintext is never served.
type ProjectionHandler struct {
	m        *manager.Manager
	binStore *cfdbin.Store // may be nil; binary fields then degrade to PATH lookup
	log      *slog.Logger
}

// NewProjectionHandler builds a ProjectionHandler.
func NewProjectionHandler(m *manager.Manager, binStore *cfdbin.Store, log *slog.Logger) *ProjectionHandler {
	return &ProjectionHandler{m: m, binStore: binStore, log: log}
}

// Get assembles the projected env/argv/binary for the instance.
func (h *ProjectionHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	snap, sc, _, err := h.m.Get(id)
	if writeManagerError(w, err) {
		return
	}

	env := projectEnv(sc)
	// cfdmgrd-mandated env (mirrors manager/instance.startLocked). The token is
	// shown masked only; NO_AUTOUPDATE / TUNNEL_OUTPUT are constant; the metrics
	// address only exists while the instance is running.
	if sc.Token != "" {
		env["TUNNEL_TOKEN"] = maskToken(sc.Token)
	}
	env["NO_AUTOUPDATE"] = "true"
	env["TUNNEL_OUTPUT"] = "json"
	if addr, ok := h.m.MetricsAddr(id); ok {
		env["TUNNEL_METRICS"] = addr
	}

	argv := []string{"tunnel", "--no-autoupdate"}
	argv = append(argv, cfdflags.LabelArgv(sc.Identity.Label)...)
	argv = append(argv, "run")

	binPath := "cloudflared"
	if h.binStore != nil {
		if p, berr := h.binStore.Resolve(sc.BinaryVersion); berr == nil {
			binPath = p
		}
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"env":            env,
		"argv":           argv,
		"binary_version": snap.BinaryVersion,
		"binary_path":    binPath,
	})
}

// projectEnv mirrors the cfdflags.Options construction in
// manager/instance.startLocked so the displayed env matches what actually runs.
// It deliberately excludes the token and other mandated env (the caller adds a
// masked token) — keep this in sync with startLocked when the spawn env changes.
func projectEnv(cfg *cfdconfig.TunnelConfigV1) map[string]string {
	opts := cfdflags.Options{
		Protocol:             cfg.Edge.Protocol,
		EdgeIPVersion:        cfg.Edge.EdgeIPVersion,
		EdgeBindAddress:      cfg.Edge.EdgeBindAddress,
		Region:               cfg.Edge.Region,
		PostQuantum:          cfg.Edge.PostQuantum,
		Retries:              cfg.Reliability.Retries,
		GracePeriod:          cfg.Reliability.GracePeriod,
		LogLevel:             cfg.Logging.LogLevel,
		TransportLogLevel:    cfg.Logging.TransportLogLevel,
		Tags:                 cfg.Identity.Tags,
		Label:                cfg.Identity.Label,
		AdvancedEnvOverrides: cfg.AdvancedEnvOverrides,
	}
	return cfdflags.ToTunnelEnv(opts)
}
