package cfdflags_test

import (
	"testing"

	"github.com/nue-mic/cloudflared-manager/pkg/cfdflags"
)

func TestAll_Count(t *testing.T) {
	flags := cfdflags.All()
	if n := len(flags); n < 10 {
		t.Fatalf("registry shrunk unexpectedly: %d flags", n)
	}
}

func TestAll_NoDuplicateYAMLPath(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range cfdflags.All() {
		if seen[f.YAMLPath] {
			t.Errorf("duplicate YAMLPath %q", f.YAMLPath)
		}
		seen[f.YAMLPath] = true
	}
}

func TestAll_NoDuplicateEnvName(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range cfdflags.All() {
		if f.EnvName == "" {
			continue
		}
		if seen[f.EnvName] {
			t.Errorf("duplicate EnvName %q", f.EnvName)
		}
		seen[f.EnvName] = true
	}
}

func TestAll_LabelHasNoEnv(t *testing.T) {
	for _, f := range cfdflags.All() {
		if f.YAMLPath == "identity.label" && f.EnvName != "" {
			t.Errorf("identity.label unexpectedly has EnvName=%q; cloudflared exposes no TUNNEL_LABEL", f.EnvName)
		}
	}
}

func TestByEnvName_OmitsLabel(t *testing.T) {
	m := cfdflags.ByEnvName()
	for env := range m {
		if env == "" {
			t.Error("ByEnvName included empty key")
		}
	}
}

func TestByEnvName_HasProtocol(t *testing.T) {
	m := cfdflags.ByEnvName()
	if _, ok := m["TUNNEL_TRANSPORT_PROTOCOL"]; !ok {
		t.Error("expected TUNNEL_TRANSPORT_PROTOCOL in ByEnvName")
	}
}

func TestToTunnelEnv_OmitZero(t *testing.T) {
	out := cfdflags.ToTunnelEnv(cfdflags.Options{})
	if n := len(out); n != 0 {
		t.Fatalf("zero options should produce empty map, got %d entries: %+v", n, out)
	}
}

func TestToTunnelEnv_Happy(t *testing.T) {
	out := cfdflags.ToTunnelEnv(cfdflags.Options{
		Protocol:          "auto",
		EdgeIPVersion:     "4",
		Region:            "us",
		PostQuantum:       true,
		Retries:           5,
		GracePeriod:       "30s",
		LogLevel:          "info",
		TransportLogLevel: "warn",
		Tags:              map[string]string{"site": "shanghai", "env": "prod"},
		AdvancedEnvOverrides: map[string]string{
			"TUNNEL_DNS_RESOLVER_ADDRS": "1.1.1.1",
			"TUNNEL_TOKEN":              "abc", // reserved, should drop
			"BOGUS":                     "x",   // not allowed, should drop
		},
	})
	expect := map[string]string{
		"TUNNEL_TRANSPORT_PROTOCOL": "auto",
		"TUNNEL_EDGE_IP_VERSION":    "4",
		"TUNNEL_REGION":             "us",
		"TUNNEL_POST_QUANTUM":       "true",
		"TUNNEL_RETRIES":            "5",
		"TUNNEL_GRACE_PERIOD":       "30s",
		"TUNNEL_LOGLEVEL":           "info",
		"TUNNEL_TRANSPORT_LOGLEVEL": "warn",
		"TUNNEL_TAG":                "env=prod,site=shanghai",
		"TUNNEL_DNS_RESOLVER_ADDRS": "1.1.1.1",
	}
	if len(out) != len(expect) {
		t.Errorf("len mismatch: got %d want %d (%+v)", len(out), len(expect), out)
	}
	for k, v := range expect {
		if out[k] != v {
			t.Errorf("env %s: got %q want %q", k, out[k], v)
		}
	}
	if _, leaked := out["TUNNEL_TOKEN"]; leaked {
		t.Error("reserved TUNNEL_TOKEN leaked through AdvancedEnvOverrides")
	}
	if _, leaked := out["BOGUS"]; leaked {
		t.Error("BOGUS leaked through AdvancedEnvOverrides")
	}
}

func TestLabelArgv_Empty(t *testing.T) {
	if got := cfdflags.LabelArgv(""); got != nil {
		t.Errorf("empty label should return nil, got %v", got)
	}
	if got := cfdflags.LabelArgv("   "); got != nil {
		t.Errorf("whitespace label should return nil, got %v", got)
	}
}

func TestLabelArgv_NonEmpty(t *testing.T) {
	got := cfdflags.LabelArgv("home-nas")
	want := []string{"--label", "home-nas"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("LabelArgv home-nas = %v, want %v", got, want)
	}
}

func TestDefaultAlertTemplates_Count(t *testing.T) {
	tpls := cfdflags.DefaultAlertTemplates()
	if n := len(tpls); n != 12 {
		t.Fatalf("expected 12 default templates, got %d", n)
	}
}

func TestDefaultAlertTemplates_StableIDs(t *testing.T) {
	wantIDs := []string{
		"ha_degraded", "ha_disconnected", "ready_probe_failed",
		"reconnect_storm", "http_5xx_ratio_high", "request_errors_high",
		"quic_rtt_high", "quic_packet_loss_high", "udp_dropped_high",
		"rss_high", "goroutines_high", "process_restarted",
	}
	tpls := cfdflags.DefaultAlertTemplates()
	for i, want := range wantIDs {
		if tpls[i].ID != want {
			t.Errorf("template[%d] id = %q, want %q", i, tpls[i].ID, want)
		}
	}
}
