package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/mia-clark/cloudflared-manager/internal/cfdbin"
	"github.com/mia-clark/cloudflared-manager/internal/eventbus"
	"github.com/mia-clark/cloudflared-manager/pkg/cfdconfig"
	"github.com/mia-clark/cloudflared-manager/pkg/cfdstate"
)

// Options configures the Manager.
type Options struct {
	ProfilesDir string
	LogsDir     string
	StoresDir   string
	MetaPath    string
	Logger      *slog.Logger
	Bus         *eventbus.Bus
	BinaryStore *cfdbin.Store // may be nil; instance falls back to PATH lookup
}

// Manager is the central registry of cloudflared connector instances.
// It owns the {data_dir}/profiles directory and gates every read/write
// to config files. Each running connector lives in its own external
// child process supervised by internal/process.Worker (PR-04 replaced
// the previous frps re-exec-self model).
type Manager struct {
	opts     Options
	binStore *cfdbin.Store // mirrors opts.BinaryStore for convenience

	mu        sync.RWMutex
	instances map[string]*instance
	logs      map[string]*instanceLog // per-id append log writers

	meta *metaStore

	rootCtx    context.Context
	rootCancel context.CancelFunc
}

// New constructs a Manager backed by the directories in opts. It does not
// scan the profiles dir; call LoadAll for that.
func New(opts Options) (*Manager, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Bus == nil {
		opts.Bus = eventbus.New(1024)
	}
	meta, err := openMetaStore(opts.MetaPath)
	if err != nil {
		return nil, fmt.Errorf("open meta: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		opts:       opts,
		binStore:   opts.BinaryStore,
		instances:  make(map[string]*instance),
		logs:       make(map[string]*instanceLog),
		meta:       meta,
		rootCtx:    ctx,
		rootCancel: cancel,
	}, nil
}

// Bus exposes the event bus so the API layer can subscribe.
func (m *Manager) Bus() *eventbus.Bus { return m.opts.Bus }

// LoadAll scans the profiles dir and registers every parseable config
// file as an instance in the stopped state. Files that cannot be read
// or parsed as TunnelConfigV1 YAML are logged and skipped.
func (m *Manager) LoadAll() error {
	files, err := filepath.Glob(filepath.Join(m.opts.ProfilesDir, "*.yaml"))
	if err != nil {
		return err
	}
	for _, f := range files {
		b, rerr := os.ReadFile(f)
		if rerr != nil {
			m.opts.Logger.Warn("skip unreadable config", slog.String("path", f), slog.Any("err", rerr))
			continue
		}
		if _, perr := cfdconfig.ParseYAML(b); perr != nil {
			m.opts.Logger.Warn("skip unparseable tunnel config", slog.String("path", f), slog.Any("err", perr))
			continue
		}
		id := idFromPath(f)
		m.register(id, f)
	}
	return nil
}

// register builds and stores an instance for id at path. Caller must ensure
// the file exists and parses.
func (m *Manager) register(id, path string) *instance {
	inst := newInstance(id, path, m.opts.Logger, m.opts.Bus, m.logWriter(id), m.binStore)
	m.mu.Lock()
	m.instances[id] = inst
	m.mu.Unlock()
	return inst
}

// AutoStart launches every loaded instance whose meta.manualStart is not
// true. Default (unset / false) means auto-start. Errors are logged but do
// not abort the daemon. Boot order follows meta.json sort.
func (m *Manager) AutoStart() {
	for _, id := range m.orderedIDs() {
		if m.meta.manualStart(id) {
			continue
		}
		if err := m.Start(id); err != nil {
			m.opts.Logger.Warn("auto-start failed", slog.String("id", id), slog.Any("err", err))
		}
	}
}

// Shutdown stops every running instance and releases resources.
func (m *Manager) Shutdown() {
	m.rootCancel()
	m.mu.RLock()
	ids := make([]string, 0, len(m.instances))
	for id := range m.instances {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if inst := m.get(id); inst != nil {
				_ = inst.stop()
			}
		}(id)
	}
	wg.Wait()
}

func (m *Manager) get(id string) *instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.instances[id]
}

// Exists reports whether an instance with this id is registered.
func (m *Manager) Exists(id string) bool { return m.get(id) != nil }

