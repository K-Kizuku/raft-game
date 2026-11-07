package simconv_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// decodeStrict は data を DisallowUnknownFields で v に読む。
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

// C-2
func TestDecodeEngineCommand(t *testing.T) {
	t.Parallel()
	ok := []struct {
		typ     protocol.MessageType
		payload string
		want    sim.Command
	}{
		{"cmd.game.start", "", sim.Command{Type: sim.CmdGameStart}},
		{"cmd.game.start", "null", sim.Command{Type: sim.CmdGameStart}},
		{"cmd.game.start", "{}", sim.Command{Type: sim.CmdGameStart}},
		{"cmd.game.start", `{"x":1}`, sim.Command{Type: sim.CmdGameStart}},
		{"cmd.ferry.load", `{"ferryId":"f1","letterIds":[12,13]}`, sim.Command{Type: sim.CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{12, 13}}},
		{"cmd.ferry.unload", `{"ferryId":"f2","letterIds":[12],"extra":true}`, sim.Command{Type: sim.CmdFerryUnload, FerryID: "f2", LetterIDs: []uint64{12}}},
		{"cmd.ferry.autoload", `{"ferryId":"f1","to":2}`, sim.Command{Type: sim.CmdFerryAutoload, FerryID: "f1", To: 2}},
		{"cmd.ferry.depart", `{"ferryId":"f1","to":99}`, sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 99}},
	}
	for _, tt := range ok {
		var payload json.RawMessage
		if tt.payload != "" {
			payload = json.RawMessage(tt.payload)
		}
		got, err := simconv.DecodeEngineCommand(tt.typ, payload)
		if err != nil {
			t.Errorf("DecodeEngineCommand(%s, %s): %v", tt.typ, tt.payload, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("DecodeEngineCommand(%s, %s) = %+v, want %+v", tt.typ, tt.payload, got, tt.want)
		}
	}

	for _, typ := range []protocol.MessageType{
		"hello", "cmd.game.pause", "cmd.game.speed", "cmd.game.skip", "cmd.sync",
		"cmd.relay.load", "internal.init", "", "cmd.foo",
	} {
		_, err := simconv.DecodeEngineCommand(typ, json.RawMessage(`{"ferryId":"f1","letterIds":[1],"to":2}`))
		if !errors.Is(err, simconv.ErrNotEngineCommand) {
			t.Errorf("DecodeEngineCommand(%q) error = %v, want ErrNotEngineCommand", typ, err)
		}
	}

	bad := []struct {
		typ     protocol.MessageType
		payload string
	}{
		{"cmd.game.start", `[]`},
		{"cmd.game.start", `"x"`},
		{"cmd.game.start", `1`},
		{"cmd.ferry.load", `[]`},
		{"cmd.ferry.load", `"x"`},
		{"cmd.ferry.load", `1`},
		{"cmd.ferry.load", `{"ferryId":1,"letterIds":[1]}`},
		{"cmd.ferry.load", `{"letterIds":[1]}`},
		{"cmd.ferry.load", `{"ferryId":"","letterIds":[1]}`},
		{"cmd.ferry.load", `{"ferryId":"f1"}`},
		{"cmd.ferry.load", `{"ferryId":"f1","letterIds":[]}`},
		{"cmd.ferry.load", `{"ferryId":"f1","letterIds":null}`},
		{"cmd.ferry.unload", `{"ferryId":"f1","letterIds":[-1]}`},
		{"cmd.ferry.unload", `{"ferryId":"f1","letterIds":[1.5]}`},
		{"cmd.ferry.unload", `{"ferryId":"f1","letterIds":[18446744073709551616]}`},
		{"cmd.ferry.unload", `{"ferryId":"f1","letterIds":"1"}`},
		{"cmd.ferry.unload", `{"FerryId":"f1","letterIds":[1]}`},
		{"cmd.ferry.autoload", `[]`},
		{"cmd.ferry.autoload", `{"ferryId":1,"to":2}`},
		{"cmd.ferry.autoload", `{"to":2}`},
		{"cmd.ferry.autoload", `{"ferryId":"","to":2}`},
		{"cmd.ferry.autoload", `{"ferryId":"f1"}`},
		{"cmd.ferry.depart", `{"ferryId":"f1","to":0}`},
		{"cmd.ferry.depart", `{"ferryId":"f1","to":"2"}`},
		{"cmd.ferry.depart", `{"ferryId":"f1","to":-1}`},
		{"cmd.ferry.depart", `{"ferryId":"f1","to":1.5}`},
		{"cmd.ferry.depart", `null`},
		{"cmd.ferry.depart", ``},
	}
	for _, tt := range bad {
		var payload json.RawMessage
		if tt.payload != "" {
			payload = json.RawMessage(tt.payload)
		}
		got, err := simconv.DecodeEngineCommand(tt.typ, payload)
		if !errors.Is(err, simconv.ErrBadPayload) {
			t.Errorf("DecodeEngineCommand(%s, %s) = (%+v, %v), want ErrBadPayload", tt.typ, tt.payload, got, err)
		}
	}

	// 返した LetterIDs は payload と共有しない。
	payload := json.RawMessage(`{"ferryId":"f1","letterIds":[12,13]}`)
	c, err := simconv.DecodeEngineCommand("cmd.ferry.load", payload)
	if err != nil {
		t.Fatal(err)
	}
	c.LetterIDs[0] = 99
	again, err := simconv.DecodeEngineCommand("cmd.ferry.load", payload)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.LetterIDs, []uint64{12, 13}) {
		t.Errorf("LetterIDs after rewrite = %v, want [12 13]", again.LetterIDs)
	}
}

