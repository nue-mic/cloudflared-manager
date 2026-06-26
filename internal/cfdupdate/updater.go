package cfdupdate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nue-mic/cloudflared-manager/internal/cfdbin"
	"github.com/nue-mic/cloudflared-manager/internal/eventbus"
)

// ErrBusy is returned by CheckAndApply when another update operation already
// holds the run lock. The API maps it to 409 Conflict.
var ErrBusy = errors.New("cfdupdate: an update operation is already in progress")

// ReleaseResolver resolves the newest upstream release tag without
// downloading. *cfdbin.Downloader satisfies it.
type ReleaseResolver interface {
	ResolveLatest(ctx context.Context, includePrerelease bool) (string, error)
}

// BinaryStore is the subset of the on-disk binary store the updater drives.
// The production implementation is StoreAdapter (which binds the concrete
// *cfdbin.Downloader into Install).
type BinaryStore interface {
	ActiveVersion() string
	Install(ctx context.Context, version string) (cfdbin.VersionMeta, error)
	Activate(version string) error
	List() ([]cfdbin.InstalledVersion, error)
	Delete(version string) error
}

// Restarter is the subset of the Manager the updater needs to apply a new
// binary to running instances. *manager.Manager satisfies it structurally.
type Restarter interface {
	// ActiveFollowerRunningIDs lists running instances that follow the active
	// version (binaryVersion empty/"current"); pinned instances are excluded.
	ActiveFollowerRunningIDs() []string
	// Reload restarts one instance (stop+start) so it picks up the new active
	// binary.
	Reload(id string) error
	// WaitHealthy reports whether an instance stays up for the grace window.
	WaitHealthy(id string, grace time.Duration) bool
	// PinnedBinaryVersions returns versions some instance pins; retention must
	// never prune these.
	PinnedBinaryVersions() map[string]struct{}
}

// Status is the live view of the updater, returned by the API GET endpoint.
type Status struct {
	State          string `json:"state"`           // idle|checking|downloading|applying|restarting|rolling_back
	LastResult     string `json:"last_result"`     // up_to_date|updated|downloaded|notified|failed|rolled_back|""
	LastError      string `json:"last_error,omitempty"`
	LastCheckAt    string `json:"last_check_at,omitempty"` // RFC3339
	ActiveVersion  string `json:"active_version,omitempty"`
	LatestKnown    string `json:"latest_known,omitempty"`
	PendingVersion string `json:"pending_version,omitempty"`
	InProgress     bool   `json:"in_progress"`
}

// RunOpts parameterise a manual CheckAndApply. The zero value is the periodic
// (scheduled) behaviour: resolve latest, honour mode, no force.
type RunOpts struct {
	Version string // explicit target tag; "" => resolve latest
	Force   bool   // proceed even if not newer / reinstall + reactivate
	Apply   bool   // force activate+restart regardless of mode (manual "update now")
}

// Config bundles the updater's dependencies. Load/Save persist Settings
// (the daemon backs them with meta.json); the conversion to the manager's
// storage struct lives at the wiring layer so this package stays manager-free.
type Config struct {
	Store        BinaryStore
	Release      ReleaseResolver
	Controller   Restarter
	Bus          *eventbus.Bus
	Logger       *slog.Logger
	Load         func() (Settings, bool)
	Save         func(Settings) error
	InitialDelay time.Duration // first scheduled check delay; 0 => 30s
}

// Updater is the auto-update engine.
type Updater struct {
	store   BinaryStore
	release ReleaseResolver
	ctl     Restarter
	bus     *eventbus.Bus
	log     *slog.Logger
	save    func(Settings) error

	initialDelay time.Duration

	mu       sync.Mutex // guards settings + status
	settings Settings
	status   Status

	runMu   sync.Mutex  // serialises CheckAndApply (try-locked: busy => ErrBusy)
	running atomic.Bool // mirrors runMu for status.InProgress

	resetCh chan struct{} // nudges Run to reset its ticker after a settings change
}