// orderedIDs returns all instance ids sorted by meta.json Sort (unknown ids
// appended in id order) for deterministic boot/list ordering.
func (m *Manager) orderedIDs() []string {
	m.mu.RLock()
	ids := make([]string, 0, len(m.instances))
	for id := range m.instances {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	order := m.meta.snapshot().Sort
	idx := make(map[string]int, len(order))
	for i, id := range order {
		idx[id] = i
	}
	sort.SliceStable(ids, func(a, b int) bool {
		ia, oka := idx[ids[a]]
		ib, okb := idx[ids[b]]
		switch {
		case oka && okb:
			return ia < ib
		case oka:
			return true
		case okb:
			return false
		default:
			return ids[a] < ids[b]
		}
	})
	return ids
}

// nameOf returns the display name for id from meta, falling back to id.
func (m *Manager) nameOf(id string) string {
	if n := m.meta.name(id); n != "" {
		return n
	}
	return id
}

// List returns a snapshot of every registered instance, ordered by meta.json.
func (m *Manager) List() []Snapshot {
	ids := m.orderedIDs()
	out := make([]Snapshot, 0, len(ids))
	for _, id := range ids {
		if inst := m.get(id); inst != nil {
			s := inst.Snapshot()
			s.Name = m.nameOf(id)
			s.LogPath = m.LogPath(id)
			out = append(out, s)
		}
	}
	return out
}

// Get returns the snapshot of a single config plus the parsed TunnelConfigV1
// read fresh from disk, and its manager metadata (name/manualStart).
func (m *Manager) Get(id string) (Snapshot, *cfdconfig.TunnelConfigV1, MgrMeta, error) {
	inst := m.get(id)
	if inst == nil {
		return Snapshot{}, nil, MgrMeta{}, ErrNotFound
	}
	b, err := os.ReadFile(inst.Path())
	if err != nil {
		return Snapshot{}, nil, MgrMeta{}, err
	}
	sc, err := cfdconfig.ParseYAML(b)
	if err != nil {
		return Snapshot{}, nil, MgrMeta{}, err
	}
	snap := inst.Snapshot()
	snap.Name = m.nameOf(id)
	snap.LogPath = m.LogPath(id)
	mm := MgrMeta{Name: m.nameOf(id), ManualStart: m.meta.manualStart(id)}
	return snap, sc, mm, nil
}

// MgrMeta carries the manager-level metadata (display name, manual start)
// that lives in meta.json rather than in the tunnel YAML.
type MgrMeta struct {
	Name        string `json:"name"`
	ManualStart bool   `json:"manualStart"`
}

// Create persists a new tunnel config file and registers an instance.
func (m *Manager) Create(id string, sc *cfdconfig.TunnelConfigV1, mm MgrMeta) error {
	if err := validateID(id); err != nil {
		return err
	}
	if m.Exists(id) {
		return ErrExists
	}
	b, err := cfdconfig.MarshalYAML(sc)
	if err != nil {
		return err
	}
	path := m.pathFor(id)
	if err := writeAtomic(path, b); err != nil {
		return err
	}
	_ = m.meta.setName(id, mm.Name)
	_ = m.meta.setManualStart(id, mm.ManualStart)
	m.register(id, path)
	cur := m.meta.snapshot().Sort
	if !slices.Contains(cur, id) {
		_ = m.meta.setSort(append(cur, id))
	}
	return nil
}

// Update replaces the whole config body for an existing instance. If running,
// it is reloaded (= stop + start, since cloudflared params need a restart).
func (m *Manager) Update(id string, sc *cfdconfig.TunnelConfigV1, mm MgrMeta) error {
	inst := m.get(id)
	if inst == nil {
		return ErrNotFound
	}
	b, err := cfdconfig.MarshalYAML(sc)
	if err != nil {
		return err
	}
	if err := writeAtomic(inst.Path(), b); err != nil {
		return err
	}
	_ = m.meta.setName(id, mm.Name)
	_ = m.meta.setManualStart(id, mm.ManualStart)
	if inst.State() == cfdstate.ConfigStateStarted {
		if err := inst.reload(m.rootCtx); err != nil {
			m.opts.Logger.Warn("reload after update failed", slog.String("id", id), slog.Any("err", err))
		}
	}
	if m.opts.Bus != nil {
		m.opts.Bus.Publish(eventbus.TypeConfigChanged, id, nil)
	}
	return nil
}

// Delete stops the instance (if running), removes the file and meta.
func (m *Manager) Delete(id string) error {
	inst := m.get(id)
	if inst == nil {
		return ErrNotFound
	}
	_ = inst.stop()

	if err := os.Remove(inst.Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	m.mu.Lock()
	delete(m.instances, id)
	if lw := m.logs[id]; lw != nil {
		lw.close()
		delete(m.logs, id)
	}
	m.mu.Unlock()
	_ = m.meta.dropIDs(id)
	if m.opts.Bus != nil {
		m.opts.Bus.Publish(eventbus.TypeConfigDeleted, id, nil)
	}
	return nil
}

// Start launches the instance. No-op if already running.
func (m *Manager) Start(id string) error {
	inst := m.get(id)
	if inst == nil {
		return ErrNotFound
	}
	return inst.start(m.rootCtx)
}

// Stop terminates the instance. No-op if already stopped.
func (m *Manager) Stop(id string) error {
	inst := m.get(id)
	if inst == nil {
		return ErrNotFound
	}
	return inst.stop()
}

// Reload restarts the underlying frps service (server params need a restart).
func (m *Manager) Reload(id string) error {
	inst := m.get(id)
	if inst == nil {
		return ErrNotFound
	}
	return inst.reload(m.rootCtx)
}

// ReadRaw returns the raw bytes of the config file on disk.
func (m *Manager) ReadRaw(id string) ([]byte, error) {
	inst := m.get(id)
	if inst == nil {
		return nil, ErrNotFound
	}
	return os.ReadFile(inst.Path())
}

// WriteRaw replaces the config file with raw YAML bytes after a parse
// check. A running instance is reloaded (restart).
func (m *Manager) WriteRaw(id string, b []byte) error {
	inst := m.get(id)
	if inst == nil {
		return ErrNotFound
	}
	if _, err := cfdconfig.ParseYAML(b); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if err := writeAtomic(inst.Path(), b); err != nil {
		return err
	}
	if inst.State() == cfdstate.ConfigStateStarted {
		if err := inst.reload(m.rootCtx); err != nil {
			m.opts.Logger.Warn("reload after raw write failed", slog.String("id", id), slog.Any("err", err))
		}
	}
	return nil
}

// Reorder persists the visual ordering used by the API list response.
func (m *Manager) Reorder(order []string) error {
	known := make(map[string]struct{})
	m.mu.RLock()
	for id := range m.instances {
		known[id] = struct{}{}
	}
	m.mu.RUnlock()
	cleaned := make([]string, 0, len(order))
	for _, id := range order {
		if _, ok := known[id]; ok {
			cleaned = append(cleaned, id)
		}
	}
	return m.meta.setSort(cleaned)
}

// ProfilesDir reports the directory the manager owns.
func (m *Manager) ProfilesDir() string { return m.opts.ProfilesDir }

// MetaPath reports the path of the daemon meta.json file (display names,
// manual-start flags, ordering). Used by the export bundle.
func (m *Manager) MetaPath() string { return m.opts.MetaPath }

// ImportMeta applies display names / manual-start flags / ordering from an
// imported meta.json, but ONLY for ids that currently exist — so restoring a
// backup cannot resurrect metadata for configs that were not also restored.
func (m *Manager) ImportMeta(names map[string]string, manual map[string]bool, order []string) {
	for id, n := range names {
		if m.Exists(id) {
			_ = m.meta.setName(id, n)
		}
	}
	for id, v := range manual {
		if m.Exists(id) {
			_ = m.meta.setManualStart(id, v)
		}
	}
	if len(order) > 0 {
		_ = m.Reorder(order) // Reorder already filters to known ids
	}
}

// LogPath returns the per-instance log file path.
func (m *Manager) LogPath(id string) string {
	return filepath.Join(m.opts.LogsDir, id+".log")
}

// RunningIDs returns the ids of all instances currently in the started state.
// Used by the metrics sampler to know which workers to poll.
func (m *Manager) RunningIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.instances))
	for id, inst := range m.instances {
		if inst.State() == cfdstate.ConfigStateStarted {
			out = append(out, id)
		}
	}
	return out
}