// C-3
func TestEncodeEngineCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cmd     sim.Command
		typ     protocol.MessageType
		payload string
	}{
		{sim.Command{Type: sim.CmdGameStart}, "cmd.game.start", `{}`},
		{sim.Command{Type: sim.CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{12, 13}}, "cmd.ferry.load", `{"ferryId":"f1","letterIds":[12,13]}`},
		{sim.Command{Type: sim.CmdFerryUnload, FerryID: "f1", LetterIDs: []uint64{12}}, "cmd.ferry.unload", `{"ferryId":"f1","letterIds":[12]}`},
		{sim.Command{Type: sim.CmdFerryAutoload, FerryID: "f1", To: 2}, "cmd.ferry.autoload", `{"ferryId":"f1","to":2}`},
		{sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2}, "cmd.ferry.depart", `{"ferryId":"f1","to":2}`},
	}
	for _, tt := range tests {
		got, err := simconv.EncodeEngineCommand(tt.cmd)
		if err != nil {
			t.Errorf("EncodeEngineCommand(%+v): %v", tt.cmd, err)
			continue
		}
		if got.Type != tt.typ || string(got.Payload) != tt.payload {
			t.Errorf("EncodeEngineCommand(%+v) = %s %s, want %s %s", tt.cmd, got.Type, got.Payload, tt.typ, tt.payload)
		}
		back, err := simconv.DecodeEngineCommand(got.Type, got.Payload)
		if err != nil {
			t.Errorf("DecodeEngineCommand(%s, %s): %v", got.Type, got.Payload, err)
			continue
		}
		if !reflect.DeepEqual(back, tt.cmd) {
			t.Errorf("round trip = %+v, want %+v", back, tt.cmd)
		}
	}
	for _, typ := range []sim.CommandType{"", "cmd.game.pause", "cmd.foo"} {
		if _, err := simconv.EncodeEngineCommand(sim.Command{Type: typ}); !errors.Is(err, simconv.ErrNotEngineCommand) {
			t.Errorf("EncodeEngineCommand(%q) error = %v, want ErrNotEngineCommand", typ, err)
		}
	}
}

func inputCmd(t int64, typ protocol.MessageType, payload string) protocol.InputCommand {
	return protocol.InputCommand{T: t, Cmd: protocol.ClientCommand{Type: typ, Payload: json.RawMessage(payload)}}
}

