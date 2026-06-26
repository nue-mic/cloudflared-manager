package cfdconfig_test

import (
	"strings"
	"testing"

	"github.com/nue-mic/cloudflared-manager/pkg/cfdconfig"
)

// validToken is a base64-ish 100-char string that satisfies the syntax
// check used by Validate; not a real cloudflared token.
var validToken = strings.Repeat("A", 100)

func TestValidate_NilReceiver(t *testing.T) {
	var c *cfdconfig.TunnelConfigV1
	if err := c.Validate(); err == nil {
		t.Fatal("expected ErrNilConfig")
	}
}

func TestValidate_EmptyDraft(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{}
	if err := c.Validate(); err != nil {
		t.Fatalf("empty draft should pass, got %v", err)
	}
}

func TestValidate_TokenTooShort(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{Token: "abc"}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("expected token length error, got %v", err)
	}
}

func TestValidate_TokenBadChar(t *testing.T) {
	bad := strings.Repeat("A", 99) + "!"
	c := &cfdconfig.TunnelConfigV1{Token: bad}
	if err := c.Validate(); err == nil {
		t.Fatal("expected token charset error")
	}
}

func TestValidate_ProtocolUnknown(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token: validToken,
		Edge:  cfdconfig.EdgeConfig{Protocol: "h3"},
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "protocol") {
		t.Fatalf("expected protocol enum error, got %v", err)
	}
}

func TestValidate_EdgeIPVersionUnknown(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token: validToken,
		Edge:  cfdconfig.EdgeConfig{EdgeIPVersion: "v6"},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected edgeIpVersion enum error")
	}
}

func TestValidate_RegionUnknown(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token: validToken,
		Edge:  cfdconfig.EdgeConfig{Region: "eu"},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected region enum error")
	}
}

func TestValidate_RetriesOutOfRange(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token:       validToken,
		Reliability: cfdconfig.ReliabilityConfig{Retries: 50},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected retries range error")
	}
}

func TestValidate_GracePeriodBadDuration(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token:       validToken,
		Reliability: cfdconfig.ReliabilityConfig{GracePeriod: "not-a-duration"},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected grace period parse error")
	}
}

func TestValidate_GracePeriodOutOfRange(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token:       validToken,
		Reliability: cfdconfig.ReliabilityConfig{GracePeriod: "10m"},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected grace period range error")
	}
}

func TestValidate_LogLevelUnknown(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token:   validToken,
		Logging: cfdconfig.LoggingConfig{LogLevel: "verbose"},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected logLevel enum error")
	}
}

func TestValidate_LabelTooLong(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token:    validToken,
		Identity: cfdconfig.IdentityConfig{Label: strings.Repeat("a", 65)},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected label length error")
	}
}

func TestValidate_LabelBadChar(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token:    validToken,
		Identity: cfdconfig.IdentityConfig{Label: "bad/slash"},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected label charset error")
	}
}

func TestValidate_TagBadKey(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token:    validToken,
		Identity: cfdconfig.IdentityConfig{Tags: map[string]string{"1bad": "x"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected tag key regex error")
	}
}

func TestValidate_AdvancedEnvReserved(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token:                validToken,
		AdvancedEnvOverrides: map[string]string{"TUNNEL_TOKEN": "x"},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected reserved env error")
	}
}

func TestValidate_AdvancedEnvBadKey(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token:                validToken,
		AdvancedEnvOverrides: map[string]string{"lower_case": "x"},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected env key regex error")
	}
}

func TestValidate_HappyPath(t *testing.T) {
	c := &cfdconfig.TunnelConfigV1{
		Token: validToken,
		Edge: cfdconfig.EdgeConfig{
			Protocol:      "auto",
			EdgeIPVersion: "4",
			Region:        "",
			PostQuantum:   false,
		},
		Reliability: cfdconfig.ReliabilityConfig{
			Retries:     5,
			GracePeriod: "30s",
		},
		Logging: cfdconfig.LoggingConfig{
			LogLevel:          "info",
			TransportLogLevel: "info",
		},
		Identity: cfdconfig.IdentityConfig{
			Label: "home-nas",
			Tags:  map[string]string{"env": "prod"},
		},
		AdvancedEnvOverrides: map[string]string{"TUNNEL_DNS_RESOLVER_ADDRS": "1.1.1.1"},
		BinaryVersion:        "2026.5.2",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("happy path failed: %v", err)
	}
}
