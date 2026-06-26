package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/nue-mic/cloudflared-manager/internal/api/middleware"
	"github.com/nue-mic/cloudflared-manager/internal/eventbus"
	"github.com/nue-mic/cloudflared-manager/internal/manager"
)

// EventsHandler upgrades HTTP requests to WebSocket and streams events
// from the bus.
type EventsHandler struct {
	m       *manager.Manager
	log     *slog.Logger
	origins []string
}

// NewEventsHandler builds an EventsHandler.
func NewEventsHandler(m *manager.Manager, log *slog.Logger, origins []string) *EventsHandler {
	return &EventsHandler{m: m, log: log, origins: origins}
}

// Subscribe handles GET /api/v1/events upgrades.
func (h *EventsHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	acceptOpts := &websocket.AcceptOptions{
		InsecureSkipVerify: middleware.IsWildcard(h.origins),
		OriginPatterns:     h.origins,
	}
	conn, err := websocket.Accept(w, r, acceptOpts)
	if err != nil {
		h.log.Warn("ws accept failed", slog.Any("err", err))
		return
	}
	defer conn.Close(websocket.StatusInternalError, "internal error")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Optional initial filter via query params.
	filter := parseFilter(r)
	sub := h.m.Bus().Subscribe(filter, 128)
	defer sub.Unsubscribe()

	// Replay recent events if client asked for them.
	if since := parseSince(r); since > 0 {
		for _, e := range h.m.Bus().Since(since) {
			if !writeEvent(ctx, conn, e) {
				return
			}
		}
	}

	go readControl(ctx, conn, sub)

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C():
			if !ok {
				return
			}
			if !writeEvent(ctx, conn, e) {
				return
			}
		case <-ping.C:
			pingCtx, c := context.WithTimeout(ctx, 5*time.Second)
			if err := conn.Ping(pingCtx); err != nil {
				c()
				return
			}
			c()
		}
	}
}

func writeEvent(ctx context.Context, conn *websocket.Conn, e eventbus.Event) bool {
	b, err := json.Marshal(e)
	if err != nil {
		return true
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, b); err != nil {
		return false
	}
	return true
}

// clientCmd is the JSON shape accepted from connected clients to update
// the subscription filter on the fly.
type clientCmd struct {
	Action    string              `json:"action"`
	Types     []eventbus.EventType `json:"types,omitempty"`
	ConfigIDs []string            `json:"config_ids,omitempty"`
}

func readControl(ctx context.Context, conn *websocket.Conn, sub *eventbus.Subscription) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var cmd clientCmd
		if err := json.Unmarshal(data, &cmd); err != nil {
			continue
		}
		switch cmd.Action {
		case "subscribe", "filter":
			if len(cmd.Types) == 0 && len(cmd.ConfigIDs) == 0 {
				sub.SetFilter(nil)
				continue
			}
			sub.SetFilter(&eventbus.Filter{Types: cmd.Types, ConfigIDs: cmd.ConfigIDs})
		case "unfilter":
			sub.SetFilter(nil)
		}
	}
}

func parseFilter(r *http.Request) *eventbus.Filter {
	q := r.URL.Query()
	types := splitCSV(q.Get("types"))
	cids := splitCSV(q.Get("config_ids"))
	if len(types) == 0 && len(cids) == 0 {
		return nil
	}
	tt := make([]eventbus.EventType, 0, len(types))
	for _, t := range types {
		tt = append(tt, eventbus.EventType(t))
	}
	return &eventbus.Filter{Types: tt, ConfigIDs: cids}
}

func parseSince(r *http.Request) uint64 {
	v := r.URL.Query().Get("since")
	if v == "" {
		return 0
	}
	var u uint64
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0
		}
		u = u*10 + uint64(c-'0')
	}
	return u
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	cur := ""
	for _, ch := range s {
		if ch == ',' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(ch)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