// C-4
func TestInputEntriesFromLog(t *testing.T) {
	t.Parallel()
	cmds := []protocol.InputCommand{
		inputCmd(0, "cmd.game.start", `{}`),
		inputCmd(0, "cmd.game.speed", `{"speed":2}`),
		inputCmd(500, "cmd.ferry.load", `{"ferryId":"f1","letterIds":[1,2]}`),
		inputCmd(600, "cmd.game.pause", `{"paused":true}`),
		inputCmd(600, "cmd.game.skip", `{"until":"next_notable"}`),
		inputCmd(700, "cmd.sync", `{}`),
		inputCmd(1200, "cmd.ferry.depart", `{"ferryId":"f1","to":2}`),
		inputCmd(1300, "cmd.ferry.autoload", `{"ferryId":"f1","to":3}`),
		inputCmd(1400, "cmd.ferry.unload", `{"ferryId":"f1","letterIds":[2]}`),
	}
	got, err := simconv.InputEntriesFromLog(cmds)
	if err != nil {
		t.Fatal(err)
	}
	want := []sim.InputEntry{
		{T: 0, Cmd: sim.Command{Type: sim.CmdGameStart}},
		{T: 500, Cmd: sim.Command{Type: sim.CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{1, 2}}},
		{T: 1200, Cmd: sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2}},
		{T: 1300, Cmd: sim.Command{Type: sim.CmdFerryAutoload, FerryID: "f1", To: 3}},
		{T: 1400, Cmd: sim.Command{Type: sim.CmdFerryUnload, FerryID: "f1", LetterIDs: []uint64{2}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InputEntriesFromLog = %+v, want %+v", got, want)
	}

	for _, tt := range []struct {
		bad  protocol.InputCommand
		want error
	}{
		{inputCmd(800, "hello", `{"client":"web","resume":false}`), simconv.ErrNotEngineCommand},
		{inputCmd(800, "cmd.relay.load", `{"ferryId":"f1","letterIds":[1]}`), simconv.ErrNotEngineCommand},
		{inputCmd(800, "cmd.ferry.load", `{"ferryId":"f1","letterIds":[]}`), simconv.ErrBadPayload},
		{inputCmd(800, "cmd.ferry.depart", `{"ferryId":"f1","to":`), simconv.ErrBadPayload},
	} {
		log := []protocol.InputCommand{cmds[0], cmds[1], tt.bad, cmds[6]}
		got, err := simconv.InputEntriesFromLog(log)
		if !errors.Is(err, tt.want) {
			t.Errorf("InputEntriesFromLog(%s) error = %v, want %v", tt.bad.Cmd.Type, err, tt.want)
			continue
		}
		if got != nil {
			t.Errorf("InputEntriesFromLog(%s) = %v, want nil on error", tt.bad.Cmd.Type, got)
		}
		wantMsg := "simconv: input log command 2 (t=800, " + string(tt.bad.Cmd.Type) + "): "
		if msg := err.Error(); len(msg) < len(wantMsg) || msg[:len(wantMsg)] != wantMsg {
			t.Errorf("error = %q, want prefix %q", msg, wantMsg)
		}
	}

	for _, in := range [][]protocol.InputCommand{nil, {}, {inputCmd(0, "cmd.sync", `{}`)}} {
		got, err := simconv.InputEntriesFromLog(in)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("InputEntriesFromLog(%v) = (%#v, %v), want an empty non-nil slice", in, got, err)
		}
	}
}

