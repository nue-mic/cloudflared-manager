package api

import (
	"net/http"

	"github.com/nue-mic/cloudflared-manager/internal/manager"
)

// StatusHandler serves /api/v1/configs/{id}/status.
type StatusHandler struct {
	m *manager.Manager
}

// NewStatusHandler builds a StatusHandler.
func NewStatusHandler(m *manager.Manager) *StatusHandler {
	return &StatusHandler{m: m}
}

// Get returns the runtime snapshot, including per-proxy status.
func (h *StatusHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	snap, err := statusOf(h.m, id)
	if writeManagerError(w, err) {
		return
	}
	WriteJSON(w, http.StatusOK, snap)
}

// Ensure manager import is referenced even if statusOf inlines.
var _ = (*manager.Manager)(nil)
