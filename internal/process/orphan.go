// Package process — orphan reaping.
//
// When cfdmgrd exits abnormally (panic, taskkill /F, OS bluescreen, IDE
// debug stop on Windows), the cloudflared child processes it spawned can
// outlive the parent and keep their Cloudflare-edge connectors alive.
// From the dashboard's perspective the tunnel then accumulates ghost
// connectors until their heartbeats time out — and a fresh cfdmgrd run
// happily spawns yet another one on top of the leftovers.
//
// This file implements a one-shot scan-and-kill executed early in daemon
// startup. It enumerates the OS process table, picks processes whose
// executable path is a descendant of our cfdbin store root (so we never
// touch a user's manually-installed cloudflared), and terminates them.
//
// Defense-in-depth: on Windows a Job Object (see job_windows.go) handles
// the *current* daemon's lifetime guarantee. The orphan scan catches the
// *previous* daemon's leftovers, plus any edge case the Job missed
// (nested-job rejection on legacy Windows, breakaway-job tricks, etc.).
//
// Known caveats:
//   - On Windows, non-admin cfdmgrd cannot kill children spawned by a
//     different user; such kill attempts log a warning and are skipped.
//     For a clean orphan reap the daemon should run with at least the
//     same identity as the previous instance.
//   - macOS HFS+ stores filenames in NFD; if cfdbin store paths contain
//     non-ASCII characters with multiple normalisation forms, the
//     prefix match may need explicit Unicode normalisation. Default
//     deployments use ASCII paths so this is dormant.
package process

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	psprocess "github.com/shirou/gopsutil/v4/process"
)

// orphanMatch is the pure decision function: given a candidate process's
// metadata, does it look like a leftover of a previous cfdmgrd run?
//
//   - pid <= 0 or pid == selfPID: never (don't terminate ourselves).
//   - binariesRoot empty / whitespace: never (safety — refuse to scan
//     with no scope; an empty prefix matches everything).
//   - exe empty: never (couldn't resolve; conservatively skip).
//   - otherwise: exe must be a descendant of binariesRoot. Path
//     comparison is case-insensitive on Windows because NTFS is.
//
// Both paths are filepath.Clean'd; the root gets a trailing separator so
// "/foo" cannot prefix-match "/foobar".
//
// Symlink-resolution and isSafeRoot are NOT performed here — that's the
// caller's job (see ScanAndKillOrphans). Keeping orphanMatch a pure
// string-level predicate makes it cheap, deterministic, and unit-testable.
func orphanMatch(pid, selfPID int, exe, binariesRoot string) bool {
	if pid <= 0 || pid == selfPID {
		return false
	}
	if strings.TrimSpace(binariesRoot) == "" || strings.TrimSpace(exe) == "" {
		return false
	}
	rootAbs, err := filepath.Abs(binariesRoot)
	if err != nil {
		return false
	}
	exeAbs, err := filepath.Abs(exe)
	if err != nil {
		return false
	}
	root := filepath.Clean(rootAbs)
	got := filepath.Clean(exeAbs)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		got = strings.ToLower(got)
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(root, sep) {
		root += sep
	}
	return strings.HasPrefix(got, root)
}

