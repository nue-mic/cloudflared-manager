package cfdflags_test

import (
	"testing"

	"github.com/nue-mic/cloudflared-manager/pkg/cfdflags"
)

func TestAllowEnvOverride_Reserved(t *testing.T) {
	for _, env := range []string{
		"TUNNEL_TOKEN", "NO_AUTOUPDATE", "AUTOUPDATE_FREQ",
		"TUNNEL_METRICS", "TUNNEL_OUTPUT", "TUNNEL_LOGFILE",
		"TUNNEL_LOGDIRECTORY",
	} {
		if cfdflags.AllowEnvOverride(env) {
			t.Errorf("reserved env %q was allowed", env)
		}
	}
}

func TestAllowEnvOverride_Modelled(t *testing.T) {
	for _, env := range []string{
		"TUNNEL_TRANSPORT_PROTOCOL", "TUNNEL_EDGE_IP_VERSION",
		"TUNNEL_EDGE_BIND_ADDRESS", "TUNNEL_REGION",
		"TUNNEL_POST_QUANTUM", "TUNNEL_RETRIES",
		"TUNNEL_GRACE_PERIOD", "TUNNEL_LOGLEVEL",
		"TUNNEL_TRANSPORT_LOGLEVEL", "TUNNEL_TAG",
	} {
		if !cfdflags.AllowEnvOverride(env) {
			t.Errorf("modelled env %q was rejected", env)
		}
	}
}

func TestAllowEnvOverride_Extra(t *testing.T) {
	for _, env := range []string{
		"TUNNEL_DNS_RESOLVER_ADDRS",
		"TUNNEL_METRICS_UPDATE_FREQ",
		"TUNNEL_MANAGEMENT_DIAGNOSTICS",
	} {
		if !cfdflags.AllowEnvOverride(env) {
			t.Errorf("extra allowed env %q was rejected", env)
		}
	}
}

func TestAllowEnvOverride_RandomKey(t *testing.T) {
	if cfdflags.AllowEnvOverride("PATH") {
		t.Error("PATH should not be allowed")
	}
	if cfdflags.AllowEnvOverride("RANDOM_NEW_FLAG") {
		t.Error("RANDOM_NEW_FLAG should not be allowed")
	}
}
