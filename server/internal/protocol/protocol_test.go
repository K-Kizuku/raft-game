package protocol_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

// decodeStrict は data を DisallowUnknownFields で v に読む。後ろに余分なものがあればエラー。
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

// P-1
func TestEventTypes(t *testing.T) {
	t.Parallel()
	want := []protocol.EventType{
		"phase.changed",
		"letter.created",
		"letter.moved",
		"letter.delivered",
		"letter.dropped",
		"letter.stale",
		"ferry.departed",
		"ferry.arrived",
		"ferry.broken",
		"ferry.repaired",
		"route.blocked",
		"route.unblocked",
		"forecast",
		"node.state",
		"node.leader_elected",
		"node.stepped_down",
		"request.created",
		"request.updated",
		"ledger.applied",
		"island.updated",
		"score.updated",
		"notice",
	}
	got := protocol.EventTypes()
	if !slices.Equal(got, want) {
		t.Fatalf("EventTypes() = %v, want %v", got, want)
	}
	seen := map[protocol.EventType]bool{}
	for _, e := range got {
		if seen[e] {
			t.Errorf("EventTypes() has duplicate %q", e)
		}
		seen[e] = true
	}
	got[0] = "changed"
	if protocol.EventTypes()[0] != protocol.EventTypePhaseChanged {
		t.Error("EventTypes() shares its slice between calls")
	}
}

// P-2
func TestNewEvent(t *testing.T) {
	t.Parallel()
	for _, typ := range protocol.EventTypes() {
		t.Run(string(typ), func(t *testing.T) {
			t.Parallel()
			ev, ok := protocol.NewEvent(typ)
			if !ok || ev == nil {
				t.Fatalf("NewEvent(%q) = (%v, %v)", typ, ev, ok)
			}
			if reflect.TypeOf(ev).Kind() != reflect.Pointer {
				t.Fatalf("NewEvent(%q) = %T, want a pointer", typ, ev)
			}
			in := `{"seq":1,"t":2,"type":"` + string(typ) + `"}`
			if err := decodeStrict([]byte(in), ev); err != nil {
				t.Fatalf("decode %s into %T: %v", in, ev, err)
			}
			out, err := json.Marshal(ev)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			prefix := `{"seq":1,"t":2,"type":"` + string(typ) + `",`
			if !strings.HasPrefix(string(out), prefix) {
				t.Errorf("Marshal = %s, want prefix %s", out, prefix)
			}
		})
	}
	for _, typ := range []protocol.EventType{"", "letter.foo"} {
		if ev, ok := protocol.NewEvent(typ); ok || ev != nil {
			t.Errorf("NewEvent(%q) = (%v, %v), want (nil, false)", typ, ev, ok)
		}
	}
}

// P-3
func TestMessageTypesAndAckCodes(t *testing.T) {
	t.Parallel()
	wantTypes := []protocol.MessageType{
		"hello", "cmd.game.start", "cmd.game.pause", "cmd.game.speed", "cmd.game.skip",
		"cmd.ferry.load", "cmd.ferry.unload", "cmd.ferry.autoload", "cmd.ferry.depart",
		"cmd.relay.load", "cmd.sync", "sub.engineer", "demo.trigger", "ping",
		"snapshot", "events", "ack", "node.detail", "round.result", "error", "pong",
		"internal.init", "internal.restore", "internal.result", "internal.ping", "internal.pong",
	}
	if got := protocol.MessageTypes(); !slices.Equal(got, wantTypes) {
		t.Errorf("MessageTypes() = %v, want %v", got, wantTypes)
	}
	wantCodes := []protocol.AckCode{
		"ferry_not_docked", "ferry_not_ready", "ferry_broken", "capacity_exceeded", "letter_not_here",
		"no_route", "route_blocked", "phase_invalid", "rate_limited", "bad_request",
	}
	if got := protocol.AckCodes(); !slices.Equal(got, wantCodes) {
		t.Errorf("AckCodes() = %v, want %v", got, wantCodes)
	}
	m := protocol.MessageTypes()
	m[0] = "x"
	c := protocol.AckCodes()
	c[0] = "x"
	if protocol.MessageTypes()[0] != protocol.MessageTypeHello || protocol.AckCodes()[0] != protocol.AckCodeFerryNotDocked {
		t.Error("MessageTypes() or AckCodes() shares its slice between calls")
	}
}

