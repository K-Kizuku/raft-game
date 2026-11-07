package protocol_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

// P-6: testdata/ の見本を DisallowUnknownFields で読めて、Marshal し直すと同じ JSON になる。
func TestTestdataRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		file string
		v    func() any
	}{
		{"snapshot.json", func() any { return &protocol.Snapshot{} }},
		{"letter-raft.json", func() any { return &protocol.Letter{} }},
		{"letter-request.json", func() any { return &protocol.Letter{} }},
		{"request.json", func() any { return &protocol.Request{} }},
		{"node-detail.json", func() any { return &protocol.NodeDetail{} }},
		{"round-result.json", func() any { return &protocol.RoundResult{} }},
		{"events.json", func() any { return &protocol.EventsPayload{} }},
		{"input-log.json", func() any { return &protocol.InputLog{} }},
		{"internal-restore.json", func() any { return &protocol.InternalRestorePayload{} }},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()
			data := readTestdata(t, tt.file)
			v := tt.v()
			if err := decodeStrict(data, v); err != nil {
				t.Fatalf("decode %s into %T: %v", tt.file, v, err)
			}
			out, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			assertSameJSON(t, out, data)
		})
	}
}

// P-7: events.json の各イベントを、type から NewEvent で選んだ型で読める。22 種類がそろっている。
func TestTestdataEvents(t *testing.T) {
	t.Parallel()
	var p protocol.EventsPayload
	if err := decodeStrict(readTestdata(t, "events.json"), &p); err != nil {
		t.Fatalf("decode events.json: %v", err)
	}
	seen := map[protocol.EventType]bool{}
	for i, raw := range p.Events {
		var h struct {
			Type protocol.EventType `json:"type"`
		}
		if err := json.Unmarshal(raw, &h); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		ev, ok := protocol.NewEvent(h.Type)
		if !ok {
			t.Fatalf("event %d: unknown type %q", i, h.Type)
		}
		if err := decodeStrict(raw, ev); err != nil {
			t.Fatalf("event %d (%s): decode into %T: %v", i, h.Type, ev, err)
		}
		out, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("event %d: Marshal: %v", i, err)
		}
		assertSameJSON(t, out, raw)
		seen[h.Type] = true
	}
	for _, typ := range protocol.EventTypes() {
		if !seen[typ] {
			t.Errorf("events.json has no %q", typ)
		}
	}
	if len(p.Events) != len(protocol.EventTypes()) {
		t.Errorf("events.json has %d events, want %d", len(p.Events), len(protocol.EventTypes()))
	}
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// assertSameJSON は got と want を any に読んで比べる（キーの順・空白を無視する）。
func assertSameJSON(t *testing.T, got, want []byte) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("JSON differs\n got: %s\nwant: %s", got, want)
	}
}
