package sim

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/cluster"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// relayTestConfig は 1-2-3 の一直線で、f1（容量 4）が島 1、f2（容量 1）が島 2 にいる設定（積み替えあり）。
// ElectionTick を 200（100 秒）にして、テストの間に Leader が変わらないようにする。
func relayTestConfig() Config {
	cfg := testConfig()
	cfg.Raft.ElectionTick = 200
	cfg.Ferries = append(slices.Clone(cfg.Ferries), ferry.FerryConfig{ID: "f2", Home: 2, Capacity: 1, SpeedPermille: 1000})
	cfg.RelayCap = 12
	return cfg
}

// letterIDsTo は island の港にある、to 宛ての Raft の手紙の ID を昇順に返す。
func letterIDsTo(e *Engine, island, to uint64) []uint64 {
	var out []uint64
	for _, id := range e.tr.Port(island) {
		if l, ok := e.tr.Letter(id); ok && l.Kind == ferry.LetterRaft && l.To == to {
			out = append(out, uint64(id))
		}
	}
	return out
}

// relayCarry は SR-1 の前半を行う。島 1 で提案して、t = 1000 に港 1 の 3 宛ての Raft の手紙を n 通 f1 に積み、
// 島 2 へ運んで着かせる（t = 4750）。積んだ手紙の ID を返し、イベントを取り出しておく。
func relayCarry(t *testing.T, cfg Config, n int) (*Engine, []uint64) {
	t.Helper()
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	mustPropose(t, e, 1, reserve("s-0001"))
	mustPropose(t, e, 1, reserve("s-0002"))
	e.RunUntil(1000)
	ids := letterIDsTo(e, 1, 3)
	if len(ids) < n {
		t.Fatalf("port 1 has %d letters to 3, want >= %d", len(ids), n)
	}
	ids = ids[:n]
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: ids})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	e.RunUntil(4750)
	if f, _ := e.tr.Ferry("f1"); f.State != ferry.Docked || f.At != 2 {
		t.Fatalf("f1 = %+v, want docked at 2", f)
	}
	e.DrainEvents()
	return e, ids
}

// nodesOf はノードの Status とログを返す。
func nodesOf(t *testing.T, e *Engine) ([]cluster.Status, [][]cluster.LogEntry) {
	t.Helper()
	var sts []cluster.Status
	var logs [][]cluster.LogEntry
	for _, n := range e.nodes {
		sts = append(sts, n.Status())
		l, err := n.Log()
		if err != nil {
			t.Fatal(err)
		}
		logs = append(logs, l)
	}
	return sts, logs
}

func TestRelayUnloadNotProcessed(t *testing.T) { // SR-1・SR-2
	t.Parallel()
	e, ids := relayCarry(t, relayTestConfig(), 1)
	base, _ := relayCarry(t, relayTestConfig(), 1) // 預けない場合
	l := ids[0]

	if ack := mustApply(t, e, Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: ids}); ack.T != 4750 {
		t.Fatalf("ack = %+v", ack)
	}
	checkEvents(t, e.DrainEvents(), []EventData{LetterMoved{LetterID: l, Loc: LocView{Type: "relay", IslandID: 2}}})
	if got := e.tr.Relay(2); !slices.Equal(got, []ferry.LetterID{ferry.LetterID(l)}) {
		t.Fatalf("relay 2 = %v", got)
	}

	for _, until := range []SimTime{6000, 9000} {
		e.RunUntil(until)
		base.RunUntil(until)
		for _, d := range ofType[LetterDelivered](e.DrainEvents()) {
			if d.LetterID == l {
				t.Fatalf("letter %d delivered while in relay: %+v", l, d)
			}
		}
		base.DrainEvents()
		gs, gl := nodesOf(t, e)
		ws, wl := nodesOf(t, base)
		if !reflect.DeepEqual(gs, ws) || !reflect.DeepEqual(gl, wl) {
			t.Fatalf("t=%d: nodes differ from the run without relay:\n got  %+v\n want %+v", until, gs, ws)
		}
	}

	// SR-2: f2 が積み直し、3 へ運ぶと配達される。
	mustApply(t, e, Command{Type: CmdRelayLoad, FerryID: "f2", LetterIDs: ids})
	checkEvents(t, e.DrainEvents(), []EventData{LetterMoved{LetterID: l, Loc: ferryLoc("f2")}})
	if got := e.tr.Relay(2); len(got) != 0 {
		t.Fatalf("relay 2 = %v, want empty", got)
	}
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f2", To: 3})
	e.RunUntil(9000 + 5250)
	var got []LetterDelivered
	for _, d := range ofType[LetterDelivered](e.DrainEvents()) {
		if d.LetterID == l {
			got = append(got, d)
		}
	}
	if len(got) != 1 || got[0].IslandID != 3 {
		t.Fatalf("letter.delivered for %d = %+v, want one at island 3", l, got)
	}
}

