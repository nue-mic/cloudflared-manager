package cfdupdate

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/nue-mic/cloudflared-manager/internal/cfdbin"
)

// ── fakes ───────────────────────────────────────────────────────────────────

type fakeRelease struct {
	tag string
	err error
}

func (f *fakeRelease) ResolveLatest(_ context.Context, _ bool) (string, error) {
	return f.tag, f.err
}

type fakeStore struct {
	mu           sync.Mutex
	active       string
	versions     map[string]bool
	installCalls []string
	activateLog  []string
	deleteLog    []string
	installErr   error
}

func newFakeStore(active string, versions ...string) *fakeStore {
	s := &fakeStore{active: active, versions: map[string]bool{}}
	for _, v := range versions {
		s.versions[v] = true
	}
	if active != "" {
		s.versions[active] = true
	}
	return s
}

func (s *fakeStore) ActiveVersion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

func (s *fakeStore) Install(_ context.Context, version string) (cfdbin.VersionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.installErr != nil {
		return cfdbin.VersionMeta{}, s.installErr
	}
	s.installCalls = append(s.installCalls, version)
	s.versions[version] = true
	return cfdbin.VersionMeta{Version: version, Verified: true}, nil
}

func (s *fakeStore) Activate(version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.versions[version] {
		return cfdbin.ErrNotInstalled
	}
	s.active = version
	s.activateLog = append(s.activateLog, version)
	return nil
}

func (s *fakeStore) List() ([]cfdbin.InstalledVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]cfdbin.InstalledVersion, 0, len(s.versions))
	for v := range s.versions {
		out = append(out, cfdbin.InstalledVersion{Version: v, IsActive: v == s.active})
	}
	// store.List() contract: newest-first (version desc).
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

func (s *fakeStore) Delete(version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version == s.active {
		return errors.New("cannot delete active")
	}
	delete(s.versions, version)
	s.deleteLog = append(s.deleteLog, version)
	return nil
}

type fakeCtl struct {
	mu        sync.Mutex
	followers []string
	reloadErr map[string]error
	// badOn maps an instance id to the version on which it stays unhealthy
	// (modelling "a bad binary that this instance can't run"). On any other
	// active version it is healthy — so a rollback to a good version recovers
	// it. Requires store to read the current active version.
	store   *fakeStore
	badOn   map[string]string
	pinned  map[string]struct{}
	reloads []string
}

func (c *fakeCtl) ActiveFollowerRunningIDs() []string { return c.followers }

func (c *fakeCtl) Reload(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reloads = append(c.reloads, id)
	if c.reloadErr != nil {
		return c.reloadErr[id]
	}
	return nil
}

func (c *fakeCtl) WaitHealthy(id string, _ time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.badOn == nil || c.store == nil {
		return true
	}
	bad, ok := c.badOn[id]
	if !ok {
		return true
	}
	return c.store.ActiveVersion() != bad // unhealthy only while the bad version is active
}

func (c *fakeCtl) PinnedBinaryVersions() map[string]struct{} {
	if c.pinned == nil {
		return map[string]struct{}{}
	}
	return c.pinned
}

func (c *fakeCtl) reloadCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reloads)
}

// newTestUpdater builds an Updater with the given settings and fakes, using an
// in-memory settings store so Load/Save round-trip.
func newTestUpdater(t *testing.T, s Settings, store BinaryStore, rel ReleaseResolver, ctl Restarter) *Updater {
	t.Helper()
	var saved Settings
	return New(Config{
		Store:        store,
		Release:      rel,
		Controller:   ctl,
		Logger:       nil,
		Load:         func() (Settings, bool) { return s, true },
		Save:         func(ns Settings) error { saved = ns; _ = saved; return nil },
		InitialDelay: time.Millisecond,
	})
}

// ── tests ───────────────────────────────────────────────────────────────────