// assertSameEventJSON は、sim.DomainEvent の JSON を NewEvent(type) の型で DisallowUnknownFields で読み、
// Marshal し直した結果がバイト単位で一致することを確かめる（C-5・C-6）。
func assertSameEventJSON(t *testing.T, ev sim.DomainEvent) {
	t.Helper()
	simJSON, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal sim event %s: %v", ev.Type, err)
	}
	v, ok := protocol.NewEvent(protocol.EventType(ev.Type))
	if !ok {
		t.Fatalf("NewEvent(%q) = false", ev.Type)
	}
	if err := decodeStrict(simJSON, v); err != nil {
		t.Fatalf("decode %s into %T: %v", simJSON, v, err)
	}
	got, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal %T: %v", v, err)
	}
	if !bytes.Equal(got, simJSON) {
		t.Errorf("JSON differs\nprotocol: %s\n     sim: %s", got, simJSON)
	}
}

func raftLetter() sim.LetterView {
	return sim.LetterView{
		ID: 31, Kind: "raft", From: 1, To: 2, CreatedAt: 57000,
		Loc: sim.LocView{Type: "ferry", FerryID: "f1"}, Label: "append",
		Raft: &sim.RaftView{
			Type: "MsgApp", Term: 2, LogTerm: 2, Index: 13, Commit: 13,
			Entries: []sim.EntryView{
				{Index: 14, Term: 2, Type: "normal", Summary: "stall.reserve stage2-0005"},
				{Index: 15, Term: 2, Type: "conf_change", Summary: ""},
			},
			Reject: true, RejectHint: 7, Stale: true,
		},
	}
}

func requestLetter() sim.LetterView {
	return sim.LetterView{
		ID: 40, Kind: "request", From: 2, To: 1, CreatedAt: 10000,
		Loc: sim.LocView{Type: "port", IslandID: 2}, Label: "request",
		Request: &sim.RequestView{ReqID: "stage2-0005", Hops: 1},
	}
}

// C-5
func TestSimEventJSONMatchesProtocol(t *testing.T) {
	t.Parallel()
	data := []sim.EventData{
		sim.PhaseChanged{Phase: sim.PhaseSailing},
		sim.LetterCreated{Letter: raftLetter()},
		sim.LetterCreated{Letter: requestLetter()},
		sim.LetterMoved{LetterID: 31, Loc: sim.LocView{Type: "port", IslandID: 3}},
		sim.LetterMoved{LetterID: 31, Loc: sim.LocView{Type: "ferry", FerryID: "f1"}},
		sim.LetterDelivered{LetterID: 31, IslandID: 2, Accepted: true},
		sim.LetterDropped{LetterID: 29, Reason: "overflow"},
		sim.LetterStale{LetterID: 30},
		sim.FerryDeparted{FerryID: "f1", From: 1, To: 2, DepartAt: 58000, ArriveAt: 61750},
		sim.FerryArrived{FerryID: "f1", IslandID: 2, ReadyAt: 63250},
		sim.NodeState{ID: 2, Role: "pre_candidate", Term: 3, Lead: 0, Commit: 14, Applied: 13, LastIndex: 15},
		sim.LeaderElected{ID: 1, Term: 2},
		sim.SteppedDown{ID: 1, Term: 2, Reason: "check_quorum"},
		sim.LedgerApplied{IslandID: 2, Index: 14, ReqID: "stage2-0005", Result: "ok"},
		sim.LedgerApplied{IslandID: 2, Index: 15, ReqID: "stage2-0006", Result: "rejected", Reason: "full"},
	}
	for i, d := range data {
		assertSameEventJSON(t, sim.DomainEvent{Seq: uint64(i + 1), T: sim.SimTime(1000 * i), Type: d.EventType(), Data: d})
	}
}

