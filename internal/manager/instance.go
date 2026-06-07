package manager

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/mia-clark/cloudflared-manager/internal/cfdbin"
	"github.com/mia-clark/cloudflared-manager/internal/eventbus"
	"github.com/mia-clark/cloudflared-manager/internal/logtail"
	"github.com/mia-clark/cloudflared-manager/internal/process"
	"github.com/mia-clark/cloudflared-manager/pkg/cfdconfig"
	"github.com/mia-clark/cloudflared-manager/pkg/cfdflags"
	"github.com/mia-clark/cloudflared-manager/pkg/cfdstate"
	"github.com/mia-clark/cloudflared-manager/pkg/util"
)

// instance owns a single cloudflared connector lifecycle. Each running
// instance lives in its own external process supervised by
// internal/process.Worker — there is no longer any re-exec-self magic.
type instance struct {
	id   string
	path string

	// opMu serializes the whole body of start/stop/reload for this
	// instance so a stop arriving during another op's startup-grace
	// window can never interleave and orphan the child process.
	opMu sync.Mutex

	mu      sync.RWMutex
	state   cfdstate.ConfigState
	lastErr string
	startAt time.Time
	stopAt  time.Time

	// run-time fields (zero unless running)
	w      *process.Worker
	cancel context.CancelFunc

	logger   *slog.Logger
	bus      *eventbus.Bus
	logSink  io.Writer
	binStore *cfdbin.Store // may be nil; start() falls back to PATH "cloudflared"

	// metricsPort is stable for the lifetime of the instance: computed
	// once from a CRC32 hash of the id so the same id always maps to the
	// same local port in range [20241, 20998].
	metricsPort int
	// tailer captures the child process's combined stdout+stderr output,
	// parses structured JSON log lines, and fans them out to subscribers.
	tailer *logtail.ProcessTailer
}

func newInstance(id, path string, logger *slog.Logger, bus *eventbus.Bus, logSink io.Writer, binStore *cfdbin.Store) *instance {
	return &instance{
		id:          id,
		path:        path,
		state:       cfdstate.ConfigStateStopped,
		logger:      logger.With(slog.String("config_id", id)),
		bus:         bus,
		logSink:     logSink,
		binStore:    binStore,
		metricsPort: allocMetricsPort(id),
		tailer:      logtail.NewProcessTailer(id, 0),
	}
}

// allocMetricsPort maps an instance id to a stable local port in the range
// [20241, 20998] using a CRC32 hash so the same id always resolves to the
// same port across daemon restarts.
func allocMetricsPort(id string) int {
	return int(crc32.ChecksumIEEE([]byte(id)))%758 + 20241
}

// freeMetricsPort returns preferred if it is currently bindable on the
// loopback interface, otherwise linearly probes the [20241,20998] range for
// the first free port. When the whole range is busy it returns preferred and
// lets cloudflared surface the bind error. Probing keeps the allocation
// deterministic (same id → same port) in the common no-collision case.
func freeMetricsPort(preferred int) int {
	const lo, hi = 20241, 20998
	bindable := func(p int) bool {
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			return false
		}
		_ = ln.Close()
		return true
	}
	if preferred >= lo && preferred <= hi && bindable(preferred) {
		return preferred
	}
	for p := lo; p <= hi; p++ {
		if bindable(p) {
			return p
		}
	}
	return preferred
}

// MetricsAddr returns the "127.0.0.1:<port>" string for this instance's
// cloudflared --metrics endpoint.
func (i *instance) MetricsAddr() string {
	return "127.0.0.1:" + strconv.Itoa(i.metricsPort)
}

// Tailer exposes the ProcessTailer so the API logs handler can subscribe
// to live structured log output from the child process.
func (i *instance) Tailer() *logtail.ProcessTailer { return i.tailer }

// ID returns the immutable config id (file stem).
func (i *instance) ID() string { return i.id }

// Path returns the absolute path of the underlying config file.
func (i *instance) Path() string { return i.path }

