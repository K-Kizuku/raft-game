package sim

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// at は evs のうち T == t のものを返す。
func at(evs []DomainEvent, t SimTime) []DomainEvent {
	var out []DomainEvent
	for _, ev := range evs {
		if ev.T == t {
			out = append(out, ev)
		}
	}
	return out
}

// indexOf は evs で pred を満たす最初のイベントの添字を返す（なければ -1）。
func indexOf(evs []DomainEvent, pred func(EventData) bool) int {
	return slices.IndexFunc(evs, func(ev DomainEvent) bool { return pred(ev.Data) })
}

// committedEngine は D-1 の 1. を行う（開始の後、t = 0 で s-0001 を提案する）。
func committedEngine(t *testing.T) (*Engine, []LetterCreated) {
	t.Helper()
	e := startedEngine(t)
	mustPropose(t, e, 1, reserve("s-0001"))
	return e, ofType[LetterCreated](e.DrainEvents())
}

func TestRoundTripCommit(t *testing.T) { // D-1
	t.Parallel()
	e, created := committedEngine(t)

	// 1. MsgApp の letter.created が 2 件
	if len(created) != 2 {
		t.Fatalf("letter.created = %+v, want 2", created)
	}
	l1, l2 := created[0].Letter, created[1].Letter
	if l1.ID != 1 || l1.To != 2 || l1.Label != "append" || l2.ID != 2 || l2.To != 3 {
		t.Errorf("letters = %+v, %+v", l1, l2)
	}
	wantEntries := []EntryView{{Index: 5, Term: 2, Type: "normal", Summary: "stall.reserve s-0001"}}
	if !reflect.DeepEqual(l1.Raft.Entries, wantEntries) {
		t.Errorf("entries = %+v, want %+v", l1.Raft.Entries, wantEntries)
	}

	// 2. 島 2 へ運ぶ
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{1}})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	e.RunUntil(3750)
	evs := at(e.DrainEvents(), 3750)
	iArr := indexOf(evs, func(d EventData) bool { return d == FerryArrived{FerryID: "f1", IslandID: 2, ReadyAt: 5250} })
	iDel := indexOf(evs, func(d EventData) bool { return d == LetterDelivered{LetterID: 1, IslandID: 2, Accepted: true} })
	var resp LetterView
	iResp := indexOf(evs, func(d EventData) bool {
		c, ok := d.(LetterCreated)
		if ok && c.Letter.From == 2 && c.Letter.Label == "append_resp" {
			resp = c.Letter
			return true
		}
		return false
	})
	if iArr < 0 || iDel <= iArr || iResp <= iDel {
		t.Fatalf("order of arrived %d, delivered %d, append_resp %d in %+v", iArr, iDel, iResp, datas(evs))
	}
	// 3. 島 2 の lastIndex が 5
	states := ofType[NodeState](evs)
	if i := slices.IndexFunc(states, func(s NodeState) bool { return s.ID == 2 && s.LastIndex == 5 }); i < 0 {
		t.Errorf("node.state of 2 with lastIndex 5 missing: %+v", states)
	}

	// 4. 返事を積んで島 1 へ
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{resp.ID}})
	if ack := e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: 1}); ack.Code != CodeFerryNotReady {
		t.Errorf("depart at 3750 = %+v, want ferry_not_ready", ack)
	}
	e.RunUntil(5250)
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 1})
	deps := ofType[FerryDeparted](e.DrainEvents())
	if len(deps) != 1 || deps[0].ArriveAt != 9000 {
		t.Fatalf("ferry.departed = %+v, want arriveAt 9000", deps)
	}

	// 5. 島 1 で Commit
	e.RunUntil(9000)
	evs = at(e.DrainEvents(), 9000)
	applied := ofType[LedgerApplied](evs)
	if want := []LedgerApplied{{IslandID: 1, Index: 5, ReqID: "s-0001", Result: "ok"}}; !reflect.DeepEqual(applied, want) {
		t.Errorf("ledger.applied = %+v, want %+v", applied, want)
	}
	states = ofType[NodeState](evs)
	if i := slices.IndexFunc(states, func(s NodeState) bool { return s.ID == 1 && s.Commit == 5 && s.Applied == 5 }); i < 0 {
		t.Errorf("node.state of 1 with commit 5 missing: %+v", states)
	}

	// 6. 島 1 の台帳
	rec, ok := e.ledgers[0].Lookup("s-0001")
	if !ok || rec.Result.Status != ledger.StatusOK {
		t.Errorf("Lookup(s-0001) = %+v, %v", rec, ok)
	}
}

