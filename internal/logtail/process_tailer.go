package logtail

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProcessTailer captures stdout + stderr from a single child process,
// parses each line as cloudflared's --output=json structured log when
// possible, and fans the resulting Entry stream out to subscribers.
//
// Unlike the file-oriented Tailer in this package, ProcessTailer
// terminates on its own when both pipes hit EOF — there is no "follow
// forever" semantic because a process exits and its output ends.
type ProcessTailer struct {
	instanceID string
	ringSize   int

	mu   sync.Mutex
	ring []Entry
	head int // next write index when ring is full
	full bool

	subs []*subscriber

	seq atomic.Uint64

	stopped atomic.Bool

	// writeMu serializes Write so the two io.Copy goroutines (stdout +
	// stderr) cannot race on the tail buffer. It must NOT be taken inside
	// append() — append() locks mu, and Write calls append() while holding
	// writeMu, so the two locks are strictly ordered writeMu → mu.
	writeMu sync.Mutex
	// tail holds bytes received via Write that have not yet been
	// terminated by a newline; flushed on the next Write call.
	tail []byte
}

// Entry is one structured log line as surfaced to subscribers and HTTP
// callers. Fields originally absent from cloudflared's JSON output stay
// at their zero value.
type Entry struct {
	Seq       uint64         `json:"seq"`
	Time      time.Time      `json:"time"`
	Level     string         `json:"level"` // info|warn|error|fatal|debug|unknown
	Message   string         `json:"message"`
	Event     int            `json:"event,omitempty"`
	ConnIndex *int           `json:"conn_index,omitempty"`
	TunnelID  string         `json:"tunnel_id,omitempty"`
	Raw       string         `json:"raw"`
	Fields    map[string]any `json:"fields,omitempty"`
	Source    string         `json:"source"` // "stderr" | "stdout" | "daemon"
}

// Filter trims the stream pushed to a subscriber. A zero Filter
// accepts everything.
type Filter struct {
	MinLevel  string    // "debug" / "info" / "warn" / "error" / "fatal" — empty = accept all
	Keyword   string    // substring match on Message + Raw (case-insensitive)
	Events    []int     // any-of match; empty = accept all
	ConnIndex *int      // exact match; nil = accept all
	Since     time.Time // accept entries strictly newer; zero = accept all
}

type subscriber struct {
	ch     chan Entry
	filter Filter
}

// NewProcessTailer creates a ProcessTailer for a single instance. ringSize defaults
// to 8000 when 0; callers may pass smaller values for memory-sensitive
// scenarios (spec §6.4: CFDM_LOG_RING_SIZE knob).
func NewProcessTailer(instanceID string, ringSize int) *ProcessTailer {
	if ringSize <= 0 {
		ringSize = 8000
	}
	return &ProcessTailer{
		instanceID: instanceID,
		ringSize:   ringSize,
		ring:       make([]Entry, 0, ringSize),
	}
}

// Attach starts two goroutines that drain stdout and stderr line by
// line. Calling Attach more than once is a no-op after Stop has been
// called; otherwise additional Attaches add more sources to the same
// fanout (rare but supported).
func (p *ProcessTailer) Attach(stdout, stderr io.Reader) {
	if p.stopped.Load() {
		return
	}
	if stdout != nil {
		go p.pump(stdout, "stdout")
	}
	if stderr != nil {
		go p.pump(stderr, "stderr")
	}
}

// pump reads source line by line and forwards each as an Entry.
// bufio.Scanner is configured with a 1 MiB ceiling so cloudflared
// debug-mode header dumps (8–32 KiB lines) do not trigger ErrTooLong.
func (p *ProcessTailer) pump(r io.Reader, source string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if p.stopped.Load() {
			return
		}
		raw := sc.Text()
		p.append(parseLine(raw, source))
	}
}

