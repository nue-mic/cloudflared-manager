package cfdbin_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nue-mic/cloudflared-manager/internal/cfdbin"
)

// fakeInstall plants a fake binary + meta.json under a version dir so
// store-level tests can run without hitting the network.
func fakeInstall(t *testing.T, s *cfdbin.Store, version string) string {
	t.Helper()
	vdir := filepath.Join(s.Root(), version)
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(vdir, cfdbin.BinaryFilename(runtime.GOOS))
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestResolve_NoActive(t *testing.T) {
	s := cfdbin.New(t.TempDir())
	if _, err := s.Resolve(""); err == nil {
		t.Fatal("expected ErrNoActive")
	}
}

func TestResolve_SpecificMissing(t *testing.T) {
	s := cfdbin.New(t.TempDir())
	if _, err := s.Resolve("9.9.9"); err == nil {
		t.Fatal("expected ErrNotInstalled")
	}
}

func TestResolve_AfterActivate(t *testing.T) {
	s := cfdbin.New(t.TempDir())
	fakeInstall(t, s, "2026.5.2")
	if err := s.Activate("2026.5.2"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	p, err := s.Resolve("")
	if err != nil {
		t.Fatalf("resolve current: %v", err)
	}
	if filepath.Base(p) != cfdbin.BinaryFilename(runtime.GOOS) {
		t.Errorf("unexpected resolved path: %s", p)
	}
}

// TestResolve_RestoresExecBit covers binaries that land in the store without
// the execute bit (restored backups, manual scp, exec-bit-dropping mounts).
// Resolve must self-heal so the subsequent spawn does not fail with
// "fork/exec ...: permission denied".
func TestResolve_RestoresExecBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec bit is meaningless on Windows")
	}
	s := cfdbin.New(t.TempDir())
	bin := fakeInstall(t, s, "2026.6.0")
	if err := os.Chmod(bin, 0o644); err != nil { // simulate a non-executable copy
		t.Fatal(err)
	}
	if err := s.Activate("2026.6.0"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := s.Resolve(""); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	st, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&0o111 == 0 {
		t.Errorf("exec bit not restored: mode=%o", st.Mode())
	}
}

func TestList_NewestFirst(t *testing.T) {
	s := cfdbin.New(t.TempDir())
	for _, v := range []string{"2026.4.1", "2026.5.2", "2025.10.0"} {
		fakeInstall(t, s, v)
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	if got[0].Version != "2026.5.2" {
		t.Errorf("expected newest first, got %s", got[0].Version)
	}
}

func TestActivate_RejectMissing(t *testing.T) {
	s := cfdbin.New(t.TempDir())
	if err := s.Activate("doesnt-exist"); err == nil {
		t.Fatal("expected error on activating missing version")
	}
}

func TestDelete_RejectActive(t *testing.T) {
	s := cfdbin.New(t.TempDir())
	fakeInstall(t, s, "2026.5.2")
	_ = s.Activate("2026.5.2")
	if err := s.Delete("2026.5.2"); err == nil {
		t.Fatal("expected delete of active version to fail")
	}
}

func TestDelete_Removes(t *testing.T) {
	s := cfdbin.New(t.TempDir())
	fakeInstall(t, s, "2026.4.1")
	fakeInstall(t, s, "2026.5.2")
	_ = s.Activate("2026.5.2")
	if err := s.Delete("2026.4.1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := s.List()
	if len(got) != 1 || got[0].Version != "2026.5.2" {
		t.Errorf("after delete list = %+v", got)
	}
}