func TestRelayLoadSameFerry(t *testing.T) {
	t.Parallel()
	e, ids := relayCarry(t, relayTestConfig(), 2)
	mustApply(t, e, Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: ids})
	e.DrainEvents()
	rev := []uint64{ids[1], ids[0]}
	mustApply(t, e, Command{Type: CmdRelayLoad, FerryID: "f1", LetterIDs: rev})
	checkEvents(t, e.DrainEvents(), []EventData{
		LetterMoved{LetterID: rev[0], Loc: ferryLoc("f1")},
		LetterMoved{LetterID: rev[1], Loc: ferryLoc("f1")},
	})
	if got := cargoOf(t, e, "f1"); !slices.Equal(got, toLetterIDs(rev)) {
		t.Errorf("cargo = %v, want %v", got, rev)
	}
	if in := e.InputLog(); !reflect.DeepEqual(in[len(in)-1].Cmd, Command{Type: CmdRelayLoad, FerryID: "f1", LetterIDs: rev}) {
		t.Errorf("last input = %+v", in[len(in)-1])
	}
}

func TestRelayAcks(t *testing.T) { // SR-3
	t.Parallel()
	fail := func(t *testing.T, e *Engine, cmd Command, code string) {
		t.Helper()
		e.DrainEvents()
		before := stateOf(e)
		relays := relaysOf(e)
		ack := e.Apply(cmd)
		if ack.OK || ack.Code != code || ack.T != e.Now() {
			t.Errorf("Apply(%+v) = %+v, want %s", cmd, ack, code)
		}
		if evs := e.DrainEvents(); len(evs) != 0 {
			t.Errorf("Apply(%+v): events %+v", cmd, datas(evs))
		}
		if after := stateOf(e); !reflect.DeepEqual(before, after) || !reflect.DeepEqual(relays, relaysOf(e)) {
			t.Errorf("Apply(%+v) changed the state", cmd)
		}
	}

	t.Run("relay_full", func(t *testing.T) {
		t.Parallel()
		cfg := relayTestConfig()
		cfg.RelayCap = 1
		e, ids := relayCarry(t, cfg, 2)
		fail(t, e, Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: ids}, CodeRelayFull)
		mustApply(t, e, Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: ids[:1]})
		fail(t, e, Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: ids[1:]}, CodeRelayFull)

		cfg.RelayCap = 0
		e, ids = relayCarry(t, cfg, 1)
		fail(t, e, Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: ids}, CodeRelayFull)
	})

	t.Run("relay.load", func(t *testing.T) {
		t.Parallel()
		e, ids := relayCarry(t, relayTestConfig(), 2)
		mustApply(t, e, Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: ids})
		port1 := e.tr.Port(1)
		if len(port1) == 0 {
			t.Fatal("no letter in port 1")
		}
		// 港の手紙は倉庫にない。
		fail(t, e, Command{Type: CmdRelayLoad, FerryID: "f2", LetterIDs: []uint64{uint64(port1[0])}}, CodeLetterNotHere)
		fail(t, e, Command{Type: CmdRelayLoad, FerryID: "f2", LetterIDs: []uint64{99999}}, CodeLetterNotHere)
		fail(t, e, Command{Type: CmdRelayLoad, FerryID: "f2", LetterIDs: ids}, CodeCapacityExceeded)
		fail(t, e, Command{Type: CmdRelayLoad, FerryID: "f2", LetterIDs: []uint64{ids[0], ids[0]}}, CodeBadRequest)
		fail(t, e, Command{Type: CmdRelayLoad, FerryID: "f2"}, CodeBadRequest)
		fail(t, e, Command{Type: CmdRelayLoad, FerryID: "zz", LetterIDs: ids[:1]}, CodeBadRequest)
		mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f2", To: 3})
		fail(t, e, Command{Type: CmdRelayLoad, FerryID: "f2", LetterIDs: ids[:1]}, CodeFerryNotDocked)
		e.RunUntil(e.Now() + 5250)
		// 島 3 の船からは島 2 の倉庫の手紙を積めない。
		fail(t, e, Command{Type: CmdRelayLoad, FerryID: "f2", LetterIDs: ids[:1]}, CodeLetterNotHere)
	})

	t.Run("phase_invalid", func(t *testing.T) {
		t.Parallel()
		e := newEngine(t, relayTestConfig(), Options{CheckInvariants: true})
		fail(t, e, Command{Type: CmdRelayLoad, FerryID: "f2", LetterIDs: lids(1, 1)}, CodePhaseInvalid)
		mustApply(t, e, Command{Type: CmdGameStart})
		e.RunUntil(e.cfg.DurationMs)
		if e.Phase() != PhaseEnded {
			t.Fatalf("phase = %s", e.Phase())
		}
		fail(t, e, Command{Type: CmdRelayLoad, FerryID: "f2", LetterIDs: lids(1, 1)}, CodePhaseInvalid)
	})
}

