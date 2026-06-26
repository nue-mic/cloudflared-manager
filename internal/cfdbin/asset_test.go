package cfdbin_test

import (
	"testing"

	"github.com/nue-mic/cloudflared-manager/internal/cfdbin"
)

func TestAssetName_Supported(t *testing.T) {
	cases := []struct{ os, arch, want string }{
		{"linux", "amd64", "cloudflared-linux-amd64"},
		{"linux", "arm64", "cloudflared-linux-arm64"},
		{"linux", "arm", "cloudflared-linux-arm"},
		{"linux", "armhf", "cloudflared-linux-armhf"},
		{"linux", "386", "cloudflared-linux-386"},
		{"darwin", "amd64", "cloudflared-darwin-amd64.tgz"},
		{"darwin", "arm64", "cloudflared-darwin-arm64.tgz"},
		{"windows", "amd64", "cloudflared-windows-amd64.exe"},
		{"windows", "386", "cloudflared-windows-386.exe"},
	}
	for _, c := range cases {
		if got := cfdbin.AssetName(c.os, c.arch); got != c.want {
			t.Errorf("AssetName(%s,%s) = %q, want %q", c.os, c.arch, got, c.want)
		}
	}
}

func TestAssetName_Unsupported(t *testing.T) {
	for _, c := range []struct{ os, arch string }{
		{"linux", "riscv64"}, {"freebsd", "amd64"}, {"windows", "arm64"}, {"plan9", "amd64"},
	} {
		if got := cfdbin.AssetName(c.os, c.arch); got != "" {
			t.Errorf("AssetName(%s,%s) = %q, want empty", c.os, c.arch, got)
		}
	}
}

func TestBinaryFilename(t *testing.T) {
	if got := cfdbin.BinaryFilename("windows"); got != "cloudflared.exe" {
		t.Errorf("windows: got %q", got)
	}
	if got := cfdbin.BinaryFilename("linux"); got != "cloudflared" {
		t.Errorf("linux: got %q", got)
	}
}

func TestIsArchive(t *testing.T) {
	if !cfdbin.IsArchive("cloudflared-darwin-amd64.tgz") {
		t.Error("expected tgz to be archive")
	}
	if cfdbin.IsArchive("cloudflared-linux-amd64") {
		t.Error("expected bare binary not to be archive")
	}
}
