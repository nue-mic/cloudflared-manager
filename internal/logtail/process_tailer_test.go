package logtail_test

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mia-clark/cloudflared-manager/internal/logtail"
)

func TestParse_JSON_HappyPath(t *testing.T) {
	p := logtail.NewProcessTailer("inst-1", 100)
	defer p.Stop()
	r := strings.NewReader(`{"level":"info","time":"2026-06-07T10:00:00Z","message":"hello","event":42,"connIndex":2}` + "\n")
	p.Attach(nil, r)
	time.Sleep(50 * time.Millisecond)
	got := p.Snapshot(logtail.Filter{}, 0)
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	e := got[0]
	if e.Level != "info" || e.Message != "hello" || e.Event != 42 {
		t.Errorf("unexpected entry: %+v", e)
	}
	if e.ConnIndex == nil || *e.ConnIndex != 2 {
		t.Errorf("conn_index: %v", e.ConnIndex)
	}
	if e.Source != "stderr" {
		t.Errorf("source=%q", e.Source)
	}
}

func TestParse_RawFallback_NonJSON(t *testing.T) {
	p := logtail.NewProcessTailer("inst-1", 100)
	defer p.Stop()
	p.Attach(strings.NewReader("not a json line\n"), nil)
	time.Sleep(50 * time.Millisecond)
	got := p.Snapshot(logtail.Filter{}, 0)
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	e := got[0]
	if e.Level != "unknown" {
		t.Errorf("level=%q want unknown", e.Level)
	}
	if e.Message != "not a json line" || e.Raw != "not a json line" {
		t.Errorf("entry=%+v", e)
	}
}

func TestParse_ZerologConsole(t *testing.T) {
	// cloudflared 2026.x emits zerolog console TEXT (not JSON) even with
	// TUNNEL_OUTPUT=json: "<RFC3339> <LVL> <message...>". The tailer must
	// extract the real level/time/message instead of dumping the whole line
	// as an "unknown" entry whose message duplicates the timestamp+level.
	cases := []struct {
		raw     string
		level   string
		message string
	}{
		{"2026-06-12T15:55:05Z INF Starting tunnel tunnelID=61f2a54b", "info", "Starting tunnel tunnelID=61f2a54b"},
		{"2026-06-12T15:55:05.123Z WRN retrying connection", "warn", "retrying connection"},
		{"2026-06-12T15:55:05Z ERR boom err=eof", "error", "boom err=eof"},
		{"2026-06-12T15:55:05Z DBG verbose", "debug", "verbose"},
		{"2026-06-12T15:55:05Z FTL fatal stop", "fatal", "fatal stop"},
	}
	for _, c := range cases {
		p := logtail.NewProcessTailer("z", 100)
		p.Attach(strings.NewReader(c.raw+"\n"), nil)
		time.Sleep(40 * time.Millisecond)
		got := p.Snapshot(logtail.Filter{}, 0)
		p.Stop()
		if len(got) != 1 {
			t.Fatalf("%q: len=%d want 1", c.raw, len(got))
		}
		e := got[0]
		if e.Level != c.level {
			t.Errorf("%q: level=%q want %q", c.raw, e.Level, c.level)
		}
		if e.Message != c.message {
			t.Errorf("%q: message=%q want %q", c.raw, e.Message, c.message)
		}
		if y := e.Time.Year(); y != 2026 {
			t.Errorf("%q: time not parsed, year=%d", c.raw, y)
		}
	}
}

func TestParse_RawFallback_MalformedJSON(t *testing.T) {
	p := logtail.NewProcessTailer("inst-1", 100)
	defer p.Stop()
	p.Attach(strings.NewReader(`{"level":"info"`+"\n"), nil)
	time.Sleep(50 * time.Millisecond)
	got := p.Snapshot(logtail.Filter{}, 0)
	if len(got) != 1 || got[0].Level != "unknown" {
		t.Errorf("entry=%+v", got)
	}
}

func TestNormaliseLevel_Warning(t *testing.T) {
	p := logtail.NewProcessTailer("inst-1", 100)
	defer p.Stop()
	p.Attach(nil, strings.NewReader(`{"level":"warning","message":"old style"}`+"\n"))
	time.Sleep(50 * time.Millisecond)
	got := p.Snapshot(logtail.Filter{}, 0)
	if len(got) != 1 || got[0].Level != "warn" {
		t.Errorf("normalise warning→warn failed: %+v", got)
	}
}

func TestRing_OverwriteOldest(t *testing.T) {
	p := logtail.NewProcessTailer("inst-1", 3)
	defer p.Stop()
	// feed 5 lines into a ring of 3
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString(`{"message":"m`)
		b.WriteByte(byte('0' + i))
		b.WriteString(`"}` + "\n")
	}
	p.Attach(strings.NewReader(b.String()), nil)
	time.Sleep(80 * time.Millisecond)
	got := p.Snapshot(logtail.Filter{}, 0)
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	if got[0].Message != "m2" || got[2].Message != "m4" {
		t.Errorf("ring order wrong: %+v", got)
	}
}