// relaysOf は島ごとの倉庫の手紙を返す（添字 = 島の ID - 1）。
func relaysOf(e *Engine) [][]ferry.LetterID {
	var out [][]ferry.LetterID
	for id := uint64(1); id <= uint64(e.cfg.Islands); id++ {
		out = append(out, e.tr.Relay(id))
	}
	return out
}

func TestRelaySnapshot(t *testing.T) { // SR-4
	t.Parallel()
	e, ids := relayCarry(t, relayTestConfig(), 2)
	mustApply(t, e, Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: ids})
	s := e.Snapshot()
	if !slices.IsSortedFunc(s.Letters, func(a, b LetterView) int { return int(a.ID) - int(b.ID) }) {
		t.Errorf("letters are not sorted: %+v", s.Letters)
	}
	for _, id := range ids {
		if got := snapLetter(t, s, id).Loc; got != (LocView{Type: "relay", IslandID: 2}) {
			t.Errorf("letter %d loc = %+v, want relay 2", id, got)
		}
	}
	// 当てはめた結果との一致は、ランダムな積み替えのラウンド（relayRun）が各操作の後に確かめる。
}

func TestRelayStale(t *testing.T) { // SR-5
	t.Parallel()
	e, ids := relayCarry(t, relayTestConfig(), 1)
	mustApply(t, e, Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: ids})
	e.DrainEvents()
	term := e.nodes[0].Status().Term
	deliverRaw(t, e, heartbeat(2, 1, term+1))
	if got := staleIDs(e.DrainEvents()); !slices.Contains(got, ids[0]) {
		t.Fatalf("letter.stale = %v, want %d", got, ids[0])
	}
	if !snapLetter(t, e.Snapshot(), ids[0]).Raft.Stale {
		t.Error("snapshot: relay letter is not stale")
	}
	deliverRaw(t, e, heartbeat(3, 1, term+2))
	if got := staleIDs(e.DrainEvents()); slices.Contains(got, ids[0]) {
		t.Errorf("letter.stale = %v, want %d only once", got, ids[0])
	}
}

