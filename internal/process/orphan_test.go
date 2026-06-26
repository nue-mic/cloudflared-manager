package process_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/nue-mic/cloudflared-manager/internal/process"
)

// TestMain doubles as a controllable "sleeper" subprocess: when invoked
// with env CFDM_TEST_SLEEPER set to a positive integer, the test binary
// just sleeps that many seconds and exits. This is used by the orphan
// integration test below — we copy the test binary itself into a fake
// store root under the name cloudflared(.exe) so it satisfies
// looksLikeCloudflared, then spawn it as the orphan we expect to reap.
func TestMain(m *testing.M) {
	if s := os.Getenv("CFDM_TEST_SLEEPER"); s != "" {
		d, _ := strconv.Atoi(s)
		if d <= 0 {
			d = 30
		}
		time.Sleep(time.Duration(d) * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// rootAndExe builds an (binariesRoot, exe) pair that is platform-correct
// for the host: forward-slash POSIX paths on Linux/Mac, drive-letter
// backslash paths on Windows.
func rootAndExe(under bool) (root, exe string) {
	if runtime.GOOS == "windows" {
		root = `C:\data\bin\cloudflared`
		if under {
			exe = `C:\data\bin\cloudflared\2026.5.2\cloudflared.exe`
		} else {
			exe = `C:\Program Files\cloudflared\cloudflared.exe`
		}
		return
	}
	root = "/var/lib/cfdmgrd/bin/cloudflared"
	if under {
		exe = "/var/lib/cfdmgrd/bin/cloudflared/2026.5.2/cloudflared"
	} else {
		exe = "/usr/local/bin/cloudflared"
	}
	return
}

func TestOrphanMatch_ExeUnderRoot_Matches(t *testing.T) {
	root, exe := rootAndExe(true)
	if !process.OrphanMatchForTest(1234, 9999, exe, root) {
		t.Fatalf("expected match: root=%q exe=%q", root, exe)
	}
}

func TestOrphanMatch_ExeOutsideRoot_NoMatch(t *testing.T) {
	root, exe := rootAndExe(false)
	if process.OrphanMatchForTest(1234, 9999, exe, root) {
		t.Fatalf("expected no-match: root=%q exe=%q", root, exe)
	}
}

func TestOrphanMatch_SelfPID_NeverMatches(t *testing.T) {
	root, exe := rootAndExe(true)
	if process.OrphanMatchForTest(42, 42, exe, root) {
		t.Fatalf("self-PID must never be flagged orphan")
	}
}

func TestOrphanMatch_NonPositivePID_NeverMatches(t *testing.T) {
	root, exe := rootAndExe(true)
	if process.OrphanMatchForTest(0, 9999, exe, root) {
		t.Fatalf("pid 0 must never be flagged orphan")
	}
	if process.OrphanMatchForTest(-1, 9999, exe, root) {
		t.Fatalf("negative pid must never be flagged orphan")
	}
}

func TestOrphanMatch_EmptyRoot_NeverMatches(t *testing.T) {
	// Safety: when callers forget to configure binariesRoot, the scanner
	// must refuse to kill anything. Otherwise an empty prefix matches
	// every process on the system.
	_, exe := rootAndExe(true)
	if process.OrphanMatchForTest(1234, 9999, exe, "") {
		t.Fatalf("empty binariesRoot must never match")
	}
}

func TestOrphanMatch_EmptyExe_NeverMatches(t *testing.T) {
	root, _ := rootAndExe(true)
	if process.OrphanMatchForTest(1234, 9999, "", root) {
		t.Fatalf("empty exe must never match (gopsutil Exe() permission denied)")
	}
}

func TestOrphanMatch_SiblingNamedPrefix_NoMatch(t *testing.T) {
	// /var/lib/cfdmgrd/bin/cloudflared vs.
	// /var/lib/cfdmgrd/bin/cloudflared-something-else/...
	// must NOT match — the trailing-separator guard catches this.
	if runtime.GOOS == "windows" {
		root := `C:\data\bin\cloudflared`
		exe := `C:\data\bin\cloudflared-old\cloudflared.exe`
		if process.OrphanMatchForTest(1234, 9999, exe, root) {
			t.Fatalf("sibling prefix must not match: %q under %q", exe, root)
		}
		return
	}
	root := "/var/lib/cfdmgrd/bin/cloudflared"
	exe := "/var/lib/cfdmgrd/bin/cloudflared-old/cloudflared"
	if process.OrphanMatchForTest(1234, 9999, exe, root) {
		t.Fatalf("sibling prefix must not match: %q under %q", exe, root)
	}
}

func TestOrphanMatch_RelativeRoot_AbsolutizesAndMatches(t *testing.T) {
	// Real callers always pass an absolute path, but we should not crash
	// if a relative path slips through; filepath.Abs resolves it against
	// cwd. We don't assert match here (cwd is host-dependent) — only
	// that the function returns a bool and doesn't panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("orphanMatch panicked on relative root: %v", r)
		}
	}()
	_ = process.OrphanMatchForTest(1234, 9999, "cloudflared", "./bin/cloudflared")
}

func TestOrphanMatch_WindowsCaseInsensitive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive path semantics are Windows-specific")
	}
	root := `D:\Github_Codes\Cloudflared-Manager\tmp\data\bin\cloudflared`
	exe := `d:\github_codes\cloudflared-manager\TMP\DATA\BIN\CLOUDFLARED\2026.5.2\cloudflared.exe`
	if !process.OrphanMatchForTest(1234, 9999, exe, root) {
		t.Fatalf("windows match must be case-insensitive: %q under %q", exe, root)
	}
}

