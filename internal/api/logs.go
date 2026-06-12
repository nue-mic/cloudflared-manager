package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/mia-clark/cloudflared-manager/internal/api/middleware"
	"github.com/mia-clark/cloudflared-manager/internal/logtail"
	"github.com/mia-clark/cloudflared-manager/internal/manager"
	"github.com/mia-clark/cloudflared-manager/pkg/util"
)

// LogsHandler serves /api/v1/configs/{id}/logs*.
type LogsHandler struct {
	m       *manager.Manager
	logsDir string
	log     *slog.Logger
	origins []string
}

// NewLogsHandler builds a LogsHandler.
func NewLogsHandler(m *manager.Manager, logsDir string, log *slog.Logger, origins []string) *LogsHandler {
	return &LogsHandler{m: m, logsDir: logsDir, log: log, origins: origins}
}

// logInstancePath 返回单个实例的独立日志文件绝对路径。子进程模型下，每个
// frps worker 的 stdout/stderr 写入各自的 <id>.log，无需再按前缀过滤。
func (h *LogsHandler) logInstancePath(id string) string {
	return h.m.LogPath(id)
}

// Query returns the last `lines` lines (default 200) from this instance's
// log file that are not older than the instance's LogViewSince watermark.
func (h *LogsHandler) Query(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if !h.m.Exists(id) {
		WriteError(w, http.StatusNotFound, CodeConfigNotFound, "config not found", nil)
		return
	}
	lines := atoiDefault(r.URL.Query().Get("lines"), 200)
	since := h.m.LogViewSince(id)

	got, err := util.ReadFileLinesFiltered(h.logInstancePath(id), lines, func(line string) bool {
		if since == 0 {
			return true
		}
		ts, ok := parseLogLineTimestamp(line)
		if !ok {
			return true // 解析失败的行保留，避免误删
		}
		return ts >= since
	})
	if err != nil {
		WriteJSON(w, http.StatusOK, map[string]any{"lines": []string{}, "next_offset": int64(0)})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"lines":       trimLines(got),
		"next_offset": int64(0), // 合并日志模式不再支持 offset 翻页；前端只用 lines
	})
}

// Files 列出本实例日志文件 <id>.log 的所有轮转副本。子进程模型下，每个 frps
// worker 写各自的日志文件，本接口只列当前实例的归档。
func (h *LogsHandler) Files(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if !h.m.Exists(id) {
		WriteError(w, http.StatusNotFound, CodeConfigNotFound, "config not found", nil)
		return
	}
	files, dates, err := util.FindLogFiles(h.logInstancePath(id))
	if err != nil {
		WriteJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	items := make([]map[string]any, 0, len(files))
	for i, f := range files {
		entry := map[string]any{"path": f}
		if i < len(dates) && !dates[i].IsZero() {
			entry["rotated_at"] = dates[i]
		}
		items = append(items, entry)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// Clear sets a "view since" timestamp for this instance instead of deleting
// the log file. Subsequent GET /logs and WS /logs/tail will skip lines older
// than this timestamp. The physical <id>.log is preserved so operators can
// still grep historical data on disk.
func (h *LogsHandler) Clear(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if !h.m.Exists(id) {
		WriteError(w, http.StatusNotFound, CodeConfigNotFound, "config not found", nil)
		return
	}
	if err := h.m.SetLogViewSince(id, time.Now().UnixMilli()); err != nil {
		WriteError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Tail upgrades to WebSocket and streams new lines belonging to the given
// instance as they arrive. 订阅本实例的 <id>.log 文件，新增行实时推送。
// 当 LogViewSince[id] > 0 时，时间戳早于该值的行被丢弃。
func (h *LogsHandler) Tail(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if !h.m.Exists(id) {
		WriteError(w, http.StatusNotFound, CodeConfigNotFound, "config not found", nil)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: middleware.IsWildcard(h.origins),
		OriginPatterns:     h.origins,
	})
	if err != nil {
		h.log.Warn("ws accept failed", slog.Any("err", err))
		return
	}
	defer conn.Close(websocket.StatusInternalError, "internal error")

	// CloseRead 在后台持续读取控制帧（ping/pong/close），返回一个在连接关闭时
	// 自动取消的 ctx。这样即便底层 TCP 已被 hijack（HTTP server 不再管理），
	// 客户端主动关闭连接也能让下方 select 及时退出。
	ctx := conn.CloseRead(r.Context())

	t := logtail.New(h.logInstancePath(id))
	ch := t.Subscribe()
	defer t.Stop()

	since := h.m.LogViewSince(id)

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			if since > 0 {
				if ts, ok := parseLogLineTimestamp(line); ok && ts < since {
					continue
				}
			}
			payload, _ := json.Marshal(map[string]string{"line": line})
			wctx, c := context.WithTimeout(ctx, 5*time.Second)
			if err := conn.Write(wctx, websocket.MessageText, payload); err != nil {
				c()
				return
			}
			c()
		case <-ping.C:
			pctx, c := context.WithTimeout(ctx, 5*time.Second)
			if err := conn.Ping(pctx); err != nil {
				c()
				return
			}
			c()
		}
	}
}

// Stream upgrades to WebSocket and streams STRUCTURED log Entries for the
// instance: cloudflared's --output=json lines parsed into {seq,time,level,...}.
// Unlike Tail (which forwards raw file lines), Stream subscribes to the
// in-memory ProcessTailer so the UI gets level/conn_index/fields for colouring
// and filtering.
//
// GET /api/v1/configs/{id}/logs/stream?token=&level=&keyword=&conn_index=&backlog=
//
// Frame format (uniform): every text frame is {"entries":[Entry,...]}. On
// connect a single backlog frame is sent (oldest→newest, honouring the
// LogViewSince watermark + query filter); thereafter each new line arrives as
// a one-element frame. The frontend dedups by Entry.seq because Stream
// subscribes before snapshotting (so no line is lost in the gap, at the cost of
// a possible duplicate at the boundary).
func (h *LogsHandler) Stream(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	tailer, ok := h.m.Tailer(id)
	if !ok {
		WriteError(w, http.StatusNotFound, CodeConfigNotFound, "config not found", nil)
		return
	}

	filter := parseLogFilter(r)
	if since := h.m.LogViewSince(id); since > 0 {
		filter.Since = time.UnixMilli(since)
	}
	backlog := atoiDefault(r.URL.Query().Get("backlog"), 500)

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: middleware.IsWildcard(h.origins),
		OriginPatterns:     h.origins,
	})
	if err != nil {
		h.log.Warn("ws accept failed", slog.Any("err", err))
		return
	}
	defer conn.Close(websocket.StatusInternalError, "internal error")
	ctx := conn.CloseRead(r.Context())

	// Subscribe BEFORE the snapshot so lines arriving in between are not lost.
	ch, cancel := tailer.Subscribe(filter)
	defer cancel()

	if backlogEntries := tailer.Snapshot(filter, backlog); len(backlogEntries) > 0 {
		if !writeEntries(ctx, conn, backlogEntries) {
			return
		}
	}

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if !writeEntries(ctx, conn, []logtail.Entry{e}) {
				return
			}
		case <-ping.C:
			pctx, c := context.WithTimeout(ctx, 5*time.Second)
			if err := conn.Ping(pctx); err != nil {
				c()
				return
			}
			c()
		}
	}
}