func TestCheckAndApply_UpToDate(t *testing.T) {
	store := newFakeStore("2026.5.2")
	rel := &fakeRelease{tag: "2026.5.2"}
	ctl := &fakeCtl{followers: []string{"a"}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeFull, IntervalHours: 24, HealthGraceSeconds: 1}, store, rel, ctl)

	if err := u.CheckAndApply(context.Background(), RunOpts{}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(store.installCalls) != 0 {
		t.Errorf("should not install when up to date, got %v", store.installCalls)
	}
	if ctl.reloadCount() != 0 {
		t.Errorf("should not restart when up to date")
	}
	if got := u.Status().LastResult; got != "up_to_date" {
		t.Errorf("last_result=%q want up_to_date", got)
	}
}

func TestCheckAndApply_FullUpdateSuccess(t *testing.T) {
	store := newFakeStore("2026.5.1")
	rel := &fakeRelease{tag: "2026.5.2"}
	ctl := &fakeCtl{followers: []string{"a", "b"}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeFull, IntervalHours: 24, AutoRollback: true, HealthGraceSeconds: 1}, store, rel, ctl)

	if err := u.CheckAndApply(context.Background(), RunOpts{}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if store.ActiveVersion() != "2026.5.2" {
		t.Errorf("active=%q want 2026.5.2", store.ActiveVersion())
	}
	if len(store.installCalls) != 1 || store.installCalls[0] != "2026.5.2" {
		t.Errorf("installCalls=%v", store.installCalls)
	}
	if ctl.reloadCount() != 2 {
		t.Errorf("want 2 reloads, got %d (%v)", ctl.reloadCount(), ctl.reloads)
	}
	if got := u.Status().LastResult; got != "updated" {
		t.Errorf("last_result=%q want updated", got)
	}
}

func TestCheckAndApply_RollbackOnUnhealthy(t *testing.T) {
	store := newFakeStore("2026.5.1")
	rel := &fakeRelease{tag: "2026.5.2"}
	// "b" is unhealthy on the new binary (2026.5.2) but recovers on rollback.
	ctl := &fakeCtl{followers: []string{"a", "b", "c"}, store: store, badOn: map[string]string{"b": "2026.5.2"}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeFull, IntervalHours: 24, AutoRollback: true, HealthGraceSeconds: 1}, store, rel, ctl)

	err := u.CheckAndApply(context.Background(), RunOpts{})
	if err == nil {
		t.Fatalf("expected error on rollback")
	}
	// active must be restored to the previous version.
	if store.ActiveVersion() != "2026.5.1" {
		t.Errorf("active=%q want rollback to 2026.5.1", store.ActiveVersion())
	}
	// activate log: 2026.5.2 (apply) then 2026.5.1 (rollback).
	if len(store.activateLog) != 2 || store.activateLog[0] != "2026.5.2" || store.activateLog[1] != "2026.5.1" {
		t.Errorf("activateLog=%v want [5.2 5.1]", store.activateLog)
	}
	// "c" was never restarted (break before it); "a" restarted then reverted,
	// "b" attempted then reverted. reloads: a, b (forward) + a, b (revert) = 4.
	if ctl.reloadCount() != 4 {
		t.Errorf("reloads=%v want 4 (a,b fwd + a,b revert)", ctl.reloads)
	}
	if got := u.Status().LastResult; got != "rolled_back" {
		t.Errorf("last_result=%q want rolled_back", got)
	}
}

func TestCheckAndApply_RollbackDegraded(t *testing.T) {
	store := newFakeStore("2026.5.1")
	rel := &fakeRelease{tag: "2026.5.2"}
	// "b" reload always errors → it cannot come up on the new OR the old
	// binary, so the rollback completes but b stays down → degraded result
	// (must NOT report a clean rolled_back).
	ctl := &fakeCtl{followers: []string{"a", "b", "c"}, store: store,
		reloadErr: map[string]error{"b": errors.New("spawn failed")}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeFull, IntervalHours: 24, AutoRollback: true, HealthGraceSeconds: 1}, store, rel, ctl)

	err := u.CheckAndApply(context.Background(), RunOpts{})
	if err == nil {
		t.Fatalf("expected error")
	}
	if store.ActiveVersion() != "2026.5.1" {
		t.Errorf("active=%q want rollback to 2026.5.1", store.ActiveVersion())
	}
	if got := u.Status().LastResult; got != "rolled_back_degraded" {
		t.Errorf("last_result=%q want rolled_back_degraded", got)
	}
}

