package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/mia-clark/cloudflared-manager/internal/manager"
	"github.com/mia-clark/cloudflared-manager/pkg/cfdconfig"
)

// 子进程模型下每个 cloudflared 实例写各自的 <id>.log，不再有合并日志/前缀。
// 这些测试验证：每个实例只读到自己文件里的行，以及 LogViewSince 水位逻辑。

// TestLogsQuery_ReadsOwnFile: GET /api/v1/configs/A/logs 只返回 A.log 的行。
func TestLogsQuery_ReadsOwnFile(t *testing.T) {
	tmp := t.TempDir()
	m := newTestManager(t, tmp)
	mustCreateInstance(t, m, "A")
	mustCreateInstance(t, m, "B")

	seedLog(t, m.LogPath("A"), []string{
		"2026-06-03 15:17:41.437 [I] try to connect",
		"2026-06-03 15:18:20.416 [E] login fail",
	})
	seedLog(t, m.LogPath("B"), []string{
		"2026-06-03 15:17:50.544 [D] heartbeat",
	})

	h := NewLogsHandler(m, filepath.Join(tmp, "logs"), testLogger(), []string{"*"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/configs/A/logs?lines=10", nil)
	req = withPathID(req, "A")
	rec := httptest.NewRecorder()
	h.Query(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if len(resp.Lines) != 2 {
		t.Fatalf("expected 2 lines for A, got %d: %v", len(resp.Lines), resp.Lines)
	}
}

// ---- test helpers ----

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestManager(t *testing.T, dataDir string) *manager.Manager {
	t.Helper()
	opts := manager.Options{
		ProfilesDir: filepath.Join(dataDir, "profiles"),
		LogsDir:     filepath.Join(dataDir, "logs"),
		StoresDir:   filepath.Join(dataDir, "stores"),
		MetaPath:    filepath.Join(dataDir, "meta.json"),
		Logger:      testLogger(),
	}
	for _, d := range []string{opts.ProfilesDir, opts.LogsDir, opts.StoresDir} {
		_ = os.MkdirAll(d, 0o755)
	}
	m, err := manager.New(opts)
	if err != nil {
		t.Fatalf("manager.New: %v", err)
	}
	return m
}

func mustCreateInstance(t *testing.T, m *manager.Manager, id string) {
	t.Helper()
	sc, err := cfdconfig.ParseYAML([]byte("edge:\n  protocol: auto\n"))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if err := m.Create(id, sc, manager.MgrMeta{Name: id}); err != nil {
		t.Fatalf("Create %s: %v", id, err)
	}
}

func seedLog(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(append(lines, ""), "\n")), 0o644); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
}

func withPathID(r *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// TestLogsTail_StreamsOwnFile: WS /logs/tail 实时推送本实例新增的行。
func TestLogsTail_StreamsOwnFile(t *testing.T) {
	tmp := t.TempDir()
	m := newTestManager(t, tmp)
	mustCreateInstance(t, m, "A")
	seedLog(t, m.LogPath("A"), []string{})

	h := NewLogsHandler(m, filepath.Join(tmp, "logs"), testLogger(), []string{"*"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = withPathID(r, "A")
		h.Tail(w, r)
	}))
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL.String(), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}

	// 给 logtail goroutine 一点时间订阅成功（Windows fsnotify 启动较慢）
	time.Sleep(500 * time.Millisecond)

	f, err := os.OpenFile(m.LogPath("A"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	for _, line := range []string{
		"2026-06-03 16:00:01.000 [I] login success\n",
		"2026-06-03 16:00:02.000 [D] heartbeat-A\n",
	} {
		_, _ = f.WriteString(line)
	}
	_ = f.Close()

	got := []string{}
	readDeadline := time.After(8 * time.Second)
	for len(got) < 2 {
		select {
		case <-readDeadline:
			t.Fatalf("timeout, got %v", got)
		default:
		}
		readCtx, c := context.WithTimeout(ctx, 3*time.Second)
		_, data, err := conn.Read(readCtx)
		c()
		if err != nil {
			t.Fatalf("ws read: %v", err)
		}
		var frame struct {
			Line string `json:"line"`
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatalf("decode frame: %v", err)
		}
		got = append(got, frame.Line)
	}

	conn.Close(websocket.StatusNormalClosure, "")
	time.Sleep(500 * time.Millisecond)
}

// TestLogsClear_SetsViewSince: DELETE /logs 仅更新 LogViewSince，不删文件。
func TestLogsClear_SetsViewSince(t *testing.T) {
	tmp := t.TempDir()
	m := newTestManager(t, tmp)
	mustCreateInstance(t, m, "A")
	logPath := m.LogPath("A")
	seedLog(t, logPath, []string{
		"2026-06-03 10:00:00.000 [I] old",
	})
	h := NewLogsHandler(m, filepath.Join(tmp, "logs"), testLogger(), []string{"*"})

	// 1. Clear A
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/configs/A/logs", nil)
	req = withPathID(req, "A")
	rec := httptest.NewRecorder()
	h.Clear(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	// 2. 文件仍存在
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file should still exist after Clear, got %v", err)
	}

	// 3. GET A 应返回空（被清空水位过滤）
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/configs/A/logs?lines=10", nil)
	getReq = withPathID(getReq, "A")
	getRec := httptest.NewRecorder()
	h.Query(getRec, getReq)
	var resp struct {
		Lines []string `json:"lines"`
	}
	_ = json.Unmarshal(getRec.Body.Bytes(), &resp)
	if len(resp.Lines) != 0 {
		t.Fatalf("expected empty lines after Clear, got %v", resp.Lines)
	}
}

// TestLogsClear_404OnUnknownID: DELETE 不存在的 instance 应返回 404。
func TestLogsClear_404OnUnknownID(t *testing.T) {
	tmp := t.TempDir()
	m := newTestManager(t, tmp)
	h := NewLogsHandler(m, filepath.Join(tmp, "logs"), testLogger(), []string{"*"})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/configs/nonexistent/logs", nil)
	req = withPathID(req, "nonexistent")
	rec := httptest.NewRecorder()
	h.Clear(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestLogsQuery_RespectsViewSince: 设置 LogViewSince 后只返回 >= since 的行。
func TestLogsQuery_RespectsViewSince(t *testing.T) {
	tmp := t.TempDir()
	m := newTestManager(t, tmp)
	mustCreateInstance(t, m, "A")
	seedLog(t, m.LogPath("A"), []string{
		"2026-06-03 10:00:00.000 [I] line-1-old",
		"2026-06-03 12:00:00.000 [I] line-2-old",
		"2026-06-03 14:00:00.000 [I] line-3-new",
	})
	h := NewLogsHandler(m, filepath.Join(tmp, "logs"), testLogger(), []string{"*"})

	cutoff, err := time.ParseInLocation("2006-01-02 15:04:05.000",
		"2026-06-03 13:00:00.000", time.Local)
	if err != nil {
		t.Fatalf("parse cutoff: %v", err)
	}
	if err := m.SetLogViewSince("A", cutoff.UnixMilli()); err != nil {
		t.Fatalf("SetLogViewSince: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/configs/A/logs?lines=10", nil)
	req = withPathID(req, "A")
	rec := httptest.NewRecorder()
	h.Query(rec, req)

	var resp struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Lines) != 1 {
		t.Fatalf("expected 1 line after view-since, got %d: %v", len(resp.Lines), resp.Lines)
	}
	if !strings.Contains(resp.Lines[0], "line-3-new") {
		t.Fatalf("expected line-3-new, got %q", resp.Lines[0])
	}
}