func TestRelayOnFerry(t *testing.T) { // D-2
	t.Parallel()
	e, _ := committedEngine(t)
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{2}}) // 1→3
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	e.RunUntil(5250)
	if got := ofType[LetterDelivered](e.DrainEvents()); len(got) != 0 {
		t.Errorf("delivered at 2: %+v", got)
	}
	if f, _ := e.tr.Ferry("f1"); !reflect.DeepEqual(f.Cargo, []ferry.LetterID{2}) {
		t.Errorf("cargo = %v, want [2]", f.Cargo)
	}
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 3})
	e.RunUntil(10500)
	got := ofType[LetterDelivered](e.DrainEvents())
	if want := []LetterDelivered{{LetterID: 2, IslandID: 3, Accepted: true}}; !reflect.DeepEqual(got, want) {
		t.Errorf("delivered = %+v, want %+v", got, want)
	}
}

// transportState は港・船の一覧（D-3 の比較用）。
type transportState struct {
	ports   [][]ferry.LetterID
	ferries []ferry.Ferry
}

func snapTransport(e *Engine) transportState {
	var s transportState
	for id := uint64(1); id <= uint64(e.cfg.Islands); id++ {
		s.ports = append(s.ports, e.tr.Port(id))
	}
	s.ferries = e.tr.Ferries()
	return s
}

func TestReclaim(t *testing.T) { // D-3
	t.Parallel()
	e, _ := committedEngine(t)
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{1}})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	e.RunUntil(3750)

	before := snapTransport(e)
	evs := e.DrainEvents()
	if after := snapTransport(e); !reflect.DeepEqual(before, after) {
		t.Errorf("DrainEvents changed the transport: %+v → %+v", before, after)
	}
	if _, ok := e.tr.Letter(1); ok {
		t.Error("delivered letter 1 is still held")
	}
	drops := ofType[LetterDropped](evs)
	if len(drops) == 0 {
		t.Fatal("no dropped heartbeat")
	}
	for _, d := range drops {
		if _, ok := e.tr.Letter(ferry.LetterID(d.LetterID)); ok {
			t.Errorf("dropped letter %d is still held", d.LetterID)
		}
	}
	for _, port := range before.ports {
		for _, id := range port {
			if _, ok := e.tr.Letter(id); !ok {
				t.Errorf("letter %d in port is not found", id)
			}
		}
	}
	for _, f := range before.ferries {
		for _, id := range f.Cargo {
			if _, ok := e.tr.Letter(id); !ok {
				t.Errorf("letter %d on ferry is not found", id)
			}
		}
	}
}

func TestDeliveryDoesNotModifyMessages(t *testing.T) { // D-4
	t.Parallel()
	e, created := committedEngine(t)
	l, ok := e.tr.Letter(1)
	if !ok {
		t.Fatal("letter 1 not found")
	}
	msg := l.Raft
	orig := proto.Clone(msg).(*pb.Message)
	origStr := msg.String()
	if want := newRaftView(orig, 0); !reflect.DeepEqual(created[0].Letter.Raft, want) {
		t.Errorf("RaftView = %+v, want %+v", created[0].Letter.Raft, want)
	}

	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{1}})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	e.RunUntil(3750)
	if got := ofType[LetterDelivered](e.DrainEvents()); len(got) != 1 {
		t.Fatalf("delivered = %+v", got)
	}
	if !proto.Equal(msg, orig) || msg.String() != origStr {
		t.Errorf("message changed by delivery: %v → %v", orig, msg)
	}
}