// P-4
func TestDecodeEnvelope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want protocol.Envelope
		err  error
	}{
		{
			name: "client",
			in:   `{"v":1,"type":"cmd.ferry.load","cmdId":"c-12","payload":{"ferryId":"f1","letterIds":[12,13]}}`,
			want: protocol.Envelope{V: 1, Type: "cmd.ferry.load", CmdID: "c-12", Payload: json.RawMessage(`{"ferryId":"f1","letterIds":[12,13]}`)},
		},
		{
			name: "server",
			in:   `{"v":1,"type":"events","seq":42,"payload":{"clock":{}}}`,
			want: protocol.Envelope{V: 1, Type: "events", Seq: 42, Payload: json.RawMessage(`{"clock":{}}`)},
		},
		{name: "payload missing", in: `{"v":1,"type":"cmd.sync"}`, want: protocol.Envelope{V: 1, Type: "cmd.sync", Payload: json.RawMessage(`{}`)}},
		{name: "payload null", in: `{"v":1,"type":"cmd.sync","payload":null}`, want: protocol.Envelope{V: 1, Type: "cmd.sync", Payload: json.RawMessage(`{}`)}},
		{name: "unknown keys", in: `{"v":1,"type":"ping","x":[1],"payload":{}}`, want: protocol.Envelope{V: 1, Type: "ping", Payload: json.RawMessage(`{}`)}},
		{name: "unknown type", in: `{"v":1,"type":"cmd.foo","payload":{}}`, want: protocol.Envelope{V: 1, Type: "cmd.foo", Payload: json.RawMessage(`{}`)}},
		{name: "whitespace", in: " \n{\"v\":1,\"type\":\"ping\",\"payload\":{}}\n", want: protocol.Envelope{V: 1, Type: "ping", Payload: json.RawMessage(`{}`)}},

		{name: "broken json", in: `{"v":1,`, err: protocol.ErrBadEnvelope},
		{name: "empty", in: ``, err: protocol.ErrBadEnvelope},
		{name: "array", in: `[1]`, err: protocol.ErrBadEnvelope},
		{name: "number", in: `1`, err: protocol.ErrBadEnvelope},
		{name: "null", in: `null`, err: protocol.ErrBadEnvelope},
		{name: "trailing", in: `{"v":1,"type":"ping","payload":{}} {}`, err: protocol.ErrBadEnvelope},

		{name: "v missing", in: `{"type":"ping","payload":{}}`, err: protocol.ErrVersionMismatch},
		{name: "v 0", in: `{"v":0,"type":"ping","payload":{}}`, err: protocol.ErrVersionMismatch},
		{name: "v 2", in: `{"v":2,"type":"ping","payload":{}}`, err: protocol.ErrVersionMismatch},
		{name: "v string", in: `{"v":"1","type":"ping","payload":{}}`, err: protocol.ErrVersionMismatch},
		{name: "v null", in: `{"v":null,"type":"ping","payload":{}}`, err: protocol.ErrVersionMismatch},
		{name: "v before type", in: `{"v":2}`, err: protocol.ErrVersionMismatch},

		{name: "type missing", in: `{"v":1,"payload":{}}`, err: protocol.ErrBadEnvelope},
		{name: "type empty", in: `{"v":1,"type":"","payload":{}}`, err: protocol.ErrBadEnvelope},
		{name: "type number", in: `{"v":1,"type":1,"payload":{}}`, err: protocol.ErrBadEnvelope},
		{name: "seq string", in: `{"v":1,"type":"ping","seq":"1","payload":{}}`, err: protocol.ErrBadEnvelope},
		{name: "cmdId number", in: `{"v":1,"type":"ping","cmdId":1,"payload":{}}`, err: protocol.ErrBadEnvelope},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := protocol.DecodeEnvelope([]byte(tt.in))
			if tt.err != nil {
				if !errors.Is(err, tt.err) {
					t.Fatalf("DecodeEnvelope(%s) error = %v, want %v", tt.in, err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeEnvelope(%s): %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DecodeEnvelope(%s) = %+v (payload %s), want %+v (payload %s)", tt.in, got, got.Payload, tt.want, tt.want.Payload)
			}
		})
	}
}

// P-5
func TestEncodeEnvelope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		typ     protocol.MessageType
		seq     uint64
		cmdID   string
		payload any
		want    string
	}{
		{name: "nil payload", typ: "cmd.sync", want: `{"v":1,"type":"cmd.sync","payload":{}}`},
		{name: "seq", typ: "ack", seq: 3, payload: protocol.Ack{CmdID: "c-1", OK: true, T: 61200},
			want: `{"v":1,"type":"ack","seq":3,"payload":{"cmdId":"c-1","ok":true,"t":61200}}`},
		{name: "cmdId", typ: "cmd.ferry.depart", cmdID: "c-2", payload: protocol.FerryToPayload{FerryID: "f1", To: 2},
			want: `{"v":1,"type":"cmd.ferry.depart","cmdId":"c-2","payload":{"ferryId":"f1","to":2}}`},
		{name: "both", typ: "pong", seq: 9, cmdID: "c-3", payload: protocol.PongPayload{ClientTime: 1.5, SimTime: 2},
			want: `{"v":1,"type":"pong","seq":9,"cmdId":"c-3","payload":{"clientTime":1.5,"simTime":2}}`},
		{name: "empty struct", typ: "internal.ping", payload: struct{}{}, want: `{"v":1,"type":"internal.ping","payload":{}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := protocol.EncodeEnvelope(tt.typ, tt.seq, tt.cmdID, tt.payload)
			if err != nil {
				t.Fatalf("EncodeEnvelope: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("EncodeEnvelope = %s, want %s", got, tt.want)
			}
			env, err := protocol.DecodeEnvelope(got)
			if err != nil {
				t.Fatalf("DecodeEnvelope: %v", err)
			}
			if env.V != protocol.Version || env.Type != tt.typ || env.Seq != tt.seq || env.CmdID != tt.cmdID {
				t.Errorf("round trip = %+v", env)
			}
			wantPayload := []byte(`{}`)
			if tt.payload != nil {
				wantPayload, _ = json.Marshal(tt.payload)
			}
			if !bytes.Equal(env.Payload, wantPayload) {
				t.Errorf("round trip payload = %s, want %s", env.Payload, wantPayload)
			}
		})
	}

	for _, bad := range []any{[]int{}, 1, "x", (*protocol.Ack)(nil), map[string]any{"x": make(chan int)}} {
		if got, err := protocol.EncodeEnvelope("ack", 1, "", bad); err == nil {
			t.Errorf("EncodeEnvelope(%#v) = %s, want an error", bad, got)
		}
	}
}
