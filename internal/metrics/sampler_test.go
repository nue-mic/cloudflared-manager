package metrics_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nue-mic/cloudflared-manager/internal/eventbus"
	"github.com/nue-mic/cloudflared-manager/internal/metrics"
)

type mockSrc struct {
	mu      sync.Mutex
	running []string
	addrs   map[string]string
}

func (m *mockSrc) RunningIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]string(nil), m.running...)
	return out
}

func (m *mockSrc) MetricsAddr(id string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.addrs[id]
	return a, ok
}

func TestSampler_Run_ScrapesOnce(t *testing.T) {
	body := `cloudflared_tunnel_ha_connections 4
cloudflared_tunnel_total_requests 100
cloudflared_tunnel_response_by_code{status_code="200"} 95
cloudflared_tunnel_response_by_code{status_code="500"} 5
cloudflared_tunnel_request_errors 2
quic_client_smoothed_rtt{conn_index="0"} 23.5
quic_client_lost_packets{conn_index="0"} 0
go_goroutines 50
process_resident_memory_bytes 12345678
`
	hits := 0
	mu := sync.Mutex{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	addr := srv.URL[len("http://"):]

	tmp := t.TempDir()
	store, err := metrics.Open(filepath.Join(tmp, "metrics.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	src := &mockSrc{
		running: []string{"inst-1"},
		addrs:   map[string]string{"inst-1": addr},
	}
	bus := eventbus.New(16)
	s := metrics.NewSampler(store, src, bus, slog.Default(), 100*time.Millisecond, time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	go s.Run(ctx)
	<-ctx.Done()

	mu.Lock()
	got := hits
	mu.Unlock()
	if got < 1 {
		t.Fatalf("scrape never happened (hits=%d)", got)
	}
}

func TestSampler_Run_SkipsNoAddr(t *testing.T) {
	tmp := t.TempDir()
	store, err := metrics.Open(filepath.Join(tmp, "metrics.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	src := &mockSrc{running: []string{"inst-no-port"}}
	bus := eventbus.New(16)
	s := metrics.NewSampler(store, src, bus, slog.Default(), 50*time.Millisecond, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go s.Run(ctx)
	<-ctx.Done()
	// No assertion on the store; the test simply proves Run doesn't
	// panic when MetricsAddr returns ok=false.
}