// isSafeRoot guards against catastrophic misconfiguration. The orphan
// scanner WILL TerminateProcess every "cloudflared(.exe)" process whose
// executable path sits under root; a root of "/" or "C:\\" would
// therefore kill every cloudflared on the box, including the user's
// hand-installed one. We refuse to scan when root is suspiciously broad.
//
// Rules:
//   - Reject empty / whitespace.
//   - On Windows: reject drive-only roots ("X:", "X:\\") and UNC roots
//     with fewer than 2 components past the share ("\\server\share").
//   - Require at least 2 non-trivial path components below the
//     filesystem root. Default deployment is "$DataDir/bin/cloudflared"
//     (depth ≥ 3); the minimum-of-2 rule keeps us out of "/usr/bin",
//     "/var", "C:\\Windows" and similar.
func isSafeRoot(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	clean := filepath.Clean(root)
	if runtime.GOOS == "windows" {
		// "C:" / "C:\\" / "C:/"
		if len(clean) <= 3 && (strings.HasSuffix(clean, `:\`) || strings.HasSuffix(clean, `:/`) || strings.HasSuffix(clean, `:`)) {
			return false
		}
		if strings.HasPrefix(clean, `\\`) {
			parts := strings.Split(strings.TrimLeft(clean, `\`), `\`)
			meaningful := 0
			for _, p := range parts {
				if p != "" {
					meaningful++
				}
			}
			if meaningful < 3 {
				return false
			}
		}
	} else {
		if clean == "/" || clean == "" || clean == "." {
			return false
		}
	}
	// On Windows the "C:" drive prefix is not a real directory segment
	// — strip it before counting depth, otherwise "C:\\Windows" reads
	// as depth 2 (drive + one directory) and would pass the guard.
	if runtime.GOOS == "windows" && len(clean) >= 2 && clean[1] == ':' {
		clean = clean[2:]
	}
	sep := string(filepath.Separator)
	trimmed := strings.Trim(clean, sep)
	parts := strings.Split(trimmed, sep)
	meaningful := 0
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			continue
		}
		meaningful++
	}
	return meaningful >= 2
}

// resolveSymlinks returns the canonical form of p, falling back to the
// lexical input if EvalSymlinks fails (broken link, permission denied,
// path doesn't exist yet, etc.). The orphan-detection prefix match has
// to use the same canonical form on BOTH sides — gopsutil's Exe() on
// Linux returns the kernel-resolved /proc/PID/exe symlink target, so
// the configured binariesRoot must also be resolved or we get false
// negatives (legitimate orphans not killed) when an admin sets
// CFDM_DATA_DIR to a symlinked mount point.
func resolveSymlinks(p string) string {
	if p == "" {
		return p
	}
	if r, err := filepath.EvalSymlinks(p); err == nil && r != "" {
		return r
	}
	return p
}

// looksLikeCloudflared narrows the candidate set BEFORE the (relatively
// expensive) Exe() syscall. The cfdbin store always writes the binary as
// "cloudflared" / "cloudflared.exe", so anything else under the store
// root is not a connector we own.
func looksLikeCloudflared(procName string) bool {
	n := strings.ToLower(procName)
	return n == "cloudflared" || n == "cloudflared.exe"
}

// ScanAndKillOrphans enumerates every process on the host and SIGKILLs
// (TerminateProcess on Windows) any that look like leftover cloudflared
// children of a previous cfdmgrd run — identified by an executable path
// rooted inside binariesRoot.
//
// Safety contract:
//   - binariesRoot must pass isSafeRoot (at least 2 components below
//     the filesystem root). A drive root / "/" / "C:\\" is REFUSED with
//     a logged warning so a misconfigured CFDM_BINARIES_DIR can never
//     mass-terminate processes outside our store.
//   - Both binariesRoot and each candidate's Exe() are resolved through
//     filepath.EvalSymlinks before comparison so a symlinked store root
//     still matches the kernel-canonical exe paths reported by gopsutil.
//   - Killed processes are polled briefly so we don't return while the
//     OS still considers the PID alive — this prevents the very next
//     Spawn from re-using a PID that's about to be recycled.
//
// Errors enumerating individual processes (permission denied, race with
// exit) are swallowed; only the top-level enumerator failure surfaces.
// Returns the list of PIDs we attempted to terminate, for logging /
// observability.
//
// Safe to call before InitParentJob; safe to call when the store dir is
// empty (no-op).
func ScanAndKillOrphans(binariesRoot string, logger *slog.Logger) ([]int, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if binariesRoot == "" {
		return nil, nil
	}
	if !isSafeRoot(binariesRoot) {
		logger.Warn("orphan: refusing to scan — binariesRoot too broad (would risk killing unrelated processes)",
			slog.String("root", binariesRoot),
		)
		return nil, nil
	}
	rootAbs, err := filepath.Abs(binariesRoot)
	if err != nil {
		return nil, fmt.Errorf("orphan: resolve binariesRoot: %w", err)
	}
	if _, statErr := os.Stat(rootAbs); statErr != nil {
		// No store dir yet → nothing could possibly be an orphan.
		return nil, nil
	}
	rootCanonical := resolveSymlinks(rootAbs)

	procs, err := psprocess.Processes()
	if err != nil {
		return nil, fmt.Errorf("orphan: enumerate processes: %w", err)
	}
	selfPID := os.Getpid()

	killed := make([]int, 0)
	killedProcs := make([]*psprocess.Process, 0)
	for _, p := range procs {
		pid := int(p.Pid)
		if pid <= 0 || pid == selfPID {
			continue
		}
		name, nerr := p.Name()
		if nerr != nil || !looksLikeCloudflared(name) {
			continue
		}
		exe, eerr := p.Exe()
		if eerr != nil || exe == "" {
			// Permission denied or process already exited. Skip rather
			// than risk killing the wrong thing on a guess.
			continue
		}
		exeCanonical := resolveSymlinks(exe)
		if !orphanMatch(pid, selfPID, exeCanonical, rootCanonical) {
			continue
		}
		if kerr := p.Kill(); kerr != nil {
			logger.Warn("orphan: kill failed",
				slog.Int("pid", pid),
				slog.String("exe", exe),
				slog.Any("err", kerr),
			)
			continue
		}
		logger.Info("orphan: terminated leftover cloudflared",
			slog.Int("pid", pid),
			slog.String("exe", exe),
		)
		killed = append(killed, pid)
		killedProcs = append(killedProcs, p)
	}

	// Briefly wait for the kernel to actually retire each killed PID
	// before returning. Without this, the OS could recycle a PID and
	// hand it to our very next Spawn, leading to nonsensical "child is
	// already orphaned" misdiagnoses if anyone re-scans during boot.
	// Capped poll loop — never block startup for an unkillable zombie.
	waitForKilledExit(killedProcs, 2*time.Second)

	return killed, nil
}

// waitForKilledExit polls each process until IsRunning() returns false
// or the budget is exhausted. Per-iteration cost is one syscall per
// still-alive PID, so this is bounded and cheap (typical orphan count
// is single-digit and they die in milliseconds after TerminateProcess).
func waitForKilledExit(ps []*psprocess.Process, budget time.Duration) {
	if len(ps) == 0 {
		return
	}
	deadline := time.Now().Add(budget)
	for {
		anyAlive := false
		for _, p := range ps {
			alive, err := p.IsRunning()
			if err == nil && alive {
				anyAlive = true
				break
			}
		}
		if !anyAlive || time.Now().After(deadline) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
