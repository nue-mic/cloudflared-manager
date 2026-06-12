package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EdgeConnection is one cloudflared↔Cloudflare-edge connection's live stats,
// derived from the per-conn_index QUIC metrics. RTT is the smoothed round-trip
// time in cloudflared's native unit for quic_client_smoothed_rtt (surfaced
// raw — the UI labels it "RTT" without asserting a unit).
type EdgeConnection struct {
	ConnIndex   int     `json:"conn_index"`
	Location    string  `json:"location,omitempty"`
	RTT         float64 `json:"rtt,omitempty"`
	LostPackets float64 `json:"lost_packets,omitempty"`
}

// LiveStatus is an on-demand snapshot of a running instance's cloudflared
// /metrics endpoint, parsed but NOT persisted to the time-series store. A
// zero-valued field means the corresponding metric was absent from the
// scrape (best-effort: different cloudflared versions expose different sets).
type LiveStatus struct {
	Running             bool             `json:"running"`
	ScrapedAt           int64            `json:"scraped_at,omitempty"`
	HAConnections       int              `json:"ha_connections"`
	RequestsTotal       int64            `json:"requests_total"`
	RequestErrors       int64            `json:"request_errors"`
	Response5xx         int64            `json:"response_5xx"`
	Goroutines          int              `json:"goroutines"`
	ResidentMemoryBytes int64            `json:"resident_memory_bytes"`
	Version             string           `json:"version,omitempty"`
	Protocol            string           `json:"protocol,omitempty"`
	Connections         []EdgeConnection `json:"connections"`
	Error               string           `json:"error,omitempty"`
}

// liveClient is a short-timeout HTTP client for on-demand /metrics scrapes.
var liveClient = &http.Client{Timeout: 4 * time.Second}

// Scrape fetches /metrics from a cloudflared --metrics "127.0.0.1:<port>"
// address and decodes the Prometheus text body into a flat sample slice.
func Scrape(addr string) ([]Sample, error) {
	url := "http://" + addr + "/metrics"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := liveClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scrape %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return ParsePromText(string(body)), nil
}

// FoldLive folds a parsed Prometheus sample slice into a LiveStatus. It fills
// only the metric-derived fields; the caller sets Running / ScrapedAt / Error.
// Connections are returned sorted by conn_index for deterministic output.
func FoldLive(samples []Sample) LiveStatus {
	var live LiveStatus
	hasQUIC := false

	type connAcc struct {
		rtt  float64
		lost float64
		loc  string
	}
	conns := map[int]*connAcc{}
	getConn := func(idxStr string) *connAcc {
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			return nil
		}
		c := conns[idx]
		if c == nil {
			c = &connAcc{}
			conns[idx] = c
		}
		return c
	}

	for _, sm := range samples {
		if strings.HasPrefix(sm.Name, "quic_client_") {
			hasQUIC = true
		}
		switch sm.Name {
		case "cloudflared_tunnel_ha_connections":
			live.HAConnections = int(sm.Value)
		case "cloudflared_tunnel_total_requests":
			live.RequestsTotal = int64(sm.Value)
		case "cloudflared_tunnel_request_errors":
			live.RequestErrors = int64(sm.Value)
		case "cloudflared_tunnel_response_by_code":
			if c := sm.Labels["status_code"]; len(c) == 3 && c[0] == '5' {
				live.Response5xx += int64(sm.Value)
			}
		case "go_goroutines":
			live.Goroutines = int(sm.Value)
		case "process_resident_memory_bytes":
			live.ResidentMemoryBytes = int64(sm.Value)
		case "build_info", "cloudflared_build_info":
			if v := sm.Labels["version"]; v != "" {
				live.Version = v
			}
		case "quic_client_smoothed_rtt":
			if c := getConn(sm.Labels["conn_index"]); c != nil {
				c.rtt = sm.Value
			}
		case "quic_client_lost_packets":
			if c := getConn(sm.Labels["conn_index"]); c != nil {
				c.lost += sm.Value
			}
		case "cloudflared_tunnel_server_locations":
			if c := getConn(sm.Labels["conn_index"]); c != nil {
				if loc := sm.Labels["edge_location"]; loc != "" {
					c.loc = loc
				}
			}
		}
	}

	idxs := make([]int, 0, len(conns))
	for idx := range conns {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	for _, idx := range idxs {
		c := conns[idx]
		live.Connections = append(live.Connections, EdgeConnection{
			ConnIndex:   idx,
			Location:    c.loc,
			RTT:         c.rtt,
			LostPackets: c.lost,
		})
	}

	switch {
	case hasQUIC:
		live.Protocol = "quic"
	case live.HAConnections > 0:
		live.Protocol = "http2"
	default:
		live.Protocol = "unknown"
	}
	return live
}
