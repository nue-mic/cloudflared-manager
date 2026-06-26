package manager

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/nue-mic/cloudflared-manager/internal/eventbus"
	"github.com/nue-mic/cloudflared-manager/pkg/cfdconfig"
)

func TestAutoUpdateRoundTrip(t *testing.T) {
	m, tmp := newMgr(t)
	if _, ok := m.AutoUpdate(); ok {
		t.Fatal("AutoUpdate should be unset on a fresh meta.json")
	}
	in := AutoUpdateMeta{
		Enabled: true, Mode: "full", IntervalHours: 12,
		IncludePrerelease: true, AutoRollback: true,
		KeepVersions: 5, HealthGraceSeconds: 10,
	}
	if err := m.SetAutoUpdate(in); err != nil {
		t.Fatalf("SetAutoUpdate: %v", err)
	}
	got, ok := m.AutoUpdate()
	if !ok {
		t.Fatal("AutoUpdate should be set after SetAutoUpdate")
	}
	if got != in {
		t.Errorf("AutoUpdate=%+v want %+v", got, in)
	}

	// Persistence: a fresh manager on the same meta.json must read it back.
	opts := Options{
		ProfilesDir: filepath.Join(tmp, "profiles"),
		LogsDir:     filepath.Join(tmp, "logs"),
		StoresDir:   filepath.Join(tmp, "stores"),
		MetaPath:    filepath.Join(tmp, "meta.json"),
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Bus:         eventbus.New(16),
	}
	m2, err := New(opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got2, ok := m2.AutoUpdate()
	if !ok || got2 != in {
		t.Errorf("persisted AutoUpdate=%+v ok=%v want %+v", got2, ok, in)
	}
}

func mustCreate(t *testing.T, m *Manager, id string, c *cfdconfig.TunnelConfigV1) {
	t.Helper()
	if err := m.Create(id, c, MgrMeta{}); err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}
}

func TestPinnedBinaryVersions(t *testing.T) {
	m, _ := newMgr(t)

	c1 := tunnelCfg()
	c1.BinaryVersion = "2026.5.2"
	c2 := tunnelCfg()
	c2.BinaryVersion = "current" // follower, not a pin
	c3 := tunnelCfg() // empty → follower
	c4 := tunnelCfg()
	c4.BinaryVersion = "2026.4.4"

	mustCreate(t, m, "a", c1)
	mustCreate(t, m, "b", c2)
	mustCreate(t, m, "c", c3)
	mustCreate(t, m, "d", c4)

	pinned := m.PinnedBinaryVersions()
	if _, ok := pinned["2026.5.2"]; !ok {
		t.Error("2026.5.2 should be pinned")
	}
	if _, ok := pinned["2026.4.4"]; !ok {
		t.Error("2026.4.4 should be pinned")
	}
	if _, ok := pinned["current"]; ok {
		t.Error(`"current" must not count as a pin`)
	}
	if len(pinned) != 2 {
		t.Errorf("pinned=%v want exactly 2", pinned)
	}
}

func TestActiveFollowerRunningIDs_EmptyWhenStopped(t *testing.T) {
	m, _ := newMgr(t)
	mustCreate(t, m, "a", tunnelCfg())
	if ids := m.ActiveFollowerRunningIDs(); len(ids) != 0 {
		t.Errorf("stopped instances must not be active-followers, got %v", ids)
	}
}
