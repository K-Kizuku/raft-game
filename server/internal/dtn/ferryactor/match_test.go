package ferryactor_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/bot"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/bpv7"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/ferryactor"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// TestFerryMatchesTransport は、参照モデル（sim.Engine。船と Cargo は ferry.Transport）を greedy のボットで
// 動かし、ドメインイベント（船への letter.moved・ferry.departed・ferry.arrived・配達の letter.delivered）を
// 船ごとの要求の列に直して Ferry Actor に与える。各段の ferry.state が、参照モデルのイベントから組み立てた
// 船の状態（ferry.arrived の readyAt・ferry.departed の arriveAt・letter.moved と配達から組み立てた Cargo の順）と
// 一致し、最後の状態が参照モデルのスナップショットの船と一致することを確かめる（AC-2）。
func TestFerryMatchesTransport(t *testing.T) {
	seeds := uint64(50)
	if testing.Short() {
		seeds = 3
	}
	for _, id := range []string{"stage1", "stage2", "demo-goto", "demo-izu"} {
		st := repoStage(t, id)
		stages := repoStages(t)
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			var total matchStats
			for seed := uint64(1); seed <= seeds; seed++ {
				s := matchSeed(t, stages, st, seed)
				total.ops += s.ops
				total.kept += s.kept
				total.adds += s.adds
			}
			// 比べる中身があること: 積み込みと、Cargo を積んだままの到着
			if total.adds == 0 || total.kept == 0 {
				t.Fatalf("too few cases: %+v", total)
			}
			t.Logf("%d seeds: %+v", seeds, total)
		})
	}
}

// ferryOp は船 1 隻への要求 1 つと、参照モデルから組み立てたその後の状態。
type ferryOp struct {
	kind    dtnproto.Kind
	t       int64
	class   uint8
	arrive  dtnproto.ArrivePayload
	depart  dtnproto.DepartPayload
	letters []dtnproto.Letter
	ids     []ferry.LetterID
	want    ferryactor.StateData
}

func (op *ferryOp) payload() any {
	switch op.kind {
	case dtnproto.KindArrive:
		return op.arrive
	case dtnproto.KindDepart:
		return op.depart
	case dtnproto.KindCargoAdd:
		return dtnproto.LettersPayload{T: op.t, Letters: op.letters}
	default:
		return dtnproto.LetterIDsPayload{LetterIDs: op.ids}
	}
}

// refFerry は参照モデルのイベントから組み立てる船 1 隻。
type refFerry struct {
	cur ferryactor.StateData
	ops []*ferryOp
}

func (f *refFerry) snapshot() ferryactor.StateData {
	s := f.cur
	s.Cargo = slices.Clone(s.Cargo)
	return s
}

// add は op を足す。直前の op が同じ種類・同じ t の積み下ろしなら 1 つにまとめる（Coordinator は 1 回の操作を
// 1 つの Handoff にする）。
func (f *refFerry) add(op *ferryOp) {
	if n := len(f.ops); n > 0 {
		last := f.ops[n-1]
		if last.kind == op.kind && last.t == op.t && len(last.letters)+len(last.ids) < dtnproto.DefaultLimits.MaxLetters {
			switch op.kind {
			case dtnproto.KindCargoAdd:
				last.letters = append(last.letters, op.letters...)
				last.want = op.want
				return
			case dtnproto.KindCargoRemove:
				last.ids = append(last.ids, op.ids...)
				last.want = op.want
				return
			}
		}
	}
	f.ops = append(f.ops, op)
}

// matchStats は比べた要求の数。
type matchStats struct {
	ops  int // 要求の数
	adds int // cargo.add の数
	kept int // Cargo を積んで着いた arrive の数（宛先でない手紙は残り、宛先の手紙は後の cargo.remove で除く）
}

