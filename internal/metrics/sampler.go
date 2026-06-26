package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/nue-mic/cloudflared-manager/internal/eventbus"
)

// InstanceSource is the subset of the Manager the sampler needs.
//
// PR-07: MetricsAddr returns the per-instance cloudflared --metrics
// 127.0.0.1:<port> address. It returns ok=false when the instance is
// not running or has no port assigned yet (PR-08 wires the actual
// port allocator into instance.start()).
type InstanceSource interface {
	RunningIDs() []string
	MetricsAddr(id string) (addr string, ok bool)
}

// Sampler periodically scrapes each running instance's cloudflared
// /metrics endpoint, decodes the Prometheus text payload via
// ParsePromText, computes interval deltas for counter-type metrics,
// writes a small TrafficPoint per (instance, scope, key), and evaluates
// the alert rules each tick.
//
// NOTE on the TrafficPoint In/Out columns: cloudflared exposes no per-tunnel
// byte counter, so the server-scope "In" carries the HTTP request-count
// delta and "Out" the error-count delta (NOT bytes). The alert metrics are
// named accordingly: conns | requests_rate | errors_rate (the older
// traffic_in_rate / traffic_out_rate names are accepted as aliases).
type Sampler struct {
	store    *Store
	src      InstanceSource
	bus      *eventbus.Bus
	log      *slog.Logger
	interval time.Duration
	client   *http.Client

	// prev tracks cumulative counter values per (instance|scope|key)
	// for delta computation between ticks.
	prev map[string]promCum
	// alert state per rule id (kept for PR-08).
	alerts map[string]*alertState
	// retention window; points older than this are pruned.
	retain time.Duration
}

// promCum holds the last counter values we observed; PR-07 only needs
// generic "in / out / count" shapes because that is what the existing
// TrafficPoint schema carries.
type promCum struct{ in, out, conns int64 }

type alertState struct {
	firingSince int64
	fired       bool
}

// NewSampler builds a sampler. interval<=0 defaults to 10s; retain<=0 to 7d.
func NewSampler(store *Store, src InstanceSource, bus *eventbus.Bus, log *slog.Logger, interval, retain time.Duration) *Sampler {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if retain <= 0 {
		retain = 7 * 24 * time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	return &Sampler{
		store:    store,
		src:      src,
		bus:      bus,
		log:      log,
		interval: interval,
		client:   &http.Client{Timeout: 4 * time.Second},
		prev:     map[string]promCum{},
		alerts:   map[string]*alertState{},
		retain:   retain,
	}
}

// Run blocks, sampling every interval until ctx is cancelled.
func (s *Sampler) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick()
		case <-prune.C:
			cutoff := time.Now().Add(-s.retain).Unix()
			if n, err := s.store.PruneBefore(cutoff); err == nil && n > 0 {
				s.log.Debug("pruned old traffic points", slog.Int64("rows", n))
			}
		}
	}
}

// tick performs one sampling pass across running instances. It scrapes each
// instance's cloudflared /metrics endpoint, converts the Prometheus samples
// to TrafficPoints, persists them, and evaluates alert rules.
func (s *Sampler) tick() {
	now := time.Now().Unix()
	stepSec := int64(s.interval / time.Second)
	if stepSec <= 0 {
		stepSec = 1
	}
	rules, _ := s.store.ListRules()
	points := make([]TrafficPoint, 0, 16)

	for _, id := range s.src.RunningIDs() {
		addr, ok := s.src.MetricsAddr(id)
		if !ok {
			continue
		}
		samples, err := Scrape(addr)
		if err != nil {
			s.log.Debug("metrics scrape failed", slog.String("id", id), slog.Any("err", err))
			continue
		}
		instPoints := s.toPoints(id, samples, now)
		points = append(points, instPoints...)
		// Evaluate alert rules using the first (server-scope) point.
		if len(instPoints) > 0 {
			sp := instPoints[0]
			s.evalRules(rules, id, "", sp.Conns, sp, stepSec, now)
		}
	}

	if len(points) > 0 {
		if err := s.store.InsertTraffic(points); err != nil {
			s.log.Warn("insert traffic failed", slog.Any("err", err))
		}
	}
}

// toPoints folds the spec §5.2 "minimum 12" metrics into TrafficPoint
// rows. The data model carries In/Out/Conns columns, so we fan out:
//   - server-scope: requests_total (in), response_5xx (out), ha_connections (conns)
//   - edge_conn-scope (per conn_index): smoothed_rtt (in), lost_packets (out)
//
// This is a deliberate squeeze of richer telemetry into the legacy
// schema; PR-08 will likely introduce a wider points table, at which
// point toPoints is rewritten one-to-one against the cleaner shape.
func (s *Sampler) toPoints(id string, samples []Sample, now int64) []TrafficPoint {
	out := make([]TrafficPoint, 0, 8)

	// gauges + counters of interest
	var haConn, goroutines, residentMem float64
	var totalReq, totalErrors, total5xx float64
	rttByConn := map[string]float64{}
	lostByConn := map[string]float64{}

	for _, sm := range samples {
		switch sm.Name {
		case "cloudflared_tunnel_ha_connections":
			haConn = sm.Value
		case "cloudflared_tunnel_total_requests":
			totalReq = sm.Value
		case "cloudflared_tunnel_request_errors":
			totalErrors = sm.Value
		case "cloudflared_tunnel_response_by_code":
			if c := sm.Labels["status_code"]; len(c) == 3 && c[0] == '5' {
				total5xx += sm.Value
			}
		case "quic_client_smoothed_rtt":
			rttByConn[sm.Labels["conn_index"]] = sm.Value
		case "quic_client_lost_packets":
			lostByConn[sm.Labels["conn_index"]] += sm.Value
		case "go_goroutines":
			goroutines = sm.Value
		case "process_resident_memory_bytes":
			residentMem = sm.Value
		}
	}

	// server scope: ha_connections (gauge → Conns), total_requests delta → In, errors delta → Out
	curServer := promCum{in: int64(totalReq), out: int64(totalErrors + total5xx), conns: int64(haConn)}
	out = append(out, s.delta(id, "server", "", curServer, int64(haConn), now))

	// edge_conn scope: one TrafficPoint per conn_index, In = rtt, Out = lost
	for idx, rtt := range rttByConn {
		lost := lostByConn[idx]
		cur := promCum{in: int64(rtt), out: int64(lost), conns: 1}
		out = append(out, s.delta(id, "edge_conn", idx, cur, 1, now))
	}

	_ = goroutines
	_ = residentMem
	return out
}

