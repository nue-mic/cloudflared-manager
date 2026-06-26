package manager

import (
	"os"
	"strings"
	"time"

	"github.com/nue-mic/cloudflared-manager/pkg/cfdconfig"
	"github.com/nue-mic/cloudflared-manager/pkg/cfdstate"
)

// AutoUpdate returns the persisted cloudflared binary auto-update config and
// whether it has ever been set. When ok is false the caller (cfdupdate) seeds
// it from env defaults and calls SetAutoUpdate to persist.
func (m *Manager) AutoUpdate() (AutoUpdateMeta, bool) {
	return m.meta.autoUpdate()
}

// SetAutoUpdate persists the auto-update config wholesale.
func (m *Manager) SetAutoUpdate(a AutoUpdateMeta) error {
	return m.meta.setAutoUpdate(a)
}

// ActiveFollowerRunningIDs returns the ids of running instances that follow
// the store's active version — i.e. whose config binaryVersion is empty or
// "current". Instances pinned to an explicit version are deliberately
// EXCLUDED: a user who pinned a version did so on purpose and must not be
// moved by an auto-update. Order follows meta.json (deterministic restart
// order). Instances whose config cannot be read/parsed are skipped.
func (m *Manager) ActiveFollowerRunningIDs() []string {
	out := make([]string, 0)
	for _, id := range m.orderedIDs() {
		inst := m.get(id)
		if inst == nil || inst.State() != cfdstate.ConfigStateStarted {
			continue
		}
		b, err := os.ReadFile(inst.Path())
		if err != nil {
			continue
		}
		cfg, err := cfdconfig.ParseYAML(b)
		if err != nil {
			continue
		}
		v := strings.TrimSpace(cfg.BinaryVersion)
		if v == "" || v == "current" {
			out = append(out, id)
		}
	}
	return out
}

// PinnedBinaryVersions returns the set of cloudflared versions explicitly
// pinned by some instance config (binaryVersion is a concrete tag, not empty
// or "current"). Auto-update retention must never prune a version in this set,
// or the pinning instance could no longer start. Scans every registered
// instance, running or not; unreadable/unparseable configs are skipped.
func (m *Manager) PinnedBinaryVersions() map[string]struct{} {
	out := make(map[string]struct{})
	m.mu.RLock()
	insts := make([]*instance, 0, len(m.instances))
	for _, inst := range m.instances {
		insts = append(insts, inst)
	}
	m.mu.RUnlock()
	for _, inst := range insts {
		b, err := os.ReadFile(inst.Path())
		if err != nil {
			continue
		}
		cfg, err := cfdconfig.ParseYAML(b)
		if err != nil {
			continue
		}
		v := strings.TrimSpace(cfg.BinaryVersion)
		if v != "" && v != "current" {
			out[v] = struct{}{}
		}
	}
	return out
}

// WaitHealthy reports whether instance id stays healthy for the whole grace
// window after a (re)start: continuously in the started state with no recorded
// error. It returns false the moment the instance leaves started or records an
// error (e.g. a bad binary that spawns then exits almost immediately), so the
// caller can roll back fast. A non-existent instance is unhealthy.
//
// grace <= 0 collapses to a single immediate check.
func (m *Manager) WaitHealthy(id string, grace time.Duration) bool {
	const step = 250 * time.Millisecond
	deadline := time.Now().Add(grace)
	for {
		inst := m.get(id)
		if inst == nil {
			return false
		}
		snap := inst.Snapshot()
		if snap.State != "started" || snap.LastError != "" {
			return false
		}
		if !time.Now().Before(deadline) {
			return true
		}
		sleep := step
		if rem := time.Until(deadline); rem < sleep {
			sleep = rem
		}
		time.Sleep(sleep)
	}
}
