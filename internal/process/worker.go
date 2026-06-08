// Package process supervises a long-running child process — typically
// the cloudflared connector binary — on behalf of a single instance.
//
// Responsibilities split with internal/manager: this package owns the
// os/exec lifecycle (Start, signal, Wait), while internal/manager owns
// the instance state machine (cfdstate.ConfigState transitions, log
// sinks, event bus notifications). The two communicate through a small
// Worker handle and a done channel.
//
// Compared to the previous frps re-exec-self model:
//   - no handshake protocol: the parent is no longer the same binary,
//     so there is no pre-allocated loopback port and no FRPS_WORKER_READY
//     line to parse;
//   - stdout AND stderr are both piped to the caller-provided io.Writer
//     (the per-instance log writer in internal/manager). cloudflared
//     writes its structured logs to stderr by default, but stdout is
//     also captured to forward any banner / fallback output;
//   - health is judged simply by "child process did not die within a
//     startup grace window". The richer three-tier check from spec §3.2
//     (alive + /ready=200 + readyConnections>=1) lands in PR-05/PR-07
//     once cfdbin and the metrics endpoint are wired up.
package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// SpawnParams configures a single Worker. Fields are immutable for the
// life of the Worker; reconfiguration means stop + new Spawn.
type SpawnParams struct {
	// BinaryPath is the absolute path of the executable to launch. If
	// empty the spawn fails fast; PR-05's cfdbin.Resolve is what fills
	// this in production.
	BinaryPath string

	// Args is the argv tail passed after BinaryPath. cloudflared callers
	// will typically pass {"tunnel", "--no-autoupdate", "run"} (plus an
	// optional "--label <value>" pair).
	Args []string

	// Env, if non-nil, is set as the child's complete environment
	// (replaces os.Environ — no implicit inheritance). Callers should
	// typically start with os.Environ() and append TUNNEL_TOKEN /
	// TUNNEL_METRICS / NO_AUTOUPDATE / TUNNEL_* overrides; cfdmgrd-
	// mandated values must be appended LAST so they win over any user
	// supplied AdvancedEnvOverrides. A nil Env means "inherit parent
	// env unchanged" (the default exec.Cmd behaviour).
	Env []string

	// LogSink receives both stdout and stderr line-buffered streams.
	// The supervisor never blocks on a slow sink; it relies on the
	// io.Writer's internal buffering / mutex.
	LogSink io.Writer

	// StartupGrace is how long Spawn waits before declaring the child
	// "successfully started". If the child exits during this window
	// Spawn returns an error and the caller sees no Worker. Defaults to
	// 2s when zero so unit tests stay snappy; real spawns should pass
	// ~5s to absorb cloudflared edge-handshake latency.
	StartupGrace time.Duration

	// StopGrace is how long Stop waits between the first SIGTERM and a
	// second SIGTERM (which cloudflared treats as "force shutdown"),
	// then 2s before sending SIGKILL. Zero defaults to 5s.
	StopGrace time.Duration
}

// Worker is the live handle to one supervised child process. Methods
// are safe to call from multiple goroutines.
type Worker struct {
	cmd  *exec.Cmd
	done chan struct{}

	mu        sync.Mutex
	exitErr   error
	stopGrace time.Duration
}

// ErrNoBinary is returned by Spawn when BinaryPath is empty.
var ErrNoBinary = errors.New("process: BinaryPath is empty")

// ErrChildExitedEarly is returned by Spawn when the child exits during
// the StartupGrace window.
var ErrChildExitedEarly = errors.New("process: child exited within startup grace")