func TestFilter_MinLevel(t *testing.T) {
	p := logtail.NewProcessTailer("inst-1", 100)
	defer p.Stop()
	p.Attach(nil, strings.NewReader(
		`{"level":"debug","message":"d"}`+"\n"+
			`{"level":"info","message":"i"}`+"\n"+
			`{"level":"warn","message":"w"}`+"\n"+
			`{"level":"error","message":"e"}`+"\n",
	))
	time.Sleep(50 * time.Millisecond)
	got := p.Snapshot(logtail.Filter{MinLevel: "warn"}, 0)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].Level != "warn" || got[1].Level != "error" {
		t.Errorf("got=%+v", got)
	}
}

func TestFilter_Keyword(t *testing.T) {
	p := logtail.NewProcessTailer("inst-1", 100)
	defer p.Stop()
	p.Attach(nil, strings.NewReader(
		`{"message":"connect attempt"}`+"\n"+
			`{"message":"disconnected"}`+"\n"+
			`{"message":"heartbeat"}`+"\n",
	))
	time.Sleep(50 * time.Millisecond)
	got := p.Snapshot(logtail.Filter{Keyword: "connect"}, 0)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 (connect attempt + disconnected)", len(got))
	}
}

func TestSubscribe_Live(t *testing.T) {
	p := logtail.NewProcessTailer("inst-1", 100)
	defer p.Stop()
	ch, unsub := p.Subscribe(logtail.Filter{})
	defer unsub()
	pr, pw := io.Pipe()
	p.Attach(pr, nil)
	go func() {
		_, _ = pw.Write([]byte(`{"level":"info","message":"live"}` + "\n"))
		_ = pw.Close()
	}()
	select {
	case e := <-ch:
		if e.Message != "live" {
			t.Errorf("entry=%+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive entry in 1s")
	}
}

func TestOnExit(t *testing.T) {
	p := logtail.NewProcessTailer("inst-1", 100)
	defer p.Stop()
	p.OnExit(nil) // nil state → no exit code suffix
	got := p.Snapshot(logtail.Filter{}, 0)
	if len(got) != 1 || got[0].Source != "daemon" || got[0].Level != "info" {
		t.Errorf("expected daemon entry, got %+v", got)
	}
}

// TestWrite_LineSplit verifies that Write correctly buffers partial lines
// across calls and emits one Entry per complete newline-terminated line.
func TestWrite_LineSplit(t *testing.T) {
	p := logtail.NewProcessTailer("inst-write", 100)
	defer p.Stop()

	// First write: a complete JSON line terminated with \n.
	_, _ = p.Write([]byte("{\"level\":\"info\",\"message\":\"first\"}\n"))
	// Second write: another complete JSON line terminated with \n, plus a
	// partial line without a terminator that must NOT yet appear in the ring.
	_, _ = p.Write([]byte("{\"level\":\"warn\",\"message\":\"second\"}\n"))
	_, _ = p.Write([]byte("partial no newline"))

	got := p.Snapshot(logtail.Filter{}, 0)
	// Both complete lines should be in the ring; the partial write must not.
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(got), got)
	}
	if got[0].Source != "stream" {
		t.Errorf("source[0]=%q want stream", got[0].Source)
	}
	if got[0].Level != "info" || got[0].Message != "first" {
		t.Errorf("entry[0]=%+v", got[0])
	}
	if got[1].Level != "warn" || got[1].Message != "second" {
		t.Errorf("entry[1]=%+v", got[1])
	}

	// Flush the partial line by appending a newline.
	_, _ = p.Write([]byte("\n"))
	got2 := p.Snapshot(logtail.Filter{}, 0)
	if len(got2) != 3 {
		t.Fatalf("expected 3 entries after flush, got %d", len(got2))
	}
	if got2[2].Raw != "partial no newline" {
		t.Errorf("flushed entry raw=%q", got2[2].Raw)
	}
}

// TestWrite_StoppedNoop verifies that Write on a stopped tailer is a
// no-op (returns len(b), nil) and does not add entries to the ring.
func TestWrite_StoppedNoop(t *testing.T) {
	p := logtail.NewProcessTailer("inst-stopped", 100)
	p.Stop()
	n, err := p.Write([]byte("hello\n"))
	if err != nil {
		t.Fatalf("Write on stopped tailer: %v", err)
	}
	if n != 6 {
		t.Errorf("n=%d want 6", n)
	}
	if got := p.Snapshot(logtail.Filter{}, 0); len(got) != 0 {
		t.Errorf("stopped tailer should have empty ring, got %d entries", len(got))
	}
}