// New constructs an Updater, seeding settings from Load (meta.json) or, on
// first boot, from env defaults — which it then persists via Save.
func New(cfg Config) *Updater {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	delay := cfg.InitialDelay
	if delay <= 0 {
		delay = 30 * time.Second
	}
	s, ok := Settings{}, false
	if cfg.Load != nil {
		s, ok = cfg.Load()
	}
	if ok {
		s = s.Normalized()
	} else {
		s = DefaultSettings()
	}
	u := &Updater{
		store:        cfg.Store,
		release:      cfg.Release,
		ctl:          cfg.Controller,
		bus:          cfg.Bus,
		log:          log,
		save:         cfg.Save,
		initialDelay: delay,
		settings:     s,
		resetCh:      make(chan struct{}, 1),
	}
	if !ok && cfg.Save != nil {
		_ = cfg.Save(s) // seed meta.json so the UI shows the effective config
	}
	return u
}

// Settings returns a snapshot of the current configuration.
func (u *Updater) Settings() Settings {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.settings
}

// SetSettings validates, persists and applies a new configuration. It nudges
// the Run loop to reset its ticker so an interval change takes effect at once.
func (u *Updater) SetSettings(s Settings) (Settings, error) {
	s = s.Normalized()
	if u.save != nil {
		if err := u.save(s); err != nil {
			return Settings{}, err
		}
	}
	u.mu.Lock()
	u.settings = s
	u.mu.Unlock()
	select {
	case u.resetCh <- struct{}{}:
	default:
	}
	return s, nil
}

// Status returns the live status, with ActiveVersion + InProgress refreshed
// from the source of truth on each call.
func (u *Updater) Status() Status {
	u.mu.Lock()
	st := u.status
	u.mu.Unlock()
	st.ActiveVersion = u.store.ActiveVersion()
	st.InProgress = u.running.Load()
	return st
}

// BootstrapIfMissing downloads + activates the latest binary when the store
// has no active version yet and auto-update is enabled. It is meant to run
// synchronously at startup, BEFORE instances auto-start, so the very first
// boot self-provisions a usable cloudflared. A failure is non-fatal (the
// caller logs it and lets instances fall back to PATH).
func (u *Updater) BootstrapIfMissing(ctx context.Context) error {
	s := u.Settings()
	if !s.Enabled {
		return nil
	}
	if u.store.ActiveVersion() != "" {
		return nil
	}
	u.log.Info("cfdupdate: no cloudflared binary present, bootstrapping latest")
	if !u.runMu.TryLock() {
		return ErrBusy
	}
	defer u.runMu.Unlock()
	u.running.Store(true)
	defer u.running.Store(false)

	u.setState("downloading")
	u.publish(eventbus.BinaryUpdateData{Phase: "downloading", Message: "bootstrap"})
	target, err := u.release.ResolveLatest(ctx, s.IncludePrerelease)
	if err != nil {
		return u.fail("checking", "", err)
	}
	meta, err := u.store.Install(ctx, target)
	if err != nil {
		return u.fail("downloading", target, err)
	}
	if err := u.store.Activate(meta.Version); err != nil {
		return u.fail("applying", meta.Version, err)
	}
	u.finish("idle", "updated", "")
	u.setLatest(meta.Version)
	u.publish(eventbus.BinaryUpdateData{Phase: "done", To: meta.Version, Message: "bootstrap"})
	u.log.Info("cfdupdate: bootstrapped cloudflared", slog.String("version", meta.Version))
	return nil
}

// Run is the scheduler loop: after an initial settle delay it checks once,
// then every interval. A settings change resets the interval immediately.
// Periodic checks are skipped while disabled (manual runs still work).
func (u *Updater) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(u.initialDelay):
	}
	u.tick(ctx)

	t := time.NewTicker(u.intervalDuration())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-u.resetCh:
			t.Reset(u.intervalDuration())
		case <-t.C:
			u.tick(ctx)
		}
	}
}

func (u *Updater) intervalDuration() time.Duration {
	h := u.Settings().IntervalHours
	if h < 1 {
		h = 1
	}
	return time.Duration(h) * time.Hour
}

