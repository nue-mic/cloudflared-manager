package metrics_test

import (
	"strings"
	"testing"

	"github.com/nue-mic/cloudflared-manager/internal/metrics"
)

func TestParsePromText_BareMetric(t *testing.T) {
	body := `cloudflared_tunnel_ha_connections 4
`
	got := metrics.ParsePromText(body)
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	if got[0].Name != "cloudflared_tunnel_ha_connections" || got[0].Value != 4 {
		t.Errorf("got=%+v", got[0])
	}
	if len(got[0].Labels) != 0 {
		t.Errorf("expected no labels, got %v", got[0].Labels)
	}
}

func TestParsePromText_Labels(t *testing.T) {
	body := `cloudflared_tunnel_response_by_code{status_code="200"} 1234
cloudflared_tunnel_response_by_code{status_code="500"} 5
`
	got := metrics.ParsePromText(body)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].Labels["status_code"] != "200" || got[0].Value != 1234 {
		t.Errorf("first=%+v", got[0])
	}
	if got[1].Labels["status_code"] != "500" || got[1].Value != 5 {
		t.Errorf("second=%+v", got[1])
	}
}

func TestParsePromText_SkipsComments(t *testing.T) {
	body := `# HELP foo The foo metric.
# TYPE foo counter
foo 42
`
	got := metrics.ParsePromText(body)
	if len(got) != 1 || got[0].Name != "foo" || got[0].Value != 42 {
		t.Errorf("got=%+v", got)
	}
}

func TestParsePromText_Histogram(t *testing.T) {
	body := `cloudflared_proxy_connect_latency_count 100
cloudflared_proxy_connect_latency_sum 1234.5
cloudflared_proxy_connect_latency_bucket{le="50"} 80
cloudflared_proxy_connect_latency_bucket{le="100"} 95
`
	got := metrics.ParsePromText(body)
	if len(got) != 4 {
		t.Fatalf("len=%d want 4", len(got))
	}
}

func TestParsePromText_MultipleLabels(t *testing.T) {
	body := `quic_client_smoothed_rtt{conn_index="0",foo="bar"} 23.5
`
	got := metrics.ParsePromText(body)
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Labels["conn_index"] != "0" || got[0].Labels["foo"] != "bar" {
		t.Errorf("labels=%v", got[0].Labels)
	}
}

func TestParsePromText_EscapedQuote(t *testing.T) {
	body := `metric_x{msg="he said \"hi\""} 1
`
	got := metrics.ParsePromText(body)
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Labels["msg"] != `he said "hi"` {
		t.Errorf("escape failed: %q", got[0].Labels["msg"])
	}
}

func TestParsePromText_MalformedSkipped(t *testing.T) {
	body := `good 1
broken without value
also_good{l="v"} 2.5
`
	got := metrics.ParsePromText(body)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 (good + also_good)", len(got))
	}
}

func TestParsePromText_EmptyBody(t *testing.T) {
	if got := metrics.ParsePromText(""); len(got) != 0 {
		t.Errorf("empty body returned %d samples", len(got))
	}
	if got := metrics.ParsePromText(strings.Repeat("\n", 10)); len(got) != 0 {
		t.Errorf("whitespace returned %d samples", len(got))
	}
}