func TestCheckAndApply_NoRollbackBestEffort(t *testing.T) {
	store := newFakeStore("2026.5.1")
	rel := &fakeRelease{tag: "2026.5.2"}
	ctl := &fakeCtl{followers: []string{"a", "b", "c"}, store: store, badOn: map[string]string{"b": "2026.5.2"}}
	// auto_rollback OFF → keep going past the failure, stay on new version.
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeFull, IntervalHours: 24, AutoRollback: false, HealthGraceSeconds: 1}, store, rel, ctl)

	err := u.CheckAndApply(context.Background(), RunOpts{})
	if err == nil {
		t.Fatalf("expected failure error")
	}
	if store.ActiveVersion() != "2026.5.2" {
		t.Errorf("active=%q want stay on 2026.5.2 (no rollback)", store.ActiveVersion())
	}
	// best-effort: all three attempted, no revert.
	if ctl.reloadCount() != 3 {
		t.Errorf("reloads=%v want 3", ctl.reloads)
	}
	if got := u.Status().LastResult; got != "failed" {
		t.Errorf("last_result=%q want failed", got)
	}
}

func TestCheckAndApply_NotifyModeNoDownload(t *testing.T) {
	store := newFakeStore("2026.5.1")
	rel := &fakeRelease{tag: "2026.5.2"}
	ctl := &fakeCtl{followers: []string{"a"}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeNotify, IntervalHours: 24, HealthGraceSeconds: 1}, store, rel, ctl)

	if err := u.CheckAndApply(context.Background(), RunOpts{}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(store.installCalls) != 0 {
		t.Errorf("notify must not download, got %v", store.installCalls)
	}
	if store.ActiveVersion() != "2026.5.1" {
		t.Errorf("notify must not activate")
	}
	if got := u.Status().LastResult; got != "notified" {
		t.Errorf("last_result=%q want notified", got)
	}
}

func TestCheckAndApply_DownloadModeNoApply(t *testing.T) {
	store := newFakeStore("2026.5.1")
	rel := &fakeRelease{tag: "2026.5.2"}
	ctl := &fakeCtl{followers: []string{"a"}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeDownload, IntervalHours: 24, HealthGraceSeconds: 1}, store, rel, ctl)

	if err := u.CheckAndApply(context.Background(), RunOpts{}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(store.installCalls) != 1 {
		t.Errorf("download mode must fetch, got %v", store.installCalls)
	}
	if store.ActiveVersion() != "2026.5.1" {
		t.Errorf("download mode must not activate")
	}
	if ctl.reloadCount() != 0 {
		t.Errorf("download mode must not restart")
	}
	st := u.Status()
	if st.LastResult != "downloaded" || st.PendingVersion != "2026.5.2" {
		t.Errorf("status=%+v want downloaded/pending 2026.5.2", st)
	}
}

func TestCheckAndApply_DownloadModeManualApply(t *testing.T) {
	store := newFakeStore("2026.5.1")
	rel := &fakeRelease{tag: "2026.5.2"}
	ctl := &fakeCtl{followers: []string{"a"}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeDownload, IntervalHours: 24, AutoRollback: true, HealthGraceSeconds: 1}, store, rel, ctl)

	// Apply:true overrides the mode → activate + restart.
	if err := u.CheckAndApply(context.Background(), RunOpts{Apply: true}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if store.ActiveVersion() != "2026.5.2" {
		t.Errorf("manual apply must activate, active=%q", store.ActiveVersion())
	}
	if ctl.reloadCount() != 1 {
		t.Errorf("manual apply must restart followers")
	}
}

func TestCheckAndApply_ForceReinstallSameVersion(t *testing.T) {
	store := newFakeStore("2026.5.2")
	rel := &fakeRelease{tag: "2026.5.2"}
	ctl := &fakeCtl{followers: []string{"a"}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeFull, IntervalHours: 24, AutoRollback: true, HealthGraceSeconds: 1}, store, rel, ctl)

	// Force:true must proceed even though already current.
	if err := u.CheckAndApply(context.Background(), RunOpts{Force: true}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(store.installCalls) != 1 {
		t.Errorf("force must reinstall, got %v", store.installCalls)
	}
}

