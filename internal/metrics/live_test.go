package metrics

import "testing"

// sampleCloudflaredMetrics is a representative subset of a running
// cloudflared connector's /metrics output (Prometheus text format).
const sampleCloudflaredMetrics = `
# HELP cloudflared_tunnel_ha_connections Number of active HA connections
# TYPE cloudflared_tunnel_ha_connections gauge
cloudflared_tunnel_ha_connections 4
cloudflared_tunnel_total_requests 12345
cloudflared_tunnel_request_errors 3
cloudflared_tunnel_response_by_code{status_code="200"} 12000
cloudflared_tunnel_response_by_code{status_code="502"} 1
quic_client_smoothed_rtt{conn_index="0"} 21.4
quic_client_smoothed_rtt{conn_index="1"} 30.1
quic_client_lost_packets{conn_index="0"} 2
go_goroutines 37
process_resident_memory_bytes 24117248
build_info{goversion="go1.25",revision="abc",version="2026.5.2"} 1
cloudflared_tunnel_server_locations{conn_index="0",edge_location="SJC"} 1
`

func TestFoldLive_ServerScalars(t *testing.T) {
	live := FoldLive(ParsePromText(sampleCloudflaredMetrics))

	if live.HAConnections != 4 {
		t.Errorf("HAConnections = %d, want 4", live.HAConnections)
	}
	if live.RequestsTotal != 12345 {
		t.Errorf("RequestsTotal = %d, want 12345", live.RequestsTotal)
	}
	if live.RequestErrors != 3 {
		t.Errorf("RequestErrors = %d, want 3", live.RequestErrors)
	}
	if live.Response5xx != 1 {
		t.Errorf("Response5xx = %d, want 1", live.Response5xx)
	}
	if live.Goroutines != 37 {
		t.Errorf("Goroutines = %d, want 37", live.Goroutines)
	}
	if live.ResidentMemoryBytes != 24117248 {
		t.Errorf("ResidentMemoryBytes = %d, want 24117248", live.ResidentMemoryBytes)
	}
	if live.Version != "2026.5.2" {
		t.Errorf("Version = %q, want 2026.5.2", live.Version)
	}
	if live.Protocol != "quic" {
		t.Errorf("Protocol = %q, want quic", live.Protocol)
	}
}

func TestFoldLive_PerConnection(t *testing.T) {
	live := FoldLive(ParsePromText(sampleCloudflaredMetrics))

	if len(live.Connections) != 2 {
		t.Fatalf("len(Connections) = %d, want 2", len(live.Connections))
	}
	// Connections must be sorted by conn_index for deterministic output.
	c0 := live.Connections[0]
	if c0.ConnIndex != 0 {
		t.Errorf("Connections[0].ConnIndex = %d, want 0", c0.ConnIndex)
	}
	if c0.RTT != 21.4 {
		t.Errorf("Connections[0].RTT = %v, want 21.4", c0.RTT)
	}
	if c0.LostPackets != 2 {
		t.Errorf("Connections[0].LostPackets = %v, want 2", c0.LostPackets)
	}
	if c0.Location != "SJC" {
		t.Errorf("Connections[0].Location = %q, want SJC", c0.Location)
	}
	c1 := live.Connections[1]
	if c1.ConnIndex != 1 {
		t.Errorf("Connections[1].ConnIndex = %d, want 1", c1.ConnIndex)
	}
	if c1.RTT != 30.1 {
		t.Errorf("Connections[1].RTT = %v, want 30.1", c1.RTT)
	}
}

func TestFoldLive_HTTP2WhenNoQUIC(t *testing.T) {
	// No quic_client_* metrics, but tunnel has HA connections → http2.
	body := "cloudflared_tunnel_ha_connections 2\n"
	live := FoldLive(ParsePromText(body))
	if live.Protocol != "http2" {
		t.Errorf("Protocol = %q, want http2", live.Protocol)
	}
}

func TestFoldLive_UnknownWhenIdle(t *testing.T) {
	live := FoldLive(ParsePromText("go_goroutines 5\n"))
	if live.Protocol != "unknown" {
		t.Errorf("Protocol = %q, want unknown", live.Protocol)
	}
	if len(live.Connections) != 0 {
		t.Errorf("Connections = %v, want empty", live.Connections)
	}
}
