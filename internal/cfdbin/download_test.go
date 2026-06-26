package cfdbin_test

import (
	"testing"

	"github.com/nue-mic/cloudflared-manager/internal/cfdbin"
)

const sampleReleaseBody = `Some intro text.

### SHA256 Checksums:

cloudflared-linux-amd64: 5286698547f03df745adb2355f04c12dde52ef425491e81f433642d695521886
cloudflared-darwin-amd64.tgz: aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899
cloudflared-windows-amd64.exe: deadbeefcafef00d1122334455667788991122334455667788991122334455667
`

func TestParseSHA256_Found(t *testing.T) {
	got := cfdbin.ParseSHA256(sampleReleaseBody, "cloudflared-linux-amd64")
	want := "5286698547f03df745adb2355f04c12dde52ef425491e81f433642d695521886"
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestParseSHA256_DarwinArchive(t *testing.T) {
	got := cfdbin.ParseSHA256(sampleReleaseBody, "cloudflared-darwin-amd64.tgz")
	if got != "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899" {
		t.Errorf("got %s", got)
	}
}

func TestParseSHA256_Missing(t *testing.T) {
	if got := cfdbin.ParseSHA256(sampleReleaseBody, "cloudflared-bsd-amd64"); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestParseSHA256_NoBody(t *testing.T) {
	if got := cfdbin.ParseSHA256("", "cloudflared-linux-amd64"); got != "" {
		t.Errorf("expected empty on empty body, got %s", got)
	}
}
