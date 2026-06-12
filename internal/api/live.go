package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/mia-clark/cloudflared-manager/internal/manager"
	"github.com/mia-clark/cloudflared-manager/internal/metrics"
)

// LiveHandler serves GET /api/v1/configs/{id}/live — an on-demand scrape of a
// running instance's cloudflared /metrics endpoint, parsed into a LiveStatus
// but NOT persisted to the time-series store. It powers the detail panel's
// Overview/Connections tabs (current HA connections, per-conn colo/RTT/loss,
// protocol, cloudflared version) without touching the SQLite schema.
type LiveHandler struct {
	m   *manager.Manager
	log *slog.Logger
}

// NewLiveHandler builds a LiveHandler bound to the manager.
func NewLiveHandler(m *manager.Manager, log *slog.Logger) *LiveHandler {
	return &LiveHandler{m: m, log: log}
}

// Get returns the live status. When the instance is not running (no metrics
// address) or the scrape fails, it responds 200 with {"running":false} so the
// UI can render a calm "not running" state instead of an error.
func (h *LiveHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if !h.m.Exists(id) {
		WriteError(w, http.StatusNotFound, CodeConfigNotFound, "config not found", nil)
		return
	}
	addr, ok := h.m.MetricsAddr(id)
	if !ok {
		WriteJSON(w, http.StatusOK, metrics.LiveStatus{Running: false})
		return
	}
	samples, err := metrics.Scrape(addr)
	if err != nil {
		h.log.Debug("live scrape failed", slog.String("id", id), slog.Any("err", err))
		WriteJSON(w, http.StatusOK, metrics.LiveStatus{Running: false, Error: err.Error()})
		return
	}
	live := metrics.FoldLive(samples)
	live.Running = true
	live.ScrapedAt = time.Now().Unix()
	WriteJSON(w, http.StatusOK, live)
}
