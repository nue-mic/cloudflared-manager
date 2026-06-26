package api

import (
	"log/slog"
	"net/http"

	"github.com/nue-mic/cloudflared-manager/internal/manager"
)

// LifecycleHandler serves start/stop/reload endpoints.
type LifecycleHandler struct {
	m   *manager.Manager
	log *slog.Logger
}

// NewLifecycleHandler builds a LifecycleHandler.
func NewLifecycleHandler(m *manager.Manager, log *slog.Logger) *LifecycleHandler {
	return &LifecycleHandler{m: m, log: log}
}

// Start launches an instance.
func (h *LifecycleHandler) Start(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := h.m.Start(id); writeManagerError(w, err) {
		return
	}
	h.writeStatus(w, id)
}

// Stop stops an instance.
func (h *LifecycleHandler) Stop(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := h.m.Stop(id); writeManagerError(w, err) {
		return
	}
	h.writeStatus(w, id)
}

// Reload restarts the instance (cloudflared has no in-place reload; the
// manager performs a stop + start).
func (h *LifecycleHandler) Reload(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := h.m.Reload(id); err != nil {
		// reload-specific failures are upstream issues, not 404s
		if writeManagerError(w, err) {
			return
		}
		WriteError(w, http.StatusBadRequest, CodeInvalidState, err.Error(), nil)
		return
	}
	h.writeStatus(w, id)
}

func (h *LifecycleHandler) writeStatus(w http.ResponseWriter, id string) {
	snap, _, _, err := h.m.Get(id)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return
	}
	WriteJSON(w, http.StatusOK, snap)
}

// statusOf is the helper used by other packages.
func statusOf(m *manager.Manager, id string) (manager.Snapshot, error) {
	snap, _, _, err := m.Get(id)
	return snap, err
}