func TestCheckAndApply_ExplicitVersionTarget(t *testing.T) {
	store := newFakeStore("2026.5.1")
	rel := &fakeRelease{tag: "2026.9.9"} // would be latest, but we target a specific tag
	ctl := &fakeCtl{followers: []string{}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeFull, IntervalHours: 24, AutoRollback: true, HealthGraceSeconds: 1}, store, rel, ctl)

	if err := u.CheckAndApply(context.Background(), RunOpts{Version: "2026.5.5", Apply: true}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(store.installCalls) != 1 || store.installCalls[0] != "2026.5.5" {
		t.Errorf("installCalls=%v want [2026.5.5]", store.installCalls)
	}
	if store.ActiveVersion() != "2026.5.5" {
		t.Errorf("active=%q want 2026.5.5", store.ActiveVersion())
	}
}

func TestRetention_PrunesOldKeepsActiveAndPinned(t *testing.T) {
	// Installed (besides active target): many old versions; keep=2.
	store := newFakeStore("2026.5.1", "2026.1.1", "2026.2.2", "2026.3.3", "2026.4.4")
	rel := &fakeRelease{tag: "2026.5.2"}
	// pin 2026.1.1 so it must survive pruning despite being oldest.
	ctl := &fakeCtl{followers: []string{}, pinned: map[string]struct{}{"2026.1.1": {}}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeFull, IntervalHours: 24, AutoRollback: true, KeepVersions: 2, HealthGraceSeconds: 1}, store, rel, ctl)

	if err := u.CheckAndApply(context.Background(), RunOpts{}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// After update active=2026.5.2. Non-active, non-pinned newest-first:
	// 2026.5.1, 2026.4.4, 2026.3.3, 2026.2.2  (2026.1.1 pinned, skipped).
	// keep=2 → keep 5.1 & 4.4, delete 3.3 & 2.2.
	deleted := map[string]bool{}
	for _, d := range store.deleteLog {
		deleted[d] = true
	}
	if !deleted["2026.3.3"] || !deleted["2026.2.2"] {
		t.Errorf("expected 3.3 and 2.2 pruned, deleteLog=%v", store.deleteLog)
	}
	if deleted["2026.1.1"] {
		t.Errorf("pinned 2026.1.1 must never be pruned")
	}
	if deleted["2026.5.2"] || deleted["2026.5.1"] || deleted["2026.4.4"] {
		t.Errorf("active/kept versions must survive, deleteLog=%v", store.deleteLog)
	}
}

// TestRetention_CalVerNumericOrder guards the fix for the string-vs-numeric
// ordering bug: once the CalVer month reaches double digits, "2026.9.0" sorts
// AFTER "2026.12.0" lexicographically, so a string-ordered prune would delete
// the genuinely-newest versions. prune must re-sort numerically.
func TestRetention_CalVerNumericOrder(t *testing.T) {
	// Installed (besides what we add): months spanning the single→double digit
	// boundary. fakeStore.List() sorts by STRING (like the real store), so this
	// reproduces the bug condition; prune must correct it.
	store := newFakeStore("2026.11.0", "2026.10.0", "2026.9.0", "2026.8.0")
	rel := &fakeRelease{tag: "2026.12.0"}
	ctl := &fakeCtl{followers: []string{}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeFull, IntervalHours: 24, AutoRollback: true, KeepVersions: 2, HealthGraceSeconds: 1}, store, rel, ctl)

	if err := u.CheckAndApply(context.Background(), RunOpts{}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// active=2026.12.0. Non-active numeric newest-first: 11, 10, 9, 8.
	// keep=2 → keep 11 & 10, delete 9 & 8.
	deleted := map[string]bool{}
	for _, d := range store.deleteLog {
		deleted[d] = true
	}
	if !deleted["2026.9.0"] || !deleted["2026.8.0"] {
		t.Errorf("expected 2026.9.0 & 2026.8.0 pruned (oldest), deleteLog=%v", store.deleteLog)
	}
	for _, keep := range []string{"2026.12.0", "2026.11.0", "2026.10.0"} {
		if deleted[keep] {
			t.Errorf("%s must NOT be pruned (it is among the newest), deleteLog=%v", keep, store.deleteLog)
		}
	}
}