// C-8
func TestConversionDoesNotShare(t *testing.T) {
	t.Parallel()
	until := sim.SimTime(90000)
	mk := func() sim.Snapshot {
		u := until
		return sim.Snapshot{
			Phase: sim.PhaseSailing, T: 1000, EndsAt: 120000,
			Ferries: []sim.FerryView{{ID: "f1", Name: "ごとう丸", Capacity: 4, State: "docked", IslandID: 1, ReadyAt: 1500, Cargo: []uint64{1, 2}}},
			Routes:  []sim.RouteView{{ID: "1-2", BlockedUntil: &u}, {ID: "2-3"}},
			Letters: []sim.LetterView{raftLetter(), requestLetter()},
			Nodes:   []sim.NodeState{{ID: 1, Role: "leader", Term: 2, Lead: 1}},
			LastSeq: 9,
		}
	}
	src := mk()
	p := simconv.SnapshotFromSim(src)
	p.Ferries[0].Cargo[0] = 99
	*p.RoutesState[0].BlockedUntil = 1
	p.Letters[0].Raft.Entries[0].Index = 99
	p.Letters[0].Raft.Term = 99
	p.Letters[1].Request.Hops = 99
	if !reflect.DeepEqual(src, mk()) {
		t.Errorf("SnapshotFromSim shares memory with its input: %+v", src)
	}
	if p.RoutesState[1].BlockedUntil != nil {
		t.Errorf("RoutesState[1].BlockedUntil = %v, want nil", *p.RoutesState[1].BlockedUntil)
	}

	v := raftLetter()
	l := simconv.LetterFromSim(v)
	l.Raft.Entries[0].Summary = "x"
	l.Raft.Stale = false
	if !reflect.DeepEqual(v, raftLetter()) {
		t.Errorf("LetterFromSim shares memory with its input: %+v", v)
	}
	if l := simconv.LetterFromSim(requestLetter()); l.Raft != nil || l.Request == nil {
		t.Errorf("LetterFromSim(request) = %+v", l)
	}
	noEntries := raftLetter()
	noEntries.Raft.Entries = nil
	if b, _ := json.Marshal(simconv.LetterFromSim(noEntries)); !bytes.Contains(b, []byte(`"entries":[]`)) {
		t.Errorf("LetterFromSim with nil entries = %s, want entries []", b)
	}

	b, err := json.Marshal(simconv.SnapshotFromSim(sim.Snapshot{}))
	if err != nil {
		t.Fatal(err)
	}
	assertNoNullArrays(t, b)
	want := `{"protocol":1,"session":{"id":"","stageId":"","seed":0,"demo":false,"restored":false},"phase":"",` +
		`"clock":{"t":0,"speed":0,"paused":false,"endsAt":0,"allowedSpeeds":[]},` +
		`"stage":{"title":"","islands":[],"routes":[],"resources":[]},` +
		`"ferries":[],"routesState":[],"letters":[],"requests":[],"nodes":[],"islandsState":[],` +
		`"score":{"total":0,"successes":0,"target":0,"criticalSecured":[]},"lastSeq":0}`
	if string(b) != want {
		t.Errorf("SnapshotFromSim(sim.Snapshot{}) = %s, want %s", b, want)
	}
}

// AckFromSim
func TestAckFromSim(t *testing.T) {
	t.Parallel()
	got := simconv.AckFromSim("c-12", sim.Ack{OK: false, T: 61200, Code: sim.CodeRouteBlocked, Message: "blocked"})
	want := protocol.Ack{CmdID: "c-12", OK: false, T: 61200, Code: protocol.AckCodeRouteBlocked, Message: "blocked"}
	if got != want {
		t.Errorf("AckFromSim = %+v, want %+v", got, want)
	}
	b, _ := json.Marshal(simconv.AckFromSim("c-1", sim.Ack{OK: true, T: 5}))
	if string(b) != `{"cmdId":"c-1","ok":true,"t":5}` {
		t.Errorf("AckFromSim(ok) JSON = %s", b)
	}
}

// assertNoNullArrays は、JSON に null の値がない（blockedUntil を除く）ことを確かめる。
func assertNoNullArrays(t *testing.T, b []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case nil:
			t.Errorf("%s is null in %s", path, b)
		case map[string]any:
			for k, c := range x {
				if c == nil && k == "blockedUntil" {
					continue
				}
				walk(path+"."+k, c)
			}
		case []any:
			for _, c := range x {
				walk(path+"[]", c)
			}
		}
	}
	walk("$", v)
}