// tick runs one scheduled check honouring the configured mode.
func (u *Updater) tick(ctx context.Context) {
	if !u.Settings().Enabled {
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err := u.CheckAndApply(runCtx, RunOpts{}); err != nil && !errors.Is(err, ErrBusy) {
		// fail() already recorded + logged; nothing more to do here.
		_ = err
	}
}

// InProgress reports whether an update operation is currently running.
func (u *Updater) InProgress() bool { return u.running.Load() }

// CheckAndApply runs the full pipeline once, synchronously. It is used by the
// scheduler and by tests; the run lock serialises it against other triggers.
func (u *Updater) CheckAndApply(ctx context.Context, opts RunOpts) error {
	if !u.runMu.TryLock() {
		return ErrBusy
	}
	defer u.runMu.Unlock()
	u.running.Store(true)
	defer u.running.Store(false)
	return u.runLocked(ctx, opts)
}

// TriggerAsync starts the pipeline in the background and returns immediately,
// so a manual API trigger need not block on a minutes-long download+restart.
// It returns ErrBusy without starting anything when an operation is already
// running. The background run uses its own timeout, independent of any request.
func (u *Updater) TriggerAsync(opts RunOpts) error {
	if !u.runMu.TryLock() {
		return ErrBusy
	}
	// Set running on THIS goroutine before spawning, so runMu-held always
	// implies running==true — otherwise Status() could briefly report
	// in_progress=false for an operation that already rejects triggers with 409.
	u.running.Store(true)
	go func() {
		defer u.runMu.Unlock()
		defer u.running.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		_ = u.runLocked(ctx, opts)
	}()
	return nil
}

// runLocked is the actual pipeline body. Callers MUST hold runMu and manage
// the running flag.
func (u *Updater) runLocked(ctx context.Context, opts RunOpts) error {
	s := u.Settings()

	// 1. checking — resolve the target tag.
	u.setState("checking")
	u.publish(eventbus.BinaryUpdateData{Phase: "checking"})
	target := strings.TrimSpace(opts.Version)
	if target == "" {
		t, err := u.release.ResolveLatest(ctx, s.IncludePrerelease)
		if err != nil {
			return u.fail("checking", "", err)
		}
		target = t
	}
	u.mu.Lock()
	u.status.LatestKnown = target
	u.status.LastCheckAt = time.Now().UTC().Format(time.RFC3339)
	u.mu.Unlock()

	active := u.store.ActiveVersion()

	// 2. compare — short-circuit when already current (unless forced).
	if !opts.Force && active != "" && compareVersions(active, target) >= 0 {
		u.finish("idle", "up_to_date", "")
		u.publish(eventbus.BinaryUpdateData{Phase: "up_to_date", Version: active})
		return nil
	}

	// An update exists. In notify mode (and not an explicit apply) we stop
	// here WITHOUT downloading — "notify" means just surface that a newer
	// version is available.
	apply := opts.Apply || s.Mode == ModeFull
	if !apply && s.Mode == ModeNotify {
		u.finish("idle", "notified", "")
		u.publish(eventbus.BinaryUpdateData{Phase: "available", Version: target})
		return nil
	}

	// 3. downloading — fetch + verify (idempotent; never touches the running set).
	u.setState("downloading")
	u.publish(eventbus.BinaryUpdateData{Phase: "downloading", Version: target})
	meta, err := u.store.Install(ctx, target)
	if err != nil {
		return u.fail("downloading", target, err)
	}
	target = meta.Version // concrete tag (Install resolves "latest"/forced reinstall)
	u.publish(eventbus.BinaryUpdateData{Phase: "downloaded", Version: target})

	// 4. download-only mode stops after a verified download (pending apply).
	if !apply {
		u.mu.Lock()
		u.status.PendingVersion = target
		u.mu.Unlock()
		u.finish("idle", "downloaded", "")
		return nil
	}

	// 5. applying — flip active, remembering the previous version for rollback.
	u.setState("applying")
	prev := u.store.ActiveVersion()
	u.publish(eventbus.BinaryUpdateData{Phase: "applying", From: prev, To: target})
	if err := u.store.Activate(target); err != nil {
		return u.fail("applying", target, err)
	}

	// 6. restarting — rolling restart of active-followers, with rollback.
	if err := u.rollingRestart(s, prev, target); err != nil {
		return err
	}

	// 7. retention — prune old non-active, non-pinned versions.
	u.prune(s.KeepVersions)

	u.mu.Lock()
	u.status.PendingVersion = ""
	u.mu.Unlock()
	u.finish("idle", "updated", "")
	u.publish(eventbus.BinaryUpdateData{Phase: "done", From: prev, To: target})
	u.log.Info("cfdupdate: applied cloudflared update",
		slog.String("from", prev), slog.String("to", target))
	return nil
}

// rollingRestart restarts each active-follower one at a time, health-probing
// after each. On the first instance that fails to come up healthy it rolls
// back the active version to prev and reverts every instance it already
// touched (auto_rollback). With auto_rollback off it restarts the rest
// best-effort and reports the failure.
func (u *Updater) rollingRestart(s Settings, prev, target string) error {
	ids := u.ctl.ActiveFollowerRunningIDs()
	u.setState("restarting")
	grace := time.Duration(s.HealthGraceSeconds) * time.Second

	restarted := make([]string, 0, len(ids))
	var restartErr error
	var failedID string

	for _, id := range ids {
		u.publish(eventbus.BinaryUpdateData{Phase: "restarting", InstanceID: id, Version: target})
		err := u.ctl.Reload(id)
		if err == nil && !u.ctl.WaitHealthy(id, grace) {
			err = fmt.Errorf("instance %s did not stay healthy on %s", id, target)
		}
		if err != nil {
			restartErr = err
			failedID = id
			u.log.Warn("cfdupdate: instance restart failed",
				slog.String("id", id), slog.String("version", target), slog.Any("err", err))
			if s.AutoRollback {
				break // stop early; revert below
			}
			u.publish(eventbus.BinaryUpdateData{Phase: "error", Version: target, InstanceID: id, Error: err.Error()})
			continue // best-effort: keep going
		}
		restarted = append(restarted, id)
	}

	if restartErr == nil {
		return nil
	}

	// Rollback path.
	if s.AutoRollback && prev != "" && prev != target {
		u.setState("rolling_back")
		u.log.Warn("cfdupdate: rolling back cloudflared",
			slog.String("from", target), slog.String("to", prev), slog.Any("trigger", restartErr))
		u.publish(eventbus.BinaryUpdateData{Phase: "applying", From: target, To: prev, Message: "rolling back"})
		if err := u.store.Activate(prev); err != nil {
			u.log.Error("cfdupdate: rollback activate failed", slog.Any("err", err))
		}
		// Revert every instance we already moved to target, plus the one that
		// failed, back onto prev — and re-probe each so we don't report a clean
		// recovery while a tunnel is actually down (e.g. prev binary also bad).
		revert := append(append([]string{}, restarted...), failedID)
		var degraded []string
		for _, id := range revert {
			if id == "" {
				continue
			}
			if err := u.ctl.Reload(id); err != nil {
				u.log.Warn("cfdupdate: rollback reload failed", slog.String("id", id), slog.Any("err", err))
				degraded = append(degraded, id)
				continue
			}
			if !u.ctl.WaitHealthy(id, grace) {
				u.log.Warn("cfdupdate: instance unhealthy after rollback", slog.String("id", id))
				degraded = append(degraded, id)
			}
		}
		u.mu.Lock()
		u.status.PendingVersion = ""
		u.mu.Unlock()
		if len(degraded) > 0 {
			// Rollback completed but one or more instances did not recover —
			// report the degraded state truthfully rather than a clean rollback.
			err := fmt.Errorf("%w；回滚后以下实例未恢复健康: %v", restartErr, degraded)
			u.finishErr("idle", "rolled_back_degraded", err)
			u.publish(eventbus.BinaryUpdateData{Phase: "rolled_back", From: target, To: prev, InstanceID: failedID, Error: err.Error(), Message: "degraded"})
			u.log.Error("cfdupdate: rollback degraded", slog.Any("instances", degraded))
			return err
		}
		u.finishErr("idle", "rolled_back", restartErr)
		u.publish(eventbus.BinaryUpdateData{Phase: "rolled_back", From: target, To: prev, InstanceID: failedID, Error: restartErr.Error()})
		return restartErr
	}

	// No rollback (disabled, or nothing to roll back to): report failure.
	u.finishErr("idle", "failed", restartErr)
	u.publish(eventbus.BinaryUpdateData{Phase: "error", Version: target, InstanceID: failedID, Error: restartErr.Error()})
	return restartErr
}

// prune keeps the active version, every pinned version, and the keep newest
// other versions; it deletes the rest. keep <= 0 disables pruning. The store
// refuses to delete the active version, so this is doubly safe.
func (u *Updater) prune(keep int) {
	if keep <= 0 {
		return
	}
	list, err := u.store.List()
	if err != nil {
		u.log.Warn("cfdupdate: retention list failed", slog.Any("err", err))
		return
	}
	// store.List() sorts by STRING, which mis-orders CalVer once the month
	// segment reaches double digits (e.g. "2026.9.0" > "2026.12.0"
	// lexicographically). Re-sort numerically here so "keep newest N" is
	// correct — otherwise retention could prune genuinely-newer binaries.
	sort.SliceStable(list, func(i, j int) bool {
		return compareVersions(list[i].Version, list[j].Version) > 0 // newest first
	})
	pinned := u.ctl.PinnedBinaryVersions()
	active := u.store.ActiveVersion()
	kept := 0
	for _, v := range list { // now genuinely newest-first (numeric CalVer order)
		if v.IsActive || v.Version == active {
			continue
		}
		if _, ok := pinned[v.Version]; ok {
			continue
		}
		kept++
		if kept <= keep {
			continue
		}
		if err := u.store.Delete(v.Version); err != nil {
			u.log.Warn("cfdupdate: retention delete failed",
				slog.String("version", v.Version), slog.Any("err", err))
		} else {
			u.log.Info("cfdupdate: pruned old cloudflared", slog.String("version", v.Version))
		}
	}
}

// ── status helpers ──────────────────────────────────────────────────────────

func (u *Updater) setState(state string) {
	u.mu.Lock()
	u.status.State = state
	u.status.InProgress = state != "idle"
	u.mu.Unlock()
}

func (u *Updater) setLatest(v string) {
	u.mu.Lock()
	u.status.LatestKnown = v
	u.mu.Unlock()
}

func (u *Updater) finish(state, result, errMsg string) {
	u.mu.Lock()
	u.status.State = state
	u.status.LastResult = result
	u.status.LastError = errMsg
	u.status.InProgress = false
	u.mu.Unlock()
}

func (u *Updater) finishErr(state, result string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	u.finish(state, result, msg)
}

func (u *Updater) fail(phase, version string, err error) error {
	u.finishErr("idle", "failed", err)
	u.publish(eventbus.BinaryUpdateData{Phase: "error", Version: version, Message: phase, Error: err.Error()})
	u.log.Warn("cfdupdate: update failed", slog.String("phase", phase), slog.Any("err", err))
	return err
}

func (u *Updater) publish(d eventbus.BinaryUpdateData) {
	if u.bus != nil {
		u.bus.Publish(eventbus.TypeBinaryUpdate, "", d)
	}
}

// ── version comparison (CalVer / 3-segment numeric, tolerant of a leading
// "v" and any pre-release/build suffix) ─────────────────────────────────────

// compareVersions returns -1, 0 or 1 for a<b, a==b, a>b. cloudflared uses
// CalVer (e.g. 2026.5.2), which this compares correctly segment by segment.
func compareVersions(a, b string) int {
	pa, pb := parseVer(a), parseVer(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVer(s string) [3]int {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+ "); i >= 0 {
		s = s[:i]
	}
	var out [3]int
	for i, part := range strings.SplitN(s, ".", 3) {
		if i >= 3 {
			break
		}
		out[i], _ = strconv.Atoi(strings.TrimSpace(part))
	}
	return out
}