func matchSeed(t *testing.T, stages []*stage.Stage, st *stage.Stage, seed uint64) matchStats {
	t.Helper()
	cfg, err := stage.SimConfig(st)
	if err != nil {
		t.Fatal(err)
	}
	e, err := sim.New(cfg, seed, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := bot.New("greedy", seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := bot.Play(e, b); err != nil {
		t.Fatal(err)
	}
	events := e.DrainEvents()
	snap := e.Snapshot()
	codec := codecFor(st)

	ferries := make(map[string]*refFerry, len(st.Ferries))
	for _, sf := range st.Ferries {
		ferries[sf.ID] = &refFerry{cur: ferryactor.StateData{State: "docked", At: sf.Home, Cargo: []ferry.LetterID{}}}
	}
	letters := map[uint64]sim.LetterView{} // 参照モデルの ID → letter.created の手紙
	onFerry := map[uint64]string{}         // 参照モデルの ID → 積んでいる船
	// dtnID は参照モデルの手紙の ID を、BPv7 の letterId（島 × 10^9 + n。simulation-time.md §5.1）にする。
	// 参照モデルがその規則で振っていれば（DTN-00 の後）そのまま、全体の連番なら送り元の島 × 10^9 を足す。
	dtnID := func(id uint64) ferry.LetterID {
		from := letters[id].From
		if node, _, err := bpv7.SplitLetterID(id); err == nil && node == from {
			return ferry.LetterID(id)
		}
		return ferry.LetterID(from*1_000_000_000 + id)
	}
	fail := func(ev sim.DomainEvent, format string, args ...any) {
		t.Helper()
		raw, _ := json.Marshal(ev)
		t.Fatalf("%s seed=%d at event %s: "+format, append([]any{st.ID, seed, raw}, args...)...)
	}
	removeFrom := func(ev sim.DomainEvent, lid uint64) {
		fid := onFerry[lid]
		f := ferries[fid]
		id := dtnID(lid)
		i := slices.Index(f.cur.Cargo, id)
		if i < 0 {
			fail(ev, "letter %d is not in the cargo of %q", lid, fid)
		}
		f.cur.Cargo = slices.Delete(f.cur.Cargo, i, i+1)
		delete(onFerry, lid)
		f.add(&ferryOp{kind: dtnproto.KindCargoRemove, t: int64(ev.T), class: 8, ids: []ferry.LetterID{id}, want: f.snapshot()})
	}

	for _, ev := range events {
		switch d := ev.Data.(type) {
		case sim.LetterCreated:
			letters[d.Letter.ID] = d.Letter
		case sim.LetterMoved:
			if d.Loc.Type == "ferry" {
				f := ferries[d.Loc.FerryID]
				l, ok := letters[d.LetterID]
				if f == nil || !ok || onFerry[d.LetterID] != "" {
					fail(ev, "unexpected load")
				}
				onFerry[d.LetterID] = d.Loc.FerryID
				f.cur.Cargo = append(f.cur.Cargo, dtnID(d.LetterID))
				f.add(&ferryOp{kind: dtnproto.KindCargoAdd, t: int64(ev.T), class: 8,
					letters: []dtnproto.Letter{refLetter(t, codec, l, dtnID(d.LetterID))}, want: f.snapshot()})
				continue
			}
			if onFerry[d.LetterID] == "" {
				fail(ev, "unload of a letter that is not on a ferry")
			}
			removeFrom(ev, d.LetterID)
		case sim.LetterDelivered:
			if onFerry[d.LetterID] != "" { // 到着の配達（港を通らない即時配達は船と関係がない）
				removeFrom(ev, d.LetterID)
			}
		case sim.LetterDropped:
			if onFerry[d.LetterID] != "" {
				fail(ev, "a letter on a ferry was dropped")
			}
		case sim.FerryDeparted:
			f := ferries[d.FerryID]
			if f.cur.State != "docked" || f.cur.At != d.From {
				fail(ev, "departure from %+v", f.cur)
			}
			f.cur.State, f.cur.At, f.cur.To, f.cur.ArriveAt = "sailing", 0, d.To, int64(d.ArriveAt)
			f.add(&ferryOp{kind: dtnproto.KindDepart, t: int64(ev.T), class: 8,
				depart: dtnproto.DepartPayload{T: int64(d.DepartAt), To: d.To, ArriveAt: int64(d.ArriveAt)}, want: f.snapshot()})
		case sim.FerryArrived:
			f := ferries[d.FerryID]
			if f.cur.State != "sailing" || f.cur.To != d.IslandID || f.cur.ArriveAt != int64(ev.T) {
				fail(ev, "arrival of %+v", f.cur)
			}
			f.cur.State, f.cur.At, f.cur.To, f.cur.ArriveAt, f.cur.ReadyAt = "docked", d.IslandID, 0, 0, int64(d.ReadyAt)
			f.add(&ferryOp{kind: dtnproto.KindArrive, t: int64(ev.T), class: 1,
				arrive: dtnproto.ArrivePayload{T: int64(ev.T), Island: d.IslandID}, want: f.snapshot()})
		}
	}

	var stats matchStats
	for _, fv := range snap.Ferries {
		rf := ferries[fv.ID]
		a := ferryactor.New(stages)
		outs, err := a.Init(initPayload(st, fv.ID))
		if err != nil {
			t.Fatal(err)
		}
		_, got := stateOf(t, outs)
		home := slices.IndexFunc(st.Ferries, func(f stage.Ferry) bool { return f.ID == fv.ID })
		if want := (ferryactor.StateData{State: "docked", At: st.Ferries[home].Home, Cargo: []ferry.LetterID{}}); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s seed=%d %s: init state %+v, want %+v", st.ID, seed, fv.ID, got, want)
		}
		lastT := int64(0)
		for i, op := range rf.ops {
			outs, err := a.Handle(op.kind, mustJSON(t, op.payload()))
			if err != nil {
				t.Fatalf("%s seed=%d %s: op %d %s %s: %v", st.ID, seed, fv.ID, i, op.kind, mustJSON(t, op.payload()), err)
			}
			out, got := stateOf(t, outs)
			if !reflect.DeepEqual(got, op.want) {
				t.Fatalf("%s seed=%d %s: op %d %s: ferry.state %+v, want %+v", st.ID, seed, fv.ID, i, op.kind, got, op.want)
			}
			wantT := op.t
			if op.kind == dtnproto.KindCargoRemove {
				wantT = lastT // cargo.remove は直前の要求の t
			}
			if out.T != wantT || out.Class != op.class || out.Seq != uint64(i+2) {
				t.Fatalf("%s seed=%d %s: op %d %s: (t, class, seq) = (%d, %d, %d), want (%d, %d, %d)",
					st.ID, seed, fv.ID, i, op.kind, out.T, out.Class, out.Seq, wantT, op.class, i+2)
			}
			lastT = out.T
			stats.ops++
			switch {
			case op.kind == dtnproto.KindCargoAdd:
				stats.adds++
			case op.kind == dtnproto.KindArrive && len(got.Cargo) > 0:
				stats.kept++
			}
		}

		// 最後の状態は、参照モデルのスナップショットの船（ferry.Transport）と一致する
		raw, err := a.Inspect()
		if err != nil {
			t.Fatal(err)
		}
		var iv struct {
			State    string `json:"state"`
			At       uint64 `json:"at"`
			From     uint64 `json:"from"`
			To       uint64 `json:"to"`
			DepartAt int64  `json:"departAt"`
			ArriveAt int64  `json:"arriveAt"`
			ReadyAt  int64  `json:"readyAt"`
			Cargo    []struct {
				LetterID ferry.LetterID `json:"letterId"`
			} `json:"cargo"`
		}
		if err := json.Unmarshal(raw, &iv); err != nil {
			t.Fatal(err)
		}
		cargo := make([]ferry.LetterID, len(iv.Cargo))
		for i, c := range iv.Cargo {
			cargo[i] = c.LetterID
		}
		wantCargo := make([]ferry.LetterID, len(fv.Cargo))
		for i, id := range fv.Cargo {
			wantCargo[i] = dtnID(id)
		}
		ok := iv.State == fv.State && iv.At == fv.IslandID && iv.From == fv.From && iv.To == fv.To &&
			iv.DepartAt == int64(fv.DepartAt) && iv.ArriveAt == int64(fv.ArriveAt) && slices.Equal(cargo, wantCargo)
		if fv.State == "docked" {
			ok = ok && iv.ReadyAt == int64(fv.ReadyAt) // 航行中のスナップショットの readyAt は ArriveAt + DockMs（船は前の値のまま）
		}
		if !ok {
			t.Fatalf("%s seed=%d %s: final state %s, snapshot %+v", st.ID, seed, fv.ID, raw, fv)
		}
		if len(rf.ops) == 0 {
			t.Fatalf("%s seed=%d %s: the ferry never moved (the scenario checks nothing)", st.ID, seed, fv.ID)
		}
	}
	return stats
}

// refLetter は参照モデルの手紙 l を、letterId id の Bundle にする（Raft のメッセージの中身は表示用の値から
// 作る。船は中身を解釈しない）。
func refLetter(t *testing.T, codec dtnproto.LetterCodec, l sim.LetterView, id ferry.LetterID) dtnproto.Letter {
	t.Helper()
	fl := ferry.Letter{ID: id, From: l.From, To: l.To, CreatedAt: int64(l.CreatedAt)}
	switch l.Kind {
	case "request":
		fl.Kind = ferry.LetterRequest
		fl.Req = &ferry.RequestNote{ReqID: l.Request.ReqID, Hops: l.Request.Hops}
	default:
		fl.Kind = ferry.LetterRaft
		typ := pb.MessageType(pb.MessageType_value[l.Raft.Type])
		term := l.Raft.Term
		fl.Raft = &pb.Message{Type: &typ, From: &fl.From, To: &fl.To, Term: &term}
	}
	return encodeLetter(t, codec, fl)
}
