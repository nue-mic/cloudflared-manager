//go:build windows

package process_test

import (
	"context"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/nue-mic/cloudflared-manager/internal/process"
)

// IsProcessInJob isn't exported by golang.org/x/sys/windows; call it
// directly via LazyDLL for this test only.
var (
	kernel32           = windows.NewLazySystemDLL("kernel32.dll")
	procIsProcessInJob = kernel32.NewProc("IsProcessInJob")
)

// isProcessInJob queries whether process is in the given job (pass 0 to
// ask "in any job at all"). Mirrors the Win32 signature.
func isProcessInJob(t *testing.T, process windows.Handle, job windows.Handle) bool {
	t.Helper()
	var result int32
	r1, _, e := procIsProcessInJob.Call(
		uintptr(process),
		uintptr(job),
		uintptr(unsafe.Pointer(&result)),
	)
	if r1 == 0 {
		t.Fatalf("IsProcessInJob syscall failed: %v", e)
	}
	return result != 0
}

// TestInitParentJob_Idempotent ensures the sync.Once-gated init can be
// safely called many times. A common boot-path bug would be a panic on
// the second call.
func TestInitParentJob_Idempotent(t *testing.T) {
	if err := process.InitParentJob(); err != nil {
		t.Fatalf("first init: %v", err)
	}
	if err := process.InitParentJob(); err != nil {
		t.Fatalf("second init: %v", err)
	}
	if !process.ParentJobReady() {
		t.Fatalf("ParentJobReady reports false after successful init")
	}
}

// TestInitParentJob_KillOnJobCloseFlagIsSet is the regression guard for
// the core safety promise. TestSpawn_ChildIsInParentJob only proves the
// child is in *some* Job; if a future refactor silently drops the
// SetInformationJobObject call (or its flags), that test still passes
// but KILL_ON_JOB_CLOSE never fires and Windows orphans return. Here we
// query the Job's actual limit info and assert the flag.
func TestInitParentJob_KillOnJobCloseFlagIsSet(t *testing.T) {
	if err := process.InitParentJob(); err != nil {
		t.Fatalf("init job: %v", err)
	}
	h := process.JobHandleForTest()
	if h == 0 {
		t.Skip("parent job not ready (e.g. sandboxed environment); cannot inspect flags")
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	var ret uint32
	err := windows.QueryInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
		&ret,
	)
	if err != nil {
		t.Fatalf("QueryInformationJobObject: %v", err)
	}
	if info.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		t.Fatalf("JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE not set; LimitFlags=0x%x",
			info.BasicLimitInformation.LimitFlags)
	}
}

// TestSpawn_ChildIsInParentJob is the end-to-end proof: after Spawn the
// child PID must satisfy IsProcessInJob(pid, parentJob) == true. We can
// only check "child is in *some* job" via IsProcessInJob with a nil job
// handle from the kernel side; the public API can only confirm "child
// is in any job" reliably, since the job handle is package-private. We
// use the inverse-style: ask if the child is in the calling process's
// job — which IS our parent job after InitParentJob.
func TestSpawn_ChildIsInParentJob(t *testing.T) {
	// Pre-flight: if the test binary itself is already inside a Job
	// (CI sandbox, custom service host, etc.), the post-test assertion
	// "child is in some Job" would pass spuriously regardless of whether
	// attachToParentJob actually attached the child. Skip rather than
	// give a false green.
	if isProcessInJob(t, windows.CurrentProcess(), 0) {
		t.Skip("test binary already inside a pre-existing Job; assertion would be ambiguous")
	}
	if err := process.InitParentJob(); err != nil {
		t.Fatalf("init job: %v", err)
	}
	if !process.ParentJobReady() {
		t.Skip("parent job not ready (e.g. nested-job rejected); test cannot proceed")
	}

	bin := "powershell.exe"
	args := []string{"-NoLogo", "-NoProfile", "-Command", "Start-Sleep -Seconds 30"}
	w, err := process.Spawn(context.Background(), process.SpawnParams{
		BinaryPath:   bin,
		Args:         args,
		StartupGrace: 500 * time.Millisecond,
		StopGrace:    500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer func() {
		_ = w.Stop()
	}()

	pid := w.PID()
	if pid <= 0 {
		t.Fatalf("spawn returned non-positive pid: %d", pid)
	}

	ph, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if err != nil {
		t.Fatalf("OpenProcess(child=%d): %v", pid, err)
	}
	defer windows.CloseHandle(ph)

	// IsProcessInJob with job=0 reports "is process in ANY job".
	// We can't pass the package-private parent-job handle from here,
	// but any-job is sufficient: a stock `go test` binary inherits no
	// pre-existing Job, so the only way the child is now in some Job
	// is because Spawn->attachToParentJob put it there.
	if !isProcessInJob(t, ph, 0) {
		t.Fatalf("expected child %d to be in a Job after Spawn, but it is not", pid)
	}
}
