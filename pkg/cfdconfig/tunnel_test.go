package cfdconfig_test

import (
	"strings"
	"testing"

	"github.com/nue-mic/cloudflared-manager/pkg/cfdconfig"
)

const sampleYAML = `token: eyJhIjoiMjk
binaryVersion: "current"
edge:
  protocol: auto
  edgeIpVersion: "4"
  postQuantum: false
reliability:
  retries: 5
  gracePeriod: 30s
logging:
  logLevel: info
  transportLogLevel: info
identity:
  label: home-nas
  tags:
    env: prod
    site: shanghai
`

func TestParseYAML_Sample(t *testing.T) {
	cfg, err := cfdconfig.ParseYAML([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Token != "eyJhIjoiMjk" {
		t.Errorf("token=%q", cfg.Token)
	}
	if cfg.BinaryVersion != "current" {
		t.Errorf("binaryVersion=%q", cfg.BinaryVersion)
	}
	if cfg.Edge.Protocol != "auto" {
		t.Errorf("edge.protocol=%q", cfg.Edge.Protocol)
	}
	if cfg.Edge.EdgeIPVersion != "4" {
		t.Errorf("edge.edgeIpVersion=%q", cfg.Edge.EdgeIPVersion)
	}
	if cfg.Reliability.Retries != 5 {
		t.Errorf("reliability.retries=%d", cfg.Reliability.Retries)
	}
	if cfg.Reliability.GracePeriod != "30s" {
		t.Errorf("reliability.gracePeriod=%q", cfg.Reliability.GracePeriod)
	}
	if cfg.Identity.Label != "home-nas" {
		t.Errorf("identity.label=%q", cfg.Identity.Label)
	}
	if cfg.Identity.Tags["env"] != "prod" {
		t.Errorf("identity.tags[env]=%q", cfg.Identity.Tags["env"])
	}
}

func TestParseYAML_Empty(t *testing.T) {
	cfg, err := cfdconfig.ParseYAML([]byte(""))
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected zero-value struct, got nil")
	}
}

func TestParseYAML_Whitespace(t *testing.T) {
	cfg, err := cfdconfig.ParseYAML([]byte("   \n  \n"))
	if err != nil {
		t.Fatalf("parse whitespace: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected zero-value struct, got nil")
	}
}

func TestParseYAML_Malformed(t *testing.T) {
	_, err := cfdconfig.ParseYAML([]byte("token: [unclosed"))
	if err == nil {
		t.Fatal("expected parse error on malformed yaml")
	}
}

func TestMarshalYAML_RoundTrip(t *testing.T) {
	orig, err := cfdconfig.ParseYAML([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := cfdconfig.MarshalYAML(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("marshal returned empty")
	}
	again, err := cfdconfig.ParseYAML(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if again.Token != orig.Token ||
		again.Edge.Protocol != orig.Edge.Protocol ||
		again.Reliability.Retries != orig.Reliability.Retries ||
		again.Identity.Tags["env"] != orig.Identity.Tags["env"] {
		t.Errorf("round trip diff: %+v vs %+v", orig, again)
	}
}

func TestMarshalYAML_OmitEmpty(t *testing.T) {
	cfg := &cfdconfig.TunnelConfigV1{Token: "abc12345" + strings.Repeat("X", 100)}
	out, err := cfdconfig.MarshalYAML(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	for _, banned := range []string{"edge:", "reliability:", "logging:", "identity:", "advancedEnvOverrides:", "binaryVersion:"} {
		if strings.Contains(s, banned) {
			t.Errorf("expected %q omitted with omitempty, got:\n%s", banned, s)
		}
	}
}

func TestParseJSON_RoundTripWithYAML(t *testing.T) {
	cfg, err := cfdconfig.ParseYAML([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("parse yaml: %v", err)
	}
	jsonBytes, err := cfdconfig.MarshalJSON(cfg)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	jc, err := cfdconfig.ParseJSON(jsonBytes)
	if err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if jc.Edge.Protocol != cfg.Edge.Protocol {
		t.Errorf("json round-trip lost edge.protocol")
	}
	if jc.Identity.Tags["env"] != cfg.Identity.Tags["env"] {
		t.Errorf("json round-trip lost identity.tags[env]")
	}
}

func TestMarshalJSON_NilSafe(t *testing.T) {
	out, err := cfdconfig.MarshalJSON(nil)
	if err != nil {
		t.Fatalf("nil marshal: %v", err)
	}
	if string(out) != "null" {
		t.Errorf("expected 'null', got %q", string(out))
	}
}
