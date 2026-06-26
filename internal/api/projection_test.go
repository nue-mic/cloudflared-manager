package api

import (
	"strings"
	"testing"

	"github.com/nue-mic/cloudflared-manager/pkg/cfdconfig"
)

func TestProjectEnv_MapsConfigAndOmitsToken(t *testing.T) {
	cfg := &cfdconfig.TunnelConfigV1{Token: "super-secret-token"}
	cfg.Edge.Protocol = "quic"
	cfg.Edge.EdgeIPVersion = "6"
	cfg.Logging.LogLevel = "debug"
	cfg.Reliability.Retries = 7

	env := projectEnv(cfg)

	if env["TUNNEL_TRANSPORT_PROTOCOL"] != "quic" {
		t.Errorf("TUNNEL_TRANSPORT_PROTOCOL = %q, want quic", env["TUNNEL_TRANSPORT_PROTOCOL"])
	}
	if env["TUNNEL_EDGE_IP_VERSION"] != "6" {
		t.Errorf("TUNNEL_EDGE_IP_VERSION = %q, want 6", env["TUNNEL_EDGE_IP_VERSION"])
	}
	if env["TUNNEL_LOGLEVEL"] != "debug" {
		t.Errorf("TUNNEL_LOGLEVEL = %q, want debug", env["TUNNEL_LOGLEVEL"])
	}
	if env["TUNNEL_RETRIES"] != "7" {
		t.Errorf("TUNNEL_RETRIES = %q, want 7", env["TUNNEL_RETRIES"])
	}
	// The token is mandated env injected (masked) by the handler, never by
	// projectEnv — and the plaintext must never appear anywhere in the map.
	if _, ok := env["TUNNEL_TOKEN"]; ok {
		t.Error("projectEnv must not include TUNNEL_TOKEN")
	}
	for k, v := range env {
		if strings.Contains(v, "super-secret-token") {
			t.Errorf("env[%q] leaks plaintext token: %q", k, v)
		}
	}
}
