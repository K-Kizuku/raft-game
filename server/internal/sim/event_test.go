package sim

import (
	"encoding/json"
	"strings"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

func TestDomainEventJSON(t *testing.T) { // V-1
	t.Parallel()
	tests := []struct {
		name string
		ev   DomainEvent
		want string
	}{
		{
			"phase.changed",
			DomainEvent{Seq: 1, T: 0, Type: EvPhaseChanged, Data: PhaseChanged{Phase: PhaseOpening}},
			`{"seq":1,"t":0,"type":"phase.changed","phase":"opening"}`,
		},
		{
			"letter.dropped",
			DomainEvent{Seq: 7, T: 2000, Type: EvLetterDropped, Data: LetterDropped{LetterID: 1, Reason: "superseded"}},
			`{"seq":7,"t":2000,"type":"letter.dropped","letterId":1,"reason":"superseded"}`,
		},
		{
			"ledger.applied ok",
			DomainEvent{Seq: 3, T: 9000, Type: EvLedgerApplied, Data: LedgerApplied{IslandID: 1, Index: 5, ReqID: "s-0001", Result: "ok"}},
			`{"seq":3,"t":9000,"type":"ledger.applied","islandId":1,"index":5,"reqId":"s-0001","result":"ok"}`,
		},
		{
			"ledger.applied rejected",
			DomainEvent{Seq: 4, T: 9000, Type: EvLedgerApplied, Data: LedgerApplied{IslandID: 2, Index: 6, ReqID: "s-0002", Result: "rejected", Reason: "full"}},
			`{"seq":4,"t":9000,"type":"ledger.applied","islandId":2,"index":6,"reqId":"s-0002","result":"rejected","reason":"full"}`,
		},
		{
			"letter.created raft without entries",
			DomainEvent{Seq: 5, T: 1000, Type: EvLetterCreated, Data: LetterCreated{Letter: newLetterView(ferry.Letter{
				ID: 1, Kind: ferry.LetterRaft, From: 1, To: 2, CreatedAt: 1000,
				Raft: &pb.Message{Type: pb.MsgHeartbeat.Enum(), From: ptr[uint64](1), To: ptr[uint64](2), Term: ptr[uint64](2)},
			}, 2)}},
			`{"seq":5,"t":1000,"type":"letter.created","letter":{"id":1,"kind":"raft","from":1,"to":2,"createdAt":1000,` +
				`"loc":{"type":"port","islandId":1},"label":"heartbeat","raft":{"type":"MsgHeartbeat","term":2,"logTerm":0,` +
				`"index":0,"commit":0,"entries":[],"reject":false,"rejectHint":0,"stale":false}}}`,
		},
		{
			"letter.created request",
			DomainEvent{Seq: 6, T: 0, Type: EvLetterCreated, Data: LetterCreated{Letter: newLetterView(ferry.Letter{
				ID: 2, Kind: ferry.LetterRequest, From: 2, To: 1, Req: &ferry.RequestNote{ReqID: "r-1", Hops: 1},
			}, 0)}},
			`{"seq":6,"t":0,"type":"letter.created","letter":{"id":2,"kind":"request","from":2,"to":1,"createdAt":0,` +
				`"loc":{"type":"port","islandId":2},"label":"request","request":{"reqId":"r-1","hops":1}}}`,
		},
		{
			"letter.moved to ferry",
			DomainEvent{Seq: 8, T: 0, Type: EvLetterMoved, Data: LetterMoved{LetterID: 3, Loc: ferryLoc("f1")}},
			`{"seq":8,"t":0,"type":"letter.moved","letterId":3,"loc":{"type":"ferry","ferryId":"f1"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(tt.ev)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tt.want {
				t.Errorf("json:\n got  %s\n want %s", b, tt.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

// emptyData は中身のない EventData（MarshalJSON の {} の扱いを確かめる）。
type emptyData struct{}

func (emptyData) EventType() EventType { return "test.empty" }

func TestDomainEventJSONEmptyData(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(DomainEvent{Seq: 2, T: 5, Type: "test.empty", Data: emptyData{}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"seq":2,"t":5,"type":"test.empty"}`; string(b) != want {
		t.Errorf("json = %s, want %s", b, want)
	}
	if strings.HasSuffix(string(b), "\n") {
		t.Error("trailing newline")
	}
}

func TestEventDataReservedKeys(t *testing.T) { // V-2
	t.Parallel()
	all := []EventData{
		PhaseChanged{}, LetterCreated{}, LetterMoved{}, LetterDelivered{}, LetterDropped{},
		FerryDeparted{}, FerryArrived{}, NodeState{}, LeaderElected{}, SteppedDown{}, LedgerApplied{},
	}
	types := make(map[EventType]bool)
	for _, d := range all {
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(b, &keys); err != nil {
			t.Fatalf("%T: %v", d, err)
		}
		for _, k := range []string{"seq", "t", "type"} {
			if _, ok := keys[k]; ok {
				t.Errorf("%T has top-level key %q", d, k)
			}
		}
		if types[d.EventType()] {
			t.Errorf("duplicate EventType %s", d.EventType())
		}
		types[d.EventType()] = true
	}
}

func TestDrainEvents(t *testing.T) { // V-4
	t.Parallel()
	e := newEngine(t, testConfig(), Options{})
	mustApply(t, e, Command{Type: CmdGameStart})
	first := e.DrainEvents()
	if again := e.DrainEvents(); len(again) != 0 {
		t.Errorf("second DrainEvents = %d events, want 0", len(again))
	}
	e.RunUntil(1000)
	second := e.DrainEvents()
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("events: %d, %d", len(first), len(second))
	}
	all := append(first, second...)
	for i, ev := range all {
		if ev.Seq != uint64(i+1) {
			t.Errorf("event %d: Seq = %d, want %d", i, ev.Seq, i+1)
		}
	}
}

func TestEntryViewSummary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ent  *pb.Entry
		want EntryView
	}{
		{&pb.Entry{Index: ptr[uint64](4), Term: ptr[uint64](2)}, EntryView{Index: 4, Term: 2, Type: "normal"}},
		{&pb.Entry{Index: ptr[uint64](5), Term: ptr[uint64](2), Data: []byte("{")}, EntryView{Index: 5, Term: 2, Type: "normal", Summary: "invalid"}},
		{&pb.Entry{Type: pb.EntryConfChange.Enum(), Index: ptr[uint64](1), Term: ptr[uint64](1), Data: []byte("x")}, EntryView{Index: 1, Term: 1, Type: "conf_change"}},
		{&pb.Entry{Type: pb.EntryConfChangeV2.Enum(), Index: ptr[uint64](1), Term: ptr[uint64](1)}, EntryView{Index: 1, Term: 1, Type: "conf_change_v2"}},
	}
	for _, tt := range tests {
		if got := newEntryView(tt.ent); got != tt.want {
			t.Errorf("newEntryView(%v) = %+v, want %+v", tt.ent, got, tt.want)
		}
	}
}