// Snapshot describes the run-time status of one instance.
type Snapshot struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Path          string     `json:"path"` // config 文件路径（PR-08 后为 .yaml）
	LogPath       string     `json:"log_path"`
	State         string     `json:"state"`
	LastError     string     `json:"last_error,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	StoppedAt     *time.Time `json:"stopped_at,omitempty"`
	BinaryVersion string     `json:"binary_version,omitempty"` // 该实例当前使用的 cloudflared 版本
	PID           int        `json:"pid,omitempty"`            // 子进程 pid，0 表示未运行
	MetricsPort   int        `json:"metrics_port,omitempty"`   // 分配给该实例的 metrics 端口
}

// Snapshot returns a JSON-friendly status view. Name / LogPath are
// injected by the Manager from meta.json + LogsDir respectively.
func (i *instance) Snapshot() Snapshot {
	i.mu.RLock()
	defer i.mu.RUnlock()
	s := Snapshot{
		ID:          i.id,
		Path:        i.path,
		State:       stateString(i.state),
		LastError:   i.lastErr,
		MetricsPort: i.metricsPort,
	}
	if !i.startAt.IsZero() {
		t := i.startAt
		s.StartedAt = &t
	}
	if !i.stopAt.IsZero() {
		t := i.stopAt
		s.StoppedAt = &t
	}
	if i.w != nil {
		s.PID = i.w.PID()
	}
	return s
}

// State returns the current lifecycle state.
func (i *instance) State() cfdstate.ConfigState {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.state
}

// setState assigns a new state under lock and returns whether it changed.
func (i *instance) setState(s cfdstate.ConfigState) bool {
	i.mu.Lock()
	prev := i.state
	if i.state == s {
		i.mu.Unlock()
		return false
	}
	i.state = s
	switch s {
	case cfdstate.ConfigStateStarted:
		i.startAt = time.Now()
	case cfdstate.ConfigStateStopped:
		i.stopAt = time.Now()
	}
	i.mu.Unlock()
	if i.bus != nil {
		i.bus.Publish(eventbus.TypeInstanceState, i.id, eventbus.InstanceStateData{
			State:     stateString(s),
			PrevState: stateString(prev),
		})
	}
	return true
}

// start spawns the cloudflared subprocess. It reads the instance's YAML
// config, validates it, projects the fields onto TUNNEL_* env vars via
// cfdflags, and injects the mandatory cfdmgrd-owned env last so they
// always win. The ProcessTailer is wired as an additional log sink so
// structured log lines are available to the API /logs handler.
func (i *instance) start(ctx context.Context) error {
	i.opMu.Lock()
	defer i.opMu.Unlock()
	return i.startLocked(ctx)
}

// startLocked performs the actual spawn. Callers MUST hold i.opMu so a
// concurrent stop cannot interleave during the startup-grace window.
func (i *instance) startLocked(ctx context.Context) error {
	i.mu.Lock()
	if i.state == cfdstate.ConfigStateStarted || i.state == cfdstate.ConfigStateStarting {
		i.mu.Unlock()
		return errors.New("already running")
	}
	i.state = cfdstate.ConfigStateStarting
	i.lastErr = ""
	// Re-validate the metrics port is free at start time: the CRC32 allocator
	// only spans 758 ports so two ids can collide, and a leftover listener
	// from a crashed run could still hold it. A taken port would make
	// cloudflared fail to bind --metrics and the sampler scrape blind.
	i.metricsPort = freeMetricsPort(i.metricsPort)
	i.mu.Unlock()

	// 1. Read + parse + validate the YAML config.
	raw, err := os.ReadFile(i.path)
	if err != nil {
		i.recordError(err)
		i.setState(cfdstate.ConfigStateStopped)
		return err
	}
	cfg, err := cfdconfig.ParseYAML(raw)
	if err != nil {
		i.recordError(err)
		i.setState(cfdstate.ConfigStateStopped)
		return err
	}
	if err := cfg.Validate(); err != nil {
		i.recordError(err)
		i.setState(cfdstate.ConfigStateStopped)
		return err
	}
	if cfg.Token == "" {
		err := errors.New("token is required to start cloudflared")
		i.recordError(err)
		i.setState(cfdstate.ConfigStateStopped)
		return err
	}

	// 2. Project config fields → cfdflags.Options → TUNNEL_* env map.
	opts := cfdflags.Options{
		Protocol:             cfg.Edge.Protocol,
		EdgeIPVersion:        cfg.Edge.EdgeIPVersion,
		EdgeBindAddress:      cfg.Edge.EdgeBindAddress,
		Region:               cfg.Edge.Region,
		PostQuantum:          cfg.Edge.PostQuantum,
		Retries:              cfg.Reliability.Retries,
		GracePeriod:          cfg.Reliability.GracePeriod,
		LogLevel:             cfg.Logging.LogLevel,
		TransportLogLevel:    cfg.Logging.TransportLogLevel,
		Tags:                 cfg.Identity.Tags,
		Label:                cfg.Identity.Label,
		AdvancedEnvOverrides: cfg.AdvancedEnvOverrides,
	}
	userEnv := cfdflags.ToTunnelEnv(opts)

	// 3. Resolve binary path: cfdbin store first, then PATH fallback.
	binPath := "cloudflared"
	if i.binStore != nil {
		if p, berr := i.binStore.Resolve(cfg.BinaryVersion); berr == nil {
			binPath = p
		}
	}

	// 4. Build the child env: os.Environ() base + user TUNNEL_* vars +
	//    cfdmgrd-mandated vars (appended last so they always win).
	env := append([]string{}, os.Environ()...)
	for k, v := range userEnv {
		env = append(env, k+"="+v)
	}
	env = append(env,
		"TUNNEL_TOKEN="+cfg.Token,
		"NO_AUTOUPDATE=true",
		"AUTOUPDATE_FREQ=87600h",
		"TUNNEL_METRICS="+i.MetricsAddr(),
		"TUNNEL_OUTPUT=json",
	)

	// 5. Build argv: "tunnel --no-autoupdate [--label <l>] run"
	args := []string{"tunnel", "--no-autoupdate"}
	args = append(args, cfdflags.LabelArgv(cfg.Identity.Label)...)
	args = append(args, "run")

	// 6. Log sink: file sink + tailer (via io.MultiWriter so both get output).
	sink := io.MultiWriter(i.logSink, i.tailer)

	runCtx, cancel := context.WithCancel(ctx)
	w, err := process.Spawn(runCtx, process.SpawnParams{
		BinaryPath:   binPath,
		Args:         args,
		Env:          env,
		LogSink:      sink,
		StartupGrace: 5 * time.Second,
		StopGrace:    5 * time.Second,
	})
	if err != nil {
		cancel()
		i.recordError(err)
		i.setState(cfdstate.ConfigStateStopped)
		return fmt.Errorf("spawn cloudflared: %w", err)
	}
	i.mu.Lock()
	i.w = w
	i.cancel = cancel
	i.mu.Unlock()

	// 7. Exit watcher: notify tailer on exit, update state.
	go func() {
		<-w.Done()
		i.tailer.OnExit(w.Cmd().ProcessState)
		i.mu.Lock()
		stopping := i.state == cfdstate.ConfigStateStopping
		i.w = nil
		i.cancel = nil
		i.mu.Unlock()
		cancel()
		if !stopping {
			if exitErr := w.ExitErr(); exitErr != nil {
				i.recordError(fmt.Errorf("cloudflared exited: %w", exitErr))
			}
			i.setState(cfdstate.ConfigStateStopped)
			i.logger.Info("cloudflared exited", slog.Int("pid", w.PID()))
		}
	}()

	i.setState(cfdstate.ConfigStateStarted)
	i.logger.Info("cloudflared instance started",
		slog.Int("pid", w.PID()),
		slog.String("metrics", i.MetricsAddr()),
	)
	return nil
}

// stop terminates the child process and waits for it to be reaped.
func (i *instance) stop() error {
	i.opMu.Lock()
	defer i.opMu.Unlock()
	return i.stopLocked()
}

// stopLocked performs the actual teardown. Callers MUST hold i.opMu.
func (i *instance) stopLocked() error {
	i.mu.Lock()
	if i.state == cfdstate.ConfigStateStopped || i.state == cfdstate.ConfigStateStopping {
		i.mu.Unlock()
		return nil
	}
	i.state = cfdstate.ConfigStateStopping
	cancel := i.cancel
	w := i.w
	i.mu.Unlock()

	if w != nil {
		_ = w.Stop()
	}
	if cancel != nil {
		cancel()
	}
	i.mu.Lock()
	i.w = nil
	i.cancel = nil
	i.mu.Unlock()
	i.setState(cfdstate.ConfigStateStopped)
	i.logger.Info("cloudflared instance stopped")
	return nil
}

// reload = stop + start. cloudflared has no in-place reload for
// per-connector settings; restart is the only correct path.
func (i *instance) reload(ctx context.Context) error {
	i.opMu.Lock()
	defer i.opMu.Unlock()
	if err := i.stopLocked(); err != nil {
		return err
	}
	return i.startLocked(ctx)
}

func (i *instance) recordError(err error) {
	if err == nil {
		return
	}
	i.mu.Lock()
	i.lastErr = err.Error()
	i.mu.Unlock()
	i.logger.Warn("instance error", slog.Any("err", err))
	if i.bus != nil {
		i.bus.Publish(eventbus.TypeInstanceError, i.id, eventbus.InstanceErrorData{Message: err.Error()})
	}
}

// idFromPath derives a config id from a file path (file stem).
func idFromPath(path string) string {
	return util.FileNameWithoutExt(filepath.Base(path))
}

func stateString(s cfdstate.ConfigState) string {
	switch s {
	case cfdstate.ConfigStateStarted:
		return "started"
	case cfdstate.ConfigStateStopped:
		return "stopped"
	case cfdstate.ConfigStateStarting:
		return "starting"
	case cfdstate.ConfigStateStopping:
		return "stopping"
	default:
		return "unknown"
	}
}