func TestCheckAndApply_InstallFailureLeavesActive(t *testing.T) {
	store := newFakeStore("2026.5.1")
	store.installErr = errors.New("network down")
	rel := &fakeRelease{tag: "2026.5.2"}
	ctl := &fakeCtl{followers: []string{"a"}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeFull, IntervalHours: 24, AutoRollback: true, HealthGraceSeconds: 1}, store, rel, ctl)

	if err := u.CheckAndApply(context.Background(), RunOpts{}); err == nil {
		t.Fatalf("expected install error")
	}
	if store.ActiveVersion() != "2026.5.1" {
		t.Errorf("install failure must not change active, got %q", store.ActiveVersion())
	}
	if ctl.reloadCount() != 0 {
		t.Errorf("install failure must not restart")
	}
	if got := u.Status().LastResult; got != "failed" {
		t.Errorf("last_result=%q want failed", got)
	}
}

func TestCheckAndApply_BusyReturnsErrBusy(t *testing.T) {
	store := newFakeStore("2026.5.1")
	rel := &fakeRelease{tag: "2026.5.2"}
	ctl := &fakeCtl{followers: []string{}}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeFull, IntervalHours: 24, HealthGraceSeconds: 1}, store, rel, ctl)

	u.runMu.Lock() // simulate an in-flight operation
	defer u.runMu.Unlock()
	if err := u.CheckAndApply(context.Background(), RunOpts{}); !errors.Is(err, ErrBusy) {
		t.Errorf("want ErrBusy, got %v", err)
	}
}

func TestBootstrapIfMissing(t *testing.T) {
	store := newFakeStore("") // empty store
	rel := &fakeRelease{tag: "2026.5.2"}
	ctl := &fakeCtl{}
	u := newTestUpdater(t, Settings{Enabled: true, Mode: ModeFull, IntervalHours: 24, HealthGraceSeconds: 1}, store, rel, ctl)

	if err := u.BootstrapIfMissing(context.Background()); err != nil {
		t.Fatalf("bootstrap err: %v", err)
	}
	if store.ActiveVersion() != "2026.5.2" {
		t.Errorf("bootstrap must install+activate latest, active=%q", store.ActiveVersion())
	}

	// Second call is a no-op (already has a binary).
	before := len(store.installCalls)
	if err := u.BootstrapIfMissing(context.Background()); err != nil {
		t.Fatalf("bootstrap2 err: %v", err)
	}
	if len(store.installCalls) != before {
		t.Errorf("bootstrap must no-op when a binary exists")
	}
}

func TestBootstrapIfMissing_DisabledNoop(t *testing.T) {
	store := newFakeStore("")
	rel := &fakeRelease{tag: "2026.5.2"}
	u := newTestUpdater(t, Settings{Enabled: false, Mode: ModeFull, IntervalHours: 24, HealthGraceSeconds: 1}, store, rel, &fakeCtl{})
	if err := u.BootstrapIfMissing(context.Background()); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(store.installCalls) != 0 {
		t.Errorf("disabled bootstrap must not download")
	}
}

func TestSettingsNormalized(t *testing.T) {
	s := Settings{Mode: "WeIrD", IntervalHours: 0, KeepVersions: 999, HealthGraceSeconds: -5}.Normalized()
	if s.Mode != ModeFull {
		t.Errorf("unknown mode must collapse to full, got %q", s.Mode)
	}
	if s.IntervalHours != 1 {
		t.Errorf("interval clamp lo failed: %d", s.IntervalHours)
	}
	if s.KeepVersions != 50 {
		t.Errorf("keep clamp hi failed: %d", s.KeepVersions)
	}
	if s.HealthGraceSeconds != 1 {
		t.Errorf("grace clamp lo failed: %d", s.HealthGraceSeconds)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2026.5.1", "2026.5.2", -1},
		{"2026.5.2", "2026.5.1", 1},
		{"2026.5.2", "2026.5.2", 0},
		{"v2026.5.2", "2026.5.2", 0},
		{"2026.10.0", "2026.9.9", 1},
		{"2026.5.2-beta", "2026.5.2", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}