// MetricsAddr returns the cloudflared --metrics 127.0.0.1:<port>
// address for the running instance with id, or "" + false when the
// instance is not registered or not in the started state.
func (m *Manager) MetricsAddr(id string) (string, bool) {
	inst := m.get(id)
	if inst == nil {
		return "", false
	}
	if inst.State() != cfdstate.ConfigStateStarted {
		return "", false
	}
	return inst.MetricsAddr(), true
}

// logWriter returns (creating if needed) the per-id append log writer that
// receives the worker's stdout/stderr.
func (m *Manager) logWriter(id string) *instanceLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lw := m.logs[id]; lw != nil {
		return lw
	}
	lw := &instanceLog{path: filepath.Join(m.opts.LogsDir, id+".log")}
	m.logs[id] = lw
	return lw
}

func (m *Manager) pathFor(id string) string {
	return filepath.Join(m.opts.ProfilesDir, id+".yaml")
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func validateID(id string) error {
	if id == "" {
		return errors.New("id must not be empty")
	}
	if strings.ContainsAny(id, `/\:?*<>|"'`) {
		return errors.New("id contains illegal characters")
	}
	if strings.HasPrefix(id, ".") {
		return errors.New("id must not start with dot")
	}
	if len(id) > 64 {
		return errors.New("id too long")
	}
	return nil
}

// LogViewSince returns the "log cleared at" watermark (unix millis) for id.
func (m *Manager) LogViewSince(id string) int64 {
	return m.meta.logViewSince(id)
}

// SetLogViewSince records a user "clear logs" action.
func (m *Manager) SetLogViewSince(id string, unixMilli int64) error {
	return m.meta.setLogViewSince(id, unixMilli)
}

// instanceLog is a lazily-opened, mutex-guarded append writer for one
// instance's worker output. Open errors are swallowed so they never break
// the frps child's stdio pipe.
type instanceLog struct {
	path string
	mu   sync.Mutex
	f    *os.File
}

func (w *instanceLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
			return len(p), nil
		}
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return len(p), nil
		}
		w.f = f
	}
	return w.f.Write(p)
}

func (w *instanceLog) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
}

// ensure io is referenced (instanceLog implements io.Writer).
var _ io.Writer = (*instanceLog)(nil)

// Sentinel errors. Map these to HTTP statuses in the API layer.
var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)
