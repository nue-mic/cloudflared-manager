//go:build windows

// Windows Job Object integration.
//
// Problem statement (see orphan.go for the broader context): when
// cfdmgrd dies without going through its graceful Shutdown path —
// taskkill /F, IDE debug-stop, panic, BSOD — the cloudflared children
// it spawned outlive it. They keep their Cloudflare-edge connections
// alive, so the dashboard sees N ghost connectors and a fresh cfdmgrd
// run happily adds another one on top.
//
// Job Objects are the canonical Windows answer. cfdmgrd creates one
// process-wide Job Object marked with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
// then assigns every spawned cloudflared child to it. The kernel holds
// the job handle for the lifetime of the cfdmgrd process; when the
// process exits *for any reason* the kernel closes the handle, which —
// because of the KILL_ON_JOB_CLOSE flag — terminates every assigned
// child. This is the only Windows mechanism that's robust against
// taskkill /F.
//
// Two important non-obvious points:
//
//   1. We deliberately NEVER call CloseHandle on the job ourselves.
//      Doing so would immediately kill every assigned child while
//      cfdmgrd is still running and trying to use them. The handle is
//      owned by the OS process and reaped automatically on exit.
//
//   2. Children are attached POST-Start (i.e. after exec.Cmd.Start
//      returns). There is a microsecond-scale window between Start and
//      attach where a child wouldn't be in the job — if cfdmgrd is
//      taskkill'd *inside* that window, that one child leaks. The
//      orphan scanner picks it up on next boot. A fully atomic attach
//      requires CREATE_SUSPENDED + custom syscall plumbing; the
//      additional complexity is not justified by the tiny remaining
//      leak window.

package process

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	jobOnce   sync.Once
	jobHandle windows.Handle
	jobErr    error
)

// InitParentJob lazily creates the process-wide Job Object and arms it
// with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. Safe to call multiple times;
// only the first call performs the syscalls. Returns the cached error
// from the first attempt — callers should log-and-continue rather than
// abort startup (the orphan scanner is the defence-in-depth path).
//
// Common failure modes that are NOT fatal:
//   - CreateJobObject returns ACCESS_DENIED on heavily-restricted
//     desktops (e.g. running inside a sandbox).
//   - AssignProcessToJobObject can fail on legacy Windows (< 8) when
//     cfdmgrd is itself already inside a non-nestable Job (some service
//     hosts do this). Win8+ supports nested jobs (up to 16 levels).
func InitParentJob() error {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			jobErr = fmt.Errorf("process: CreateJobObject: %w", err)
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
				LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
			},
		}
		_, err = windows.SetInformationJobObject(
			h,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		)
		if err != nil {
			_ = windows.CloseHandle(h)
			jobErr = fmt.Errorf("process: SetInformationJobObject: %w", err)
			return
		}
		// NOTE: we deliberately do not store the handle in a finaliser
		// or close it on package shutdown. The OS reaps it when the
		// process exits, which is exactly the moment we want
		// KILL_ON_JOB_CLOSE to fire.
		jobHandle = h
	})
	return jobErr
}

// attachToParentJob assigns the given child PID to the parent job.
// No-op when InitParentJob has not been called or has errored
// (jobHandle == 0). Errors are returned so Spawn can log them but
// should not abort the child — the orphan scanner is the fallback.
func attachToParentJob(pid int) error {
	if jobHandle == 0 {
		return nil
	}
	ph, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA,
		false,
		uint32(pid),
	)
	if err != nil {
		return fmt.Errorf("process: OpenProcess(%d): %w", pid, err)
	}
	defer windows.CloseHandle(ph)
	if err := windows.AssignProcessToJobObject(jobHandle, ph); err != nil {
		return fmt.Errorf("process: AssignProcessToJobObject(%d): %w", pid, err)
	}
	return nil
}

// ParentJobReady reports whether the kernel Job Object was successfully
// initialised. Useful for status / observability ("is the kill-on-close
// safety net active right now?").
func ParentJobReady() bool { return jobHandle != 0 }
