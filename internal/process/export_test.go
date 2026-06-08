package process

// Test-only exports. Keeping these in a *_test.go file ensures they are
// invisible to non-test builds while letting the external _test package
// exercise the pure decision logic without making it part of the public
// API surface.

// OrphanMatchForTest exposes orphanMatch to package process_test.
var OrphanMatchForTest = orphanMatch

// IsSafeRootForTest exposes isSafeRoot to package process_test so the
// "refuses to scan an over-broad root" safety guard can be unit-tested.
var IsSafeRootForTest = isSafeRoot
