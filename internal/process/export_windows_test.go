//go:build windows

package process

import "golang.org/x/sys/windows"

// JobHandleForTest exposes the package-private parent Job Object handle
// so Windows-only tests can query its limit flags directly. Never
// exported in production builds.
func JobHandleForTest() windows.Handle { return jobHandle }