func TestRelayRequestInTransit(t *testing.T) { // SR-6
	t.Parallel()
	cfg := relayTestConfig()
	cfg.Requests = reqConfig(fireworkReq(0, 3))
	cfg.Ferries[1].Capacity = 2
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	e.RunUntil(0)
	reqs := requestLetters(e.DrainEvents())
	if len(reqs) != 1 || reqs[0].From != 3 || reqs[0].To != 1 {
		t.Fatalf("request letters = %+v", reqs)
	}
	id := reqs[0].ID
	// f2 を 3 へ出し、依頼書を積んで 2 へ戻る。
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f2", To: 3})
	e.RunUntil(5250)
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f2", LetterIDs: []uint64{id}})
	if got := ofType[RequestUpdated](e.DrainEvents()); !reflect.DeepEqual(got, []RequestUpdated{updated("t-0001", game.StatusInTransit, nil)}) {
		t.Fatalf("request.updated after load = %+v", got)
	}
	e.RunUntil(5250 + 1500)
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f2", To: 2})
	e.RunUntil(5250 + 1500 + 5250)
	e.DrainEvents()

	// 2 で預けても運搬中のまま（request.updated は出ない）。
	mustApply(t, e, Command{Type: CmdFerryUnload, FerryID: "f2", LetterIDs: []uint64{id}})
	evs := e.DrainEvents()
	checkEvents(t, evs, []EventData{LetterMoved{LetterID: id, Loc: LocView{Type: "relay", IslandID: 2}}})
	if st := e.Snapshot().Requests[0].Status; st != string(game.StatusInTransit) {
		t.Fatalf("status = %s, want in_transit", st)
	}
	// 積み直して 3 へ戻り、港に戻すと今までどおり待ち（waiting）に戻る。
	mustApply(t, e, Command{Type: CmdRelayLoad, FerryID: "f2", LetterIDs: []uint64{id}})
	if got := ofType[RequestUpdated](e.DrainEvents()); len(got) != 0 {
		t.Fatalf("request.updated after relay.load = %+v", got)
	}
	f, _ := e.tr.Ferry("f2")
	e.RunUntil(SimTime(f.ReadyAt))
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f2", To: 3})
	e.RunUntil(e.Now() + 5250)
	e.DrainEvents()
	mustApply(t, e, Command{Type: CmdFerryUnload, FerryID: "f2", LetterIDs: []uint64{id}})
	if got := ofType[RequestUpdated](e.DrainEvents()); !reflect.DeepEqual(got, []RequestUpdated{updated("t-0001", game.StatusWaiting, nil)}) {
		t.Errorf("request.updated after unload at origin = %+v, want waiting", got)
	}
}

// relayRunStats はランダムな積み替えのラウンドで成功した操作の数。
type relayRunStats struct {
	stored, relayLoaded int
}

// relayRun は cfg・seed で、積み替えを含むランダムな操作をラウンドの終わりまで行う。各操作の後に、
// スナップショットの手紙とノードがイベントを当てはめた結果と一致することを確かめる（SR-4）。
// 提案は入力ログに入らないので使わない（Replay で再現できるように）。
func relayRun(t *testing.T, cfg Config, seed uint64) (*Engine, relayRunStats) {
	t.Helper()
	e, err := New(cfg, seed, Options{CheckInvariants: true})
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(seed, 5))
	m := newEventModel(cfg.Islands)
	var st relayRunStats
	check := func() {
		t.Helper()
		evs := e.DrainEvents()
		for _, d := range ofType[LetterMoved](evs) {
			if d.Loc.Type == "relay" {
				st.stored++
			}
		}
		m.apply(t, evs)
		s := e.Snapshot()
		if !reflect.DeepEqual(s.Letters, m.letters) {
			t.Fatalf("t=%d: Letters differ:\n snapshot %+v\n events   %+v", e.Now(), s.Letters, m.letters)
		}
		if !reflect.DeepEqual(s.Nodes, m.nodes) || s.LastSeq != m.lastSeq {
			t.Fatalf("t=%d: Nodes or LastSeq differ", e.Now())
		}
	}
	pick := func(pool []ferry.LetterID) []uint64 {
		var ids []uint64
		for _, id := range pool {
			if r.IntN(2) == 0 {
				ids = append(ids, uint64(id))
			}
		}
		return ids
	}
	mustApply(t, e, Command{Type: CmdGameStart})
	check()
	for e.Phase() != PhaseEnded {
		for _, f := range e.tr.Ferries() {
			if f.State != ferry.Docked {
				continue
			}
			id := string(f.ID)
			switch r.IntN(5) {
			case 0:
				e.Apply(Command{Type: CmdFerryLoad, FerryID: id, LetterIDs: pick(e.tr.Port(f.At))})
			case 1:
				e.Apply(Command{Type: CmdFerryUnload, FerryID: id, LetterIDs: pick(f.Cargo)})
			case 2:
				if e.Apply(Command{Type: CmdRelayLoad, FerryID: id, LetterIDs: pick(e.tr.Relay(f.At))}).OK {
					st.relayLoaded++
				}
			case 3:
				ns := rpNeighbors(e.tr.Routes(), f.At)
				e.Apply(Command{Type: CmdFerryDepart, FerryID: id, To: ns[r.IntN(len(ns))]})
			}
			check()
		}
		e.RunUntil(e.Now() + SimTime(200+r.IntN(2000)))
		check()
	}
	return e, st
}