// TestScanAndKillOrphans_EmptyRoot is a smoke test for the public entry:
// passing "" must short-circuit without errors and without enumerating
// processes (defense against a misconfigured caller).
func TestScanAndKillOrphans_EmptyRoot(t *testing.T) {
	killed, err := process.ScanAndKillOrphans("", nil)
	if err != nil {
		t.Fatalf("unexpected error with empty root: %v", err)
	}
	if len(killed) != 0 {
		t.Fatalf("must not kill anything with empty root, got %v", killed)
	}
}

// TestScanAndKillOrphans_NonExistentRoot must also short-circuit; an
// uninitialized cfdbin store on first daemon boot is a normal state.
func TestScanAndKillOrphans_NonExistentRoot(t *testing.T) {
	tmp := t.TempDir()
	bogus := filepath.Join(tmp, "does", "not", "exist")
	killed, err := process.ScanAndKillOrphans(bogus, nil)
	if err != nil {
		t.Fatalf("unexpected error with non-existent root: %v", err)
	}
	if len(killed) != 0 {
		t.Fatalf("must not kill anything with non-existent root, got %v", killed)
	}
}

func TestOrphanMatch_WhitespaceOnlyRoot_NeverMatches(t *testing.T) {
	// This tests the vulnerability claim: whitespace-only binariesRoot
	// should be treated like an empty string and refuse to match.
	_, exe := rootAndExe(true)
	if process.OrphanMatchForTest(1234, 9999, exe, "   ") {
		t.Fatalf("whitespace-only binariesRoot must never match")
	}
	if process.OrphanMatchForTest(1234, 9999, exe, "\t\n  \r") {
		t.Fatalf("various whitespace-only binariesRoot must never match")
	}
}

// ---------------------------------------------------------------------
// isSafeRoot tests — the over-broad-root guard.

func TestIsSafeRoot_RejectsEmptyAndWhitespace(t *testing.T) {
	for _, r := range []string{"", "   ", "\t\n"} {
		if process.IsSafeRootForTest(r) {
			t.Errorf("isSafeRoot(%q) must be false", r)
		}
	}
}

