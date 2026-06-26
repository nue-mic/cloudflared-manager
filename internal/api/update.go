package api

import (
	"log/slog"
	"net/http"

	"github.com/nue-mic/cloudflared-manager/internal/selfupdate"
	"github.com/nue-mic/cloudflared-manager/pkg/version"
)

// UpdateHandler serves the version-check and self-update endpoints:
//
//	GET  /api/v1/version/check  — compare running vs latest GitHub release
//	POST /api/v1/system/update  — launch a detached in-place upgrade + restart
type UpdateHandler struct {
	updater      *selfupdate.Updater
	selfUpdateOn bool
	logger       *slog.Logger
}

// NewUpdateHandler wires an UpdateHandler. selfUpdateOn reflects
// CFDM_SELF_UPDATE_ENABLED; when false the POST endpoint is refused.
func NewUpdateHandler(dataDir string, selfUpdateOn bool, logger *slog.Logger) *UpdateHandler {
	return &UpdateHandler{
		updater: selfupdate.New(selfupdate.Config{
			DataDir: dataDir,
		}),
		selfUpdateOn: selfUpdateOn,
		logger:       logger,
	}
}

// Check reports the current version alongside the latest GitHub release, the
// detected deployment mode and whether a one-click self-update is possible.
// Pass ?force=1 to bypass the ~1h cache.
func (h *UpdateHandler) Check(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("refresh") == "1"

	mode := selfupdate.DetectDeployment()
	canDeploy, reason := selfupdate.CanSelfUpdate(mode)

	out := map[string]any{
		"current":             version.Number,
		"deployment_mode":     string(mode),
		"self_update_enabled": h.selfUpdateOn,
	}

	rel, err := h.updater.CheckLatest(r.Context(), force)
	if err != nil {
		out["has_update"] = false
		out["can_self_update"] = false
		out["check_error"] = err.Error()
		out["reason"] = "无法获取最新版本：" + err.Error()
		WriteJSON(w, http.StatusOK, out)
		return
	}

	out["latest"] = rel.Tag
	out["changelog"] = rel.Changelog
	out["html_url"] = rel.HTMLURL
	out["published_at"] = rel.PublishedAt
	out["has_update"] = selfupdate.HasUpdate(version.Number, rel.Tag)

	// can_self_update is the capability (deployment supports it AND operator
	// enabled it); the frontend combines it with has_update to enable the
	// button. reason explains a disabled state.
	canSelf := canDeploy && h.selfUpdateOn
	if !h.selfUpdateOn {
		reason = "管理员已禁用 Web 端自更新（CFDM_SELF_UPDATE_ENABLED=false）"
	}
	out["can_self_update"] = canSelf
	out["reason"] = reason

	WriteJSON(w, http.StatusOK, out)
}

// Update launches the detached self-update and returns 202 immediately. The
// daemon is about to be restarted by the updater, so the client should poll
// /health and /version until the version changes. Pass ?force=1 to reinstall
// the current latest even when already up to date.
func (h *UpdateHandler) Update(w http.ResponseWriter, r *http.Request) {
	if !h.selfUpdateOn {
		WriteError(w, http.StatusForbidden, CodeForbidden,
			"Web 端自更新已禁用（CFDM_SELF_UPDATE_ENABLED=false）", nil)
		return
	}

	mode := selfupdate.DetectDeployment()
	if ok, reason := selfupdate.CanSelfUpdate(mode); !ok {
		WriteError(w, http.StatusBadRequest, CodeInvalidState, reason, nil)
		return
	}

	// Resolve the target so the updater installs a deterministic version and
	// we can echo it back.
	rel, err := h.updater.CheckLatest(r.Context(), false)
	if err != nil {
		WriteError(w, http.StatusBadGateway, CodeUpstreamFailure,
			"无法获取最新版本："+err.Error(), nil)
		return
	}

	force := r.URL.Query().Get("force") == "1"
	if !selfupdate.HasUpdate(version.Number, rel.Tag) && !force {
		WriteError(w, http.StatusConflict, CodeConflict,
			"已是最新版本 "+version.Number+"（如需强制重装请加 ?force=1）", nil)
		return
	}

	// Start the log fresh so the web UI streams only this run's steps.
	h.updater.ResetLog(version.Number, rel.Tag)

	if err := h.updater.StartUpdate(rel.Tag); err != nil {
		h.logger.Error("self-update spawn failed", "err", err)
		WriteError(w, http.StatusInternalServerError, CodeInternal,
			"启动更新失败："+err.Error(), nil)
		return
	}

	h.logger.Warn("self-update initiated; service will restart",
		"from", version.Number, "to", rel.Tag, "mode", string(mode))
	WriteJSON(w, http.StatusAccepted, map[string]any{
		"status":  "updating",
		"from":    version.Number,
		"to":      rel.Tag,
		"message": "更新已开始，服务即将重启，请稍候…",
	})
}

// Log returns the current self-update log (update.log) so the web UI can show
// the live progress steps while an update runs.
func (h *UpdateHandler) Log(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{"content": h.updater.ReadLog()})
}
