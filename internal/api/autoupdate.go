package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/nue-mic/cloudflared-manager/internal/cfdupdate"
)

// AutoUpdateHandler exposes the cloudflared binary auto-update endpoints:
//
//	GET  /api/v1/binaries/auto-update      — settings + live status
//	PUT  /api/v1/binaries/auto-update      — change settings (partial), applied at once
//	POST /api/v1/binaries/auto-update/run  — trigger a check/download/apply now
//
// A nil updater (auto-update disabled at build/wiring time) makes every
// endpoint return 503 so the UI can degrade gracefully.
type AutoUpdateHandler struct {
	upd    *cfdupdate.Updater
	logger *slog.Logger
}

// NewAutoUpdateHandler wires the handler. upd may be nil.
func NewAutoUpdateHandler(upd *cfdupdate.Updater, logger *slog.Logger) *AutoUpdateHandler {
	return &AutoUpdateHandler{upd: upd, logger: logger}
}

func (h *AutoUpdateHandler) unavailable(w http.ResponseWriter) bool {
	if h.upd == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeInternal, "binary auto-update not configured", nil)
		return true
	}
	return false
}

// Get returns the current settings and live status.
func (h *AutoUpdateHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"settings": h.upd.Settings(),
		"status":   h.upd.Status(),
	})
}

// Put applies a partial settings change. Omitted fields keep their current
// value (the body is decoded onto a copy of the live settings); unknown keys
// are rejected by decodeJSON's DisallowUnknownFields.
func (h *AutoUpdateHandler) Put(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	next := h.upd.Settings() // start from current → partial update semantics
	if !decodeJSON(w, r, &next) {
		return
	}
	saved, err := h.upd.SetSettings(next)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, "save settings: "+err.Error(), nil)
		return
	}
	h.logger.Info("binary auto-update settings updated",
		slog.Bool("enabled", saved.Enabled), slog.String("mode", saved.Mode),
		slog.Int("interval_hours", saved.IntervalHours))
	WriteJSON(w, http.StatusOK, map[string]any{
		"settings": saved,
		"status":   h.upd.Status(),
	})
}

// Run triggers an update operation in the background and returns 202. Body
// (all optional):
//
//	{"version":"2026.5.2", "apply":true, "force":true}
//
//   - version: target a specific tag instead of latest.
//   - apply:   force activate + restart regardless of mode (manual "update now").
//     Omitted/false honours the configured mode (like a scheduled check).
//   - force:   reinstall/reactivate even when already current.
//
// Returns 409 when an operation is already running.
func (h *AutoUpdateHandler) Run(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	var body struct {
		Version string `json:"version"`
		Apply   bool   `json:"apply"`
		Force   bool   `json:"force"`
	}
	// Body is optional (empty / no Content-Length / chunked all tolerated).
	if !decodeJSONOptional(w, r, &body) {
		return
	}
	if body.Version != "" && !validVersionParam(body.Version) {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid version tag", nil)
		return
	}

	err := h.upd.TriggerAsync(cfdupdate.RunOpts{
		Version: body.Version,
		Apply:   body.Apply,
		Force:   body.Force,
	})
	if errors.Is(err, cfdupdate.ErrBusy) {
		WriteError(w, http.StatusConflict, CodeConflict, "更新操作正在进行中，请稍候", nil)
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, "trigger update: "+err.Error(), nil)
		return
	}
	h.logger.Info("binary auto-update triggered",
		slog.String("version", body.Version), slog.Bool("apply", body.Apply), slog.Bool("force", body.Force))
	WriteJSON(w, http.StatusAccepted, map[string]any{
		"status":  "running",
		"message": "更新已开始，进度可在事件流中查看",
	})
}
