//go:build !windows

package process

// On POSIX systems the systemd unit's KillMode=control-group (the default)
// already kills the whole cgroup when cfdmgrd exits — no Job Object
// equivalent is needed at the application layer. We keep the API surface
// identical for cross-platform callers; both functions are no-ops here.
//
// The orphan scanner (orphan.go) remains the universal defence-in-depth
// path and runs on every platform.

// InitParentJob is a no-op on POSIX. Returns nil unconditionally.
func InitParentJob() error { return nil }

// attachToParentJob is a no-op on POSIX. Returns nil unconditionally.
func attachToParentJob(_ int) error { return nil }

// ParentJobReady reports whether InitParentJob successfully created an
// underlying kernel object. Always false on POSIX (no such object).
func ParentJobReady() bool { return false }