func TestRequestDelivery(t *testing.T) { // D-5
	t.Parallel()
	e := startedEngine(t)
	id, _, err := e.tr.PostRequest(int64(e.Now()), 1, 2, ferry.RequestNote{ReqID: "r-0001"})
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{uint64(id)}})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	before := e.nodes[1].Status()
	e.RunUntil(3750)
	got := ofType[LetterDelivered](e.DrainEvents())
	if want := []LetterDelivered{{LetterID: uint64(id), IslandID: 2, Accepted: true}}; !reflect.DeepEqual(got, want) {
		t.Errorf("delivered = %+v, want %+v", got, want)
	}
	if after := e.nodes[1].Status(); after != before {
		t.Errorf("node 2 status changed: %+v → %+v", before, after)
	}
}

// randomRun は D-6 のランダムな操作をラウンドの終わりまで行い、全イベントと TraceHash を返す。
func randomRun(t *testing.T, seed uint64) ([]DomainEvent, [32]byte) {
	t.Helper()
	e, err := New(testConfig(), seed, Options{CheckInvariants: true})
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(seed, 1))
	var all []DomainEvent
	var created []uint64
	collect := func() {
		evs := e.DrainEvents()
		all = append(all, evs...)
		for _, c := range ofType[LetterCreated](evs) {
			created = append(created, c.Letter.ID)
		}
		for _, id := range created {
			l, ok := e.tr.Letter(ferry.LetterID(id))
			if ok && l.Location.Kind != ferry.LocPort && l.Location.Kind != ferry.LocFerry {
				t.Fatalf("t=%d: letter %d is held at %+v", e.Now(), id, l.Location)
			}
		}
	}
	mustApply(t, e, Command{Type: CmdGameStart})
	collect()
	reqIDs := []string{"s-1", "s-2", "s-3", "f-1"}
	for e.Phase() != PhaseEnded {
		f, _ := e.tr.Ferry("f1")
		switch r.IntN(5) {
		case 0: // load
			if f.State == ferry.Docked {
				port := e.tr.Port(f.At)
				if len(port) > 0 {
					n := 1 + r.IntN(min(len(port), 5))
					var ids []uint64
					for _, i := range r.Perm(len(port))[:n] {
						ids = append(ids, uint64(port[i]))
					}
					e.Apply(Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: ids})
				}
			}
		case 1: // unload
			if len(f.Cargo) > 0 {
				e.Apply(Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: []uint64{uint64(f.Cargo[r.IntN(len(f.Cargo))])}})
			}
		case 2: // depart
			if f.State == ferry.Docked {
				var to uint64
				switch f.At {
				case 1:
					to = 2
				case 2:
					to = 1 + 2*uint64(r.IntN(2))
				case 3:
					to = 2
				}
				e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: to})
			}
		case 3: // propose
			island := 1 + uint64(r.IntN(3))
			kind, res := ledger.KindStallReserve, "stall:main"
			reqID := reqIDs[r.IntN(len(reqIDs))]
			if reqID[0] == 'f' {
				kind, res = ledger.KindFireworkReserve, "firework:a"
			}
			data, err := ledger.Encode(ledger.Command{V: ledger.Version, ReqID: reqID, Origin: island, Kind: kind, Resource: res})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.propose(island, data); err != nil && !errors.Is(err, raft.ErrProposalDropped) {
				t.Fatalf("propose: %v", err)
			}
			e.afterStep()
		case 4:
			e.RunUntil(e.Now() + SimTime(50+r.IntN(1951)))
		}
		collect()
	}
	return all, e.TraceHash()
}

func TestRandomOperations(t *testing.T) { // D-6
	t.Parallel()
	a, ha := randomRun(t, 1)
	b, hb := randomRun(t, 1)
	if !reflect.DeepEqual(a, b) || ha != hb {
		t.Errorf("same seed gave different runs: %d vs %d events", len(a), len(b))
	}
	if sha256OfTrace(t, a) != ha {
		t.Error("TraceHash does not match the definition")
	}
	_, hc := randomRun(t, 2)
	if ha == hc {
		t.Error("seeds 1 and 2 gave the same TraceHash")
	}
	if n := len(ofType[LetterDelivered](a)); n == 0 {
		t.Error("no letter was delivered in the random run")
	}
}
