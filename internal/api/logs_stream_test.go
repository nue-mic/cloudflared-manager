package api

import (
	"encoding/json"
	"testing"

	"github.com/mia-clark/cloudflared-manager/internal/logtail"
)

// The WS /logs/stream contract is one JSON frame {"entries":[Entry,...]} with
// snake_case Entry fields. The frontend binding depends on these exact keys.
func TestMarshalEntriesFrame_ShapeAndFields(t *testing.T) {
	ci := 2
	entries := []logtail.Entry{
		{Seq: 7, Level: "warn", Message: "retrying connection", Source: "stderr", Raw: "{}", ConnIndex: &ci},
	}
	b, err := marshalEntriesFrame(entries)
	if err != nil {
		t.Fatalf("marshalEntriesFrame: %v", err)
	}

	var frame struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(b, &frame); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if len(frame.Entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(frame.Entries))
	}
	e := frame.Entries[0]
	if e["seq"].(float64) != 7 {
		t.Errorf("seq = %v, want 7", e["seq"])
	}
	if e["level"] != "warn" {
		t.Errorf("level = %v, want warn", e["level"])
	}
	if e["message"] != "retrying connection" {
		t.Errorf("message = %v, want %q", e["message"], "retrying connection")
	}
	if e["source"] != "stderr" {
		t.Errorf("source = %v, want stderr", e["source"])
	}
	if e["conn_index"].(float64) != 2 {
		t.Errorf("conn_index = %v, want 2", e["conn_index"])
	}
}
