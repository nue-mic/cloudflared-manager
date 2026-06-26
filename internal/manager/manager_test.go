package manager

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/nue-mic/cloudflared-manager/internal/eventbus"
	"github.com/nue-mic/cloudflared-manager/pkg/cfdconfig"
)

func newMgr(t *testing.T) (*Manager, string) {
	t.Helper()
	tmp := t.TempDir()
	opts := Options{
		ProfilesDir: filepath.Join(tmp, "profiles"),
		LogsDir:     filepath.Join(tmp, "logs"),
		StoresDir:   filepath.Join(tmp, "stores"),
		MetaPath:    filepath.Join(tmp, "meta.json"),
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Bus:         eventbus.New(16),
	}
	for _, d := range []string{opts.ProfilesDir, opts.LogsDir, opts.StoresDir} {
		_ = os.MkdirAll(d, 0o755)
	}
	m, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m, tmp
}

// tunnelCfg returns a minimal TunnelConfigV1 suitable for tests that do
// not actually spawn a cloudflared process. The token is intentionally
// empty so start() will fail fast (token required) without needing a real
// binary in PATH.
func tunnelCfg() *cfdconfig.TunnelConfigV1 {
	return &cfdconfig.TunnelConfigV1{
		Edge: cfdconfig.EdgeConfig{Protocol: "auto"},
	}
}

// TestCreateGetRoundTrip: Create 写盘后，Get 能读回 Edge.Protocol 与管理元数据。
func TestCreateGetRoundTrip(t *testing.T) {
	m, _ := newMgr(t)
	cfg := tunnelCfg()
	cfg.Edge.Protocol = "http2"
	if err := m.Create("main", cfg, MgrMeta{Name: "主连接器", ManualStart: true}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	snap, sc, mm, err := m.Get("main")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sc.Edge.Protocol != "http2" {
		t.Fatalf("Edge.Protocol = %q, want http2", sc.Edge.Protocol)
	}
	if mm.Name != "主连接器" || !mm.ManualStart {
		t.Fatalf("manager meta lost: %+v", mm)
	}
	if snap.Name != "主连接器" {
		t.Fatalf("snapshot name = %q, want 主连接器", snap.Name)
	}
	if snap.State != "stopped" {
		t.Fatalf("fresh instance state = %q, want stopped", snap.State)
	}
}

// TestCreateDuplicateRejected: 同 id 重复创建返回 ErrExists。
func TestCreateDuplicateRejected(t *testing.T) {
	m, _ := newMgr(t)
	if err := m.Create("dup", tunnelCfg(), MgrMeta{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Create("dup", tunnelCfg(), MgrMeta{}); err != ErrExists {
		t.Fatalf("expected ErrExists, got %v", err)
	}
}

// TestDeleteRemovesFileAndMeta: Delete 后文件消失、Get 404、meta 不再含该 id。
func TestDeleteRemovesFileAndMeta(t *testing.T) {
	m, tmp := newMgr(t)
	if err := m.Create("gone", tunnelCfg(), MgrMeta{Name: "n"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	path := filepath.Join(tmp, "profiles", "gone.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist: %v", err)
	}
	if err := m.Delete("gone"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should be gone, stat err=%v", err)
	}
	if _, _, _, err := m.Get("gone"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	if m.meta.name("gone") != "" {
		t.Fatalf("meta name not dropped")
	}
}

// TestListReflectsCreateAndReorder: List 返回创建的实例并尊重 Reorder 顺序。
func TestListReflectsCreateAndReorder(t *testing.T) {
	m, _ := newMgr(t)
	for _, id := range []string{"a", "b", "c"} {
		if err := m.Create(id, tunnelCfg(), MgrMeta{Name: id}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}
	if err := m.Reorder([]string{"c", "a", "b"}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	list := m.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 items, got %d", len(list))
	}
	if list[0].ID != "c" || list[1].ID != "a" || list[2].ID != "b" {
		t.Fatalf("reorder not honored: %s,%s,%s", list[0].ID, list[1].ID, list[2].ID)
	}
}

// TestWriteRawRejectsGarbage: WriteRaw 对非法 YAML 返回 parse 错误。
func TestWriteRawRejectsGarbage(t *testing.T) {
	m, _ := newMgr(t)
	if err := m.Create("x", tunnelCfg(), MgrMeta{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Inject a tab character which is illegal in YAML block scalars in this context.
	if err := m.WriteRaw("x", []byte("edge:\n\tprotocol: bad-tab\n")); err == nil {
		t.Fatalf("expected parse error for garbage YAML")
	}
}

// TestMetricsPort: 同一 id 每次 allocMetricsPort 返回相同端口，且在合法范围内。
func TestMetricsPort(t *testing.T) {
	port := allocMetricsPort("my-tunnel")
	if port < 20241 || port > 20998 {
		t.Fatalf("port %d outside [20241, 20998]", port)
	}
	if allocMetricsPort("my-tunnel") != port {
		t.Fatal("port not stable for same id")
	}
	if allocMetricsPort("other-tunnel") == port {
		// collision possible but extremely unlikely for two short strings
		t.Log("collision (acceptable, probabilistic)")
	}
}