// Spawn launches a child process and returns a Worker that owns its
// lifecycle. On failure the child (if any) is reaped before returning.
//
// Spawn DOES NOT block until the child is "fully ready" — there is no
// upstream definition of "ready" that holds across PR boundaries. The
// caller (internal/manager.instance) is responsible for any further
// health probing (e.g. polling /ready in PR-05+).
func Spawn(ctx context.Context, p SpawnParams) (*Worker, error) {
	if p.BinaryPath == "" {
		return nil, ErrNoBinary
	}
	startupGrace := p.StartupGrace
	if startupGrace == 0 {
		startupGrace = 2 * time.Second
	}
	stopGrace := p.StopGrace
	if stopGrace == 0 {
		stopGrace = 5 * time.Second
	}

	cmd := exec.CommandContext(ctx, p.BinaryPath, p.Args...)
	if len(p.Env) > 0 {
		cmd.Env = p.Env
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("process: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("process: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("process: start %s: %w", p.BinaryPath, err)
	}

	// Attach the freshly-spawned child to the cfdmgrd-process-wide Job
	// Object on Windows so a hard kill of the daemon (taskkill /F, IDE
	// debug stop, panic, BSOD) takes the child down with it. Failure is
	// non-fatal: the orphan scanner (orphan.go) catches anything that
	// slips through on next boot, and on non-Windows this is a no-op.
	_ = attachToParentJob(cmd.Process.Pid)

	w := &Worker{
		cmd:       cmd,
		done:      make(chan struct{}),
		stopGrace: stopGrace,
	}

	// Forward stdout + stderr to the sink. nil sink → discard.
	//
	// StdoutPipe/StderrPipe contract: cmd.Wait closes the pipes as soon as it
	// sees the process exit, so reading MUST complete before Wait — otherwise
	// the child's final log lines on exit get truncated. We therefore drain
	// both copies (they return on pipe EOF, i.e. when the child exits) and
	// only THEN call cmd.Wait.
	//
	// The two io.Copy goroutines share one sink; wrap it in a lockedWriter so
	// concurrent Writes are serialised regardless of whether the caller-
	// provided sink is itself thread-safe.
	var sink io.Writer
	if p.LogSink != nil {
		sink = &lockedWriter{w: p.LogSink}
	} else {
		sink = io.Discard
	}
	var copyWG sync.WaitGroup
	copyWG.Add(2)
	go func() { defer copyWG.Done(); _, _ = io.Copy(sink, stdout) }()
	go func() { defer copyWG.Done(); _, _ = io.Copy(sink, stderr) }()

	// Reaper: sole owner of cmd.Wait(). Drains output, then reaps, then closes
	// done so the manager's exit watcher only fires once all logs are captured.
	go func() {
		copyWG.Wait()
		err := cmd.Wait()
		w.mu.Lock()
		w.exitErr = err
		w.mu.Unlock()
		close(w.done)
	}()

	// Startup grace: if child dies within this window, treat the spawn
	// as failed and surface the exit error.
	select {
	case <-w.done:
		exitErr := w.ExitErr()
		if exitErr == nil {
			exitErr = ErrChildExitedEarly
		}
		return nil, fmt.Errorf("process: %w: %v", ErrChildExitedEarly, exitErr)
	case <-time.After(startupGrace):
		// passed grace; child is still alive
	}

	return w, nil
}

// PID returns the child's OS process id, or 0 if it has already exited
// and been reaped.
func (w *Worker) PID() int {
	if w.cmd.Process == nil {
		return 0
	}
	return w.cmd.Process.Pid
}

// Cmd exposes the underlying *exec.Cmd for advanced callers (e.g. tests
// inspecting Args/Env). Mutating it after Spawn returns is unsafe.
func (w *Worker) Cmd() *exec.Cmd { return w.cmd }

// Done returns a channel that closes when the child has exited and the
// reaper has called cmd.Wait().
func (w *Worker) Done() <-chan struct{} { return w.done }

// ExitErr returns the child's exit error after Done closes, or nil if
// the child has not yet exited.
func (w *Worker) ExitErr() error {
	select {
	case <-w.done:
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.exitErr
	default:
		return nil
	}
}

// lockedWriter serialises Write calls onto an inner io.Writer. Spawn wraps
// the caller-provided sink with this so the stdout + stderr forwarder
// goroutines cannot interleave a single Write — even when the sink is a
// plain *bytes.Buffer (as it is in tests).
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// Stop terminates the child gracefully. It sends SIGTERM (or platform
// equivalent), waits up to stopGrace, sends a second SIGTERM (which
// cloudflared interprets as "force"), waits up to 2s, then SIGKILLs as
// a last resort. Returns once the child has been reaped.
func (w *Worker) Stop() error {
	if w.cmd.Process == nil {
		return nil
	}
	_ = signalTerminate(w.cmd.Process)
	select {
	case <-w.done:
		return nil
	case <-time.After(w.stopGrace):
	}
	// Second-shot signal: cloudflared upgrades this to "force shutdown".
	_ = signalTerminate(w.cmd.Process)
	select {
	case <-w.done:
		return nil
	case <-time.After(2 * time.Second):
	}
	_ = w.cmd.Process.Kill()
	<-w.done
	return nil
}