// parseLogFilter builds a logtail.Filter from the WS query parameters.
func parseLogFilter(r *http.Request) logtail.Filter {
	f := logtail.Filter{
		MinLevel: strings.TrimSpace(r.URL.Query().Get("level")),
		Keyword:  r.URL.Query().Get("keyword"),
	}
	if ci := strings.TrimSpace(r.URL.Query().Get("conn_index")); ci != "" {
		if n, err := strconv.Atoi(ci); err == nil {
			f.ConnIndex = &n
		}
	}
	return f
}

// marshalEntriesFrame encodes a batch of Entries into the wire frame the
// frontend consumes: {"entries":[Entry,...]} with snake_case Entry fields.
func marshalEntriesFrame(entries []logtail.Entry) ([]byte, error) {
	return json.Marshal(map[string]any{"entries": entries})
}

// writeEntries sends one {"entries":[...]} frame. Returns false on any write
// error so the caller can tear down the connection.
func writeEntries(ctx context.Context, conn *websocket.Conn, entries []logtail.Entry) bool {
	payload, err := marshalEntriesFrame(entries)
	if err != nil {
		return false
	}
	wctx, c := context.WithTimeout(ctx, 5*time.Second)
	defer c()
	return conn.Write(wctx, websocket.MessageText, payload) == nil
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func trimLines(in []string) []string {
	out := make([]string, 0, len(in))
	for _, l := range in {
		out = append(out, strings.TrimRight(l, "\r\n"))
	}
	return out
}

// parseLogLineTimestamp 解析一行日志的时间戳（毫秒精度），用于「清空日志」
// 水位过滤。支持两种格式：
//   - cloudflared --output=json：{"level":...,"time":"2026-06-07T18:42:09.9Z",...}
//   - 旧 frp 文本行："2026-06-03 15:18:20.546 [D] ..."（向后兼容）
// 解析失败时 ok=false，调用方默认保留这一行。
func parseLogLineTimestamp(line string) (unixMilli int64, ok bool) {
	line = strings.TrimSpace(line)
	// cloudflared 结构化 JSON 行：提取 "time":"<RFC3339>"。
	if strings.HasPrefix(line, "{") {
		const key = `"time":"`
		if i := strings.Index(line, key); i >= 0 {
			rest := line[i+len(key):]
			if j := strings.IndexByte(rest, '"'); j > 0 {
				if t, err := time.Parse(time.RFC3339Nano, rest[:j]); err == nil {
					return t.UnixMilli(), true
				}
			}
		}
		return 0, false
	}
	// 旧 frp 文本布局。
	const layout = "2006-01-02 15:04:05.000"
	if len(line) < len(layout) {
		return 0, false
	}
	t, err := time.ParseInLocation(layout, line[:len(layout)], time.Local)
	if err != nil {
		return 0, false
	}
	return t.UnixMilli(), true
}