// delta turns a cumulative reading into a TrafficPoint with incremental
// In/Out (Counter semantics) and absolute Conns (Gauge). A negative
// delta — typical when a counter resets after cloudflared restart — is
// clamped to 0 to avoid surprising negatives in the chart.
func (s *Sampler) delta(id, scope, key string, cur promCum, conns int64, now int64) TrafficPoint {
	k := id + "|" + scope + "|" + key
	prev, seen := s.prev[k]
	var dIn, dOut int64
	if seen {
		if cur.in >= prev.in {
			dIn = cur.in - prev.in
		}
		if cur.out >= prev.out {
			dOut = cur.out - prev.out
		}
	}
	s.prev[k] = cur
	return TrafficPoint{Ts: now, InstID: id, Scope: scope, Key: key, In: dIn, Out: dOut, Conns: conns}
}

// evalRules / applyRule / publishAlert / postWebhook are kept dormant
// for PR-08 to re-enable. Lint guards prevent the "declared and not
// used" error.

func (s *Sampler) evalRules(rules []AlertRule, instID, target string, conns int64, pt TrafficPoint, stepSec, now int64) {
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if r.InstID != "*" && r.InstID != instID {
			continue
		}
		ruleTarget := r.Target
		if ruleTarget == "*" {
			ruleTarget = ""
		}
		if ruleTarget != target {
			continue
		}
		var value float64
		switch r.Metric {
		case "conns":
			value = float64(conns)
		case "requests_rate", "traffic_in_rate": // pt.In carries request-count deltas
			value = float64(pt.In) / float64(stepSec)
		case "errors_rate", "traffic_out_rate": // pt.Out carries error-count deltas
			value = float64(pt.Out) / float64(stepSec)
		default:
			continue
		}
		s.applyRule(r, instID, target, value, now)
	}
}

func (s *Sampler) applyRule(r AlertRule, instID, target string, value float64, now int64) {
	st := s.alerts[r.ID]
	if st == nil {
		st = &alertState{}
		s.alerts[r.ID] = st
	}
	breached := compare(value, r.Op, r.Threshold)
	if breached {
		if st.firingSince == 0 {
			st.firingSince = now
		}
		held := now - st.firingSince
		if !st.fired && held >= int64(r.ForSeconds) {
			st.fired = true
			ev := AlertEvent{
				ID:     fmt.Sprintf("ae_%s_%d", r.ID, now),
				RuleID: r.ID, InstID: instID, Target: target,
				FiredAt: now, Value: value, State: "firing",
			}
			_ = s.store.InsertEvent(ev)
			s.publishAlert(ev, r)
		}
	} else {
		if st.fired {
			st.fired = false
			_ = s.store.ResolveEvent(r.ID, now)
			ev := AlertEvent{
				ID:     fmt.Sprintf("ae_%s_%d_r", r.ID, now),
				RuleID: r.ID, InstID: instID, Target: target,
				FiredAt: st.firingSince, ResolvedAt: now, Value: value, State: "resolved",
			}
			s.publishAlert(ev, r)
		}
		st.firingSince = 0
	}
}

func compare(v float64, op string, th float64) bool {
	switch op {
	case ">":
		return v > th
	case ">=":
		return v >= th
	case "<":
		return v < th
	case "<=":
		return v <= th
	}
	return false
}

func (s *Sampler) publishAlert(ev AlertEvent, r AlertRule) {
	if s.bus != nil {
		s.bus.Publish(eventbus.TypeAlert, ev.InstID, map[string]any{
			"rule_id": ev.RuleID, "rule_name": r.Name, "target": ev.Target,
			"state": ev.State, "value": ev.Value, "threshold": r.Threshold,
			"metric": r.Metric, "fired_at": ev.FiredAt, "resolved_at": ev.ResolvedAt,
		})
	}
	if r.Webhook != "" {
		go s.postWebhook(r.Webhook, ev, r)
	}
}

func (s *Sampler) postWebhook(url string, ev AlertEvent, r AlertRule) {
	payload, _ := json.Marshal(map[string]any{
		"rule_id": ev.RuleID, "rule_name": r.Name, "inst_id": ev.InstID,
		"target": ev.Target, "metric": r.Metric, "op": r.Op, "threshold": r.Threshold,
		"value": ev.Value, "state": ev.State, "fired_at": ev.FiredAt, "resolved_at": ev.ResolvedAt,
	})
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Warn("alert webhook failed", slog.String("rule", r.ID), slog.Any("err", err))
		return
	}
	_ = resp.Body.Close()
}