func TestIsSafeRoot_RejectsFilesystemRoots(t *testing.T) {
	var cases []string
	if runtime.GOOS == "windows" {
		cases = []string{`C:\`, `D:\`, `c:/`, `C:`}
	} else {
		cases = []string{"/"}
	}
	for _, r := range cases {
		if process.IsSafeRootForTest(r) {
			t.Errorf("isSafeRoot(%q) must reject filesystem-root", r)
		}
	}
}

func TestIsSafeRoot_RejectsSingleComponentBelowRoot(t *testing.T) {
	var cases []string
	if runtime.GOOS == "windows" {
		cases = []string{`C:\Windows`, `D:\foo`}
	} else {
		cases = []string{"/usr", "/var", "/home"}
	}
	for _, r := range cases {
		if process.IsSafeRootForTest(r) {
			t.Errorf("isSafeRoot(%q) must reject (only 1 component below root)", r)
		}
	}
}

func TestIsSafeRoot_AcceptsRealisticStoreRoots(t *testing.T) {
	var cases []string
	if runtime.GOOS == "windows" {
		cases = []string{
			`C:\ProgramData\cfdmgrd\bin\cloudflared`,
			`D:\data\bin\cloudflared`,
		}
	} else {
		cases = []string{
			"/var/lib/cfdmgrd/bin/cloudflared",
			"/opt/cfdmgrd/bin/cloudflared",
		}
	}
	for _, r := range cases {
		if !process.IsSafeRootForTest(r) {
			t.Errorf("isSafeRoot(%q) must accept (realistic deployment path)", r)
		}
	}
}

func TestIsSafeRoot_WindowsUNCNeedsShareAndComponent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC paths are Windows-only")
	}
	// \\server alone or \\server\share alone is too broad.
	for _, r := range []string{`\\server`, `\\server\share`} {
		if process.IsSafeRootForTest(r) {
			t.Errorf("isSafeRoot(%q) must reject (UNC needs at least share + 1 component)", r)
		}
	}
	if !process.IsSafeRootForTest(`\\server\share\bin\cloudflared`) {
		t.Errorf("isSafeRoot must accept fully-qualified UNC path")
	}
}

func TestScanAndKillOrphans_RefusesOverBroadRoot(t *testing.T) {
	// The function must short-circuit with no kills (and no error)
	// when the configured root would risk killing system processes.
	r := "/"
	if runtime.GOOS == "windows" {
		r = `C:\`
	}
	killed, err := process.ScanAndKillOrphans(r, nil)
	if err != nil {
		t.Fatalf("expected no error refusing over-broad root, got %v", err)
	}
	if len(killed) != 0 {
		t.Fatalf("must not kill anything for over-broad root, got %v", killed)
	}
}

// ---------------------------------------------------------------------
// Integration test: real process enumeration + real kill.
//
// Strategy: copy the test binary itself into a fake store root under the
// canonical name "cloudflared"/"cloudflared.exe" so it passes
// looksLikeCloudflared, set CFDM_TEST_SLEEPER so the copy idles, then
// invoke ScanAndKillOrphans and assert the spawned PID was reaped.

// copyExecutable copies src to dst preserving the +x bit. dst's parent
// directory must already exist.
func copyExecutable(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("create dst: %v", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copy: %v", err)
	}
}

// fakeCloudflaredName mirrors the cfdbin store naming convention. The
// orphan scanner looks at process basename, so the file MUST be named
// exactly cloudflared / cloudflared.exe.
func fakeCloudflaredName() string {
	if runtime.GOOS == "windows" {
		return "cloudflared.exe"
	}
	return "cloudflared"
}

func TestScanAndKillOrphans_KillsLeftoverInStoreRoot(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable not available: %v", err)
	}

	storeRoot := filepath.Join(t.TempDir(), "data", "bin", "cloudflared")
	versionDir := filepath.Join(storeRoot, "2026.test.0")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	fakeBin := filepath.Join(versionDir, fakeCloudflaredName())
	copyExecutable(t, self, fakeBin)

	// Spawn the fake. Inherits CFDM_TEST_SLEEPER so the child sleeps
	// in TestMain instead of running the test suite.
	cmd := exec.Command(fakeBin)
	cmd.Env = append(os.Environ(), "CFDM_TEST_SLEEPER=30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn fake cloudflared: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort: if scan failed, don't leak the child.
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	// Give the OS a moment to populate /proc (Linux) or the process
	// snapshot (Windows). gopsutil reads from those.
	time.Sleep(200 * time.Millisecond)

	killed, err := process.ScanAndKillOrphans(storeRoot, nil)
	if err != nil {
		t.Fatalf("ScanAndKillOrphans: %v", err)
	}
	if len(killed) == 0 {
		t.Fatalf("expected the scanner to kill at least 1 leftover, killed=%v", killed)
	}
	found := false
	for _, pid := range killed {
		if pid == cmd.Process.Pid {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("scanner killed %v but not our PID %d", killed, cmd.Process.Pid)
	}

	// Confirm the child is actually dead, not just signalled.
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("child %d did not exit after scan reported kill", cmd.Process.Pid)
	}
}

func TestScanAndKillOrphans_DoesNotKillProcessOutsideRoot(t *testing.T) {
	// Spawn the test binary as a sleeper outside the configured store
	// root and ensure the scanner does NOT touch it. Belt-and-suspenders
	// against future regressions in prefix-matching.
	self, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable not available: %v", err)
	}

	tmp := t.TempDir()
	storeRoot := filepath.Join(tmp, "data", "bin", "cloudflared")
	if err := os.MkdirAll(storeRoot, 0o755); err != nil {
		t.Fatalf("mkdir store root: %v", err)
	}
	// fake cloudflared OUTSIDE the store
	outsideDir := filepath.Join(tmp, "rogue")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outside dir: %v", err)
	}
	outsideBin := filepath.Join(outsideDir, fakeCloudflaredName())
	copyExecutable(t, self, outsideBin)

	cmd := exec.Command(outsideBin)
	cmd.Env = append(os.Environ(), "CFDM_TEST_SLEEPER=10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn rogue cloudflared: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	time.Sleep(200 * time.Millisecond)

	killed, _ := process.ScanAndKillOrphans(storeRoot, nil)
	for _, pid := range killed {
		if pid == cmd.Process.Pid {
			t.Fatalf("scanner killed process %d which is OUTSIDE storeRoot — safety guard failed", pid)
		}
	}
}