// parseLine converts one raw text line into a structured Entry.
// JSON-first, raw fallback: malformed JSON or non-JSON text becomes an
// Entry with Level="unknown" and Message=Raw=raw.
func parseLine(raw, source string) Entry {
	e := Entry{
		Source:  source,
		Raw:     raw,
		Time:    time.Now().UTC(),
		Level:   "unknown",
		Message: raw,
	}

	trim := strings.TrimSpace(raw)
	if len(trim) == 0 || trim[0] != '{' {
		// Not JSON. cloudflared 2026.x emits zerolog console TEXT even with
		// TUNNEL_OUTPUT=json, so try that shape before giving up to "unknown".
		tryParseZerolog(trim, &e)
		return e
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(trim), &m); err != nil {
		return e
	}

	// At this point JSON parsed successfully; extract well-known fields
	// and tuck the remainder into Fields for the UI's "expand JSON".
	if v, ok := m["level"].(string); ok {
		e.Level = normaliseLevel(v)
	} else {
		e.Level = "info" // JSON without an explicit level → info
	}
	if v, ok := m["time"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			e.Time = t.UTC()
		}
	}
	if v, ok := m["message"].(string); ok {
		e.Message = v
	}
	if v, ok := m["event"].(float64); ok {
		e.Event = int(v)
	}
	if v, ok := m["connIndex"].(float64); ok {
		ci := int(v)
		e.ConnIndex = &ci
	}
	if v, ok := m["tunnelID"].(string); ok {
		e.TunnelID = v
	}
	// Stash the full decoded map so consumers can inspect any field
	// cloudflared adds in a future version without parser changes.
	e.Fields = m

	return e
}

// normaliseLevel maps cloudflared's historical level vocabulary into
// the canonical set used by Filter.MinLevel.
func normaliseLevel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "warn", "warning":
		return "warn"
	case "err", "error":
		return "error"
	case "fatal", "panic":
		return "fatal"
	case "debug", "trace":
		return "debug"
	case "info":
		return "info"
	default:
		return "unknown"
	}
}

// tryParseZerolog parses cloudflared's zerolog console text format
// "<RFC3339> <LVL> <message...>". On a match it overwrites e.Time / e.Level /
// e.Message with the extracted values and returns true; otherwise it leaves e
// untouched (so genuinely free-form text stays an "unknown" entry whose
// message is the whole raw line).
func tryParseZerolog(line string, e *Entry) bool {
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return false
	}
	lvl := zerologLevel(parts[1])
	if lvl == "" {
		return false
	}
	e.Time = t.UTC()
	e.Level = lvl
	if len(parts) == 3 {
		e.Message = parts[2]
	} else {
		e.Message = ""
	}
	return true
}

// zerologLevel maps zerolog's 3-letter console abbreviations (and the full
// words, defensively) onto the canonical level set. Returns "" when the token
// is not a recognised level, so an arbitrary line that merely starts with an
// RFC3339 timestamp is not misclassified.
func zerologLevel(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "TRC", "TRACE", "DBG", "DEBUG":
		return "debug"
	case "INF", "INFO":
		return "info"
	case "WRN", "WARN", "WARNING":
		return "warn"
	case "ERR", "ERROR":
		return "error"
	case "FTL", "FATAL", "PNC", "PANIC":
		return "fatal"
	}
	return ""
}

// append assigns the next Seq, inserts the Entry into the ring, and
// fans out to subscribers whose filter matches.
func (p *ProcessTailer) append(e Entry) {
	e.Seq = p.seq.Add(1)
	p.mu.Lock()
	if len(p.ring) < p.ringSize {
		p.ring = append(p.ring, e)
	} else {
		p.ring[p.head] = e
		p.head = (p.head + 1) % p.ringSize
		p.full = true
	}
	subs := append([]*subscriber(nil), p.subs...)
	p.mu.Unlock()

	for _, s := range subs {
		if !match(e, s.filter) {
			continue
		}
		select {
		case s.ch <- e:
		default: // subscriber slow → drop oldest in their channel
			select {
			case <-s.ch:
			default:
			}
			select {
			case s.ch <- e:
			default:
			}
		}
	}
}