// relayRunConfig5 は 5 つの島の環で、積み替えありの設定。
func relayRunConfig5() Config {
	cfg := rpConfig5()
	cfg.RelayCap = 3
	return cfg
}

func TestRelayDeterminism(t *testing.T) { // SR-7・SR-8
	t.Parallel()
	for _, c := range []struct {
		name string
		cfg  func() Config
		seed uint64
	}{
		{"line/1", relayTestConfig, 1},
		{"line/2", relayTestConfig, 2},
		{"ring/1", relayRunConfig5, 1},
		{"ring/2", relayRunConfig5, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a, st := relayRun(t, c.cfg(), c.seed)
			if st.stored == 0 || st.relayLoaded == 0 {
				t.Fatalf("no relay in the round: %+v", st)
			}
			b, _ := relayRun(t, c.cfg(), c.seed)
			if a.TraceHash() != b.TraceHash() || !reflect.DeepEqual(a.InputLog(), b.InputLog()) {
				t.Fatal("same input, different trace or input log")
			}
			re := mustReplay(t, c.cfg(), rpLog(c.seed, a.InputLog(), c.cfg().DurationMs), Options{CheckInvariants: true})
			if re.TraceHash() != a.TraceHash() {
				t.Error("Replay: different trace hash")
			}
			if !reflect.DeepEqual(re.Snapshot(), a.Snapshot()) {
				t.Error("Replay: different snapshot")
			}
		})
	}
}

// SR-9（Engine の中の確かめ）: 積み替えを使わないラウンドは、RelayCap が 0 でも 12 でもトレースのハッシュが同じ。
// ボット（greedy・steady・random）での確かめは、bot が sim を import するため simcli の relay_hash_test.go で行う。
func TestRelayCapUnusedSameTrace(t *testing.T) {
	t.Parallel()
	for _, seed := range []uint64{1, 2, 3} {
		run := func(relayCap int) [32]byte {
			cfg := relayTestConfig()
			cfg.Raft.ElectionTick = 40
			cfg.RelayCap = relayCap
			e := newEngine(t, cfg, Options{CheckInvariants: true})
			r := rand.New(rand.NewPCG(seed, 3))
			mustApply(t, e, Command{Type: CmdGameStart})
			for e.Phase() != PhaseEnded {
				for _, f := range e.tr.Ferries() {
					if f.State != ferry.Docked {
						continue
					}
					id := string(f.ID)
					switch r.IntN(4) {
					case 0:
						if port := e.tr.Port(f.At); len(port) > 0 {
							e.Apply(Command{Type: CmdFerryLoad, FerryID: id, LetterIDs: []uint64{uint64(port[r.IntN(len(port))])}})
						}
					case 1: // 送信元の手紙だけを降ろす（経由の手紙は降ろさない）
						var own []uint64
						for _, lid := range f.Cargo {
							if l, _ := e.tr.Letter(lid); l.From == f.At {
								own = append(own, uint64(lid))
							}
						}
						if len(own) > 0 {
							e.Apply(Command{Type: CmdFerryUnload, FerryID: id, LetterIDs: own})
						}
					case 2:
						ns := rpNeighbors(e.tr.Routes(), f.At)
						e.Apply(Command{Type: CmdFerryDepart, FerryID: id, To: ns[r.IntN(len(ns))]})
					}
				}
				if r.IntN(5) == 0 {
					data, err := ledger.Encode(reserve("s-0001"))
					if err != nil {
						t.Fatal(err)
					}
					_ = e.propose(1+uint64(r.IntN(3)), data) // Leader がいなければ捨てられる（どちらの設定でも同じ）
					e.afterStep()
				}
				e.RunUntil(e.Now() + SimTime(200+r.IntN(2000)))
			}
			return e.TraceHash()
		}
		if run(0) != run(12) {
			t.Errorf("seed %d: trace differs between relayCap 0 and 12", seed)
		}
	}
}