// match reports whether e survives filter.
func match(e Entry, f Filter) bool {
	if f.MinLevel != "" && levelRank(e.Level) < levelRank(f.MinLevel) {
		return false
	}
	if f.Keyword != "" {
		needle := strings.ToLower(f.Keyword)
		if !strings.Contains(strings.ToLower(e.Message), needle) && !strings.Contains(strings.ToLower(e.Raw), needle) {
			return false
		}
	}
	if len(f.Events) > 0 {
		hit := false
		for _, ev := range f.Events {
			if e.Event == ev {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if f.ConnIndex != nil {
		if e.ConnIndex == nil || *e.ConnIndex != *f.ConnIndex {
			return false
		}
	}
	if !f.Since.IsZero() && !e.Time.After(f.Since) {
		return false
	}
	return true
}

// filterIsZero reports whether f is the zero Filter (accept everything).
// Cannot use f == Filter{} because Filter contains a slice field.
func filterIsZero(f Filter) bool {
	return f.MinLevel == "" && f.Keyword == "" && len(f.Events) == 0 && f.ConnIndex == nil && f.Since.IsZero()
}

// levelRank maps a textual level into an integer for >= comparison.
// Unknown levels rank lowest so they never satisfy a MinLevel filter
// (they still show up when no filter is set).
func levelRank(s string) int {
	switch strings.ToLower(s) {
	case "debug":
		return 1
	case "info":
		return 2
	case "warn":
		return 3
	case "error":
		return 4
	case "fatal":
		return 5
	}
	return 0
}

// Subscribe registers a new subscriber and returns (ch, unsubscribe).
// The channel is buffered to 4096 entries; on overflow ProcessTailer
// drops the oldest to make room for the new (never blocks the pump).
func (p *ProcessTailer) Subscribe(f Filter) (<-chan Entry, func()) {
	s := &subscriber{ch: make(chan Entry, 4096), filter: f}
	p.mu.Lock()
	p.subs = append(p.subs, s)
	p.mu.Unlock()
	return s.ch, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		for i, x := range p.subs {
			if x == s {
				p.subs = append(p.subs[:i], p.subs[i+1:]...)
				close(s.ch)
				return
			}
		}
	}
}

// Snapshot returns up to limit Entries from the ring that survive f.
// Entries are returned oldest-first (insertion order). limit<=0 means
// "no cap".
func (p *ProcessTailer) Snapshot(f Filter, limit int) []Entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.ring) == 0 {
		return nil
	}
	// Re-linearise the ring: if not full, p.ring is already in order;
	// if full, the oldest entry sits at p.head.
	out := make([]Entry, 0, len(p.ring))
	if !p.full {
		out = append(out, p.ring...)
	} else {
		out = append(out, p.ring[p.head:]...)
		out = append(out, p.ring[:p.head]...)
	}
	if !filterIsZero(f) {
		filtered := out[:0]
		for _, e := range out {
			if match(e, f) {
				filtered = append(filtered, e)
			}
		}
		out = filtered
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// OnExit injects a synthetic Entry describing how the child exited.
// Sourced as "daemon" so UI can render it with a distinct marker.
func (p *ProcessTailer) OnExit(state *os.ProcessState) {
	if p.stopped.Load() {
		return
	}
	msg := "cloudflared exited"
	if state != nil {
		msg = "cloudflared exited code=" + itoa(state.ExitCode())
	}
	p.append(Entry{
		Source:  "daemon",
		Level:   "info",
		Message: msg,
		Raw:     msg,
		Time:    time.Now().UTC(),
	})
}

// Stop marks the tailer dead. Subsequent Attach calls are no-ops and
// pump goroutines exit at their next line boundary.
func (p *ProcessTailer) Stop() {
	if !p.stopped.CompareAndSwap(false, true) {
		return
	}
	p.mu.Lock()
	for _, s := range p.subs {
		close(s.ch)
	}
	p.subs = nil
	p.mu.Unlock()
}

// Write implements io.Writer so that ProcessTailer can be used directly as a
// LogSink in process.SpawnParams (via io.MultiWriter). Incoming bytes are
// split on '\n'; each complete line is passed through parseLine with source
// "stream". Partial lines are buffered in p.tail and flushed on the next
// Write. If the tailer is stopped, Write drains the call without error to
// avoid blocking the caller.
func (p *ProcessTailer) Write(b []byte) (int, error) {
	if p.stopped.Load() {
		return len(b), nil
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	rem := append(p.tail, b...) //nolint:gocritic // intentional: grow tail
	p.tail = p.tail[:0]
	for {
		i := bytes.IndexByte(rem, '\n')
		if i < 0 {
			p.tail = append(p.tail[:0], rem...)
			return len(b), nil
		}
		line := rem[:i]
		rem = rem[i+1:]
		p.append(parseLine(strings.TrimRight(string(line), "\r"), "stream"))
	}
}

// itoa avoids strconv.Itoa to keep the import set minimal; it handles
// the small int range OnExit produces (exit codes).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
