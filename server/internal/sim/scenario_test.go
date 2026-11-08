package sim

import (
	"cmp"
	"reflect"
	"slices"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"github.com/K-Kizuku/raft-game/server/internal/cluster"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// シナリオテスト（T-110・DesignDoc §14）の台本の部品。名前はすべて scn で始める。

// scnPollMs は、船の到着・出航がなくても台本が起きる間隔（台本の定数で、ゲームの数値ではない）。
const scnPollMs SimTime = 250

// scnConfig5 は 5 島の台本の設定。DurationMs は、分断の長さを 2 倍にしたので 2 倍にした（台本がラウンドの途中で終わるように）。
// 1-3 が少数派 {1, 2} と多数派 {3, 4, 5} をつなぐ唯一の航路。
func scnConfig5() Config {
	cfg := testConfig()
	cfg.Islands = 5
	cfg.DurationMs = 1800000
	cfg.Routes = []ferry.RouteConfig{
		{A: 1, B: 2, TravelMs: 2000},
		{A: 1, B: 3, TravelMs: 2500},
		{A: 3, B: 4, TravelMs: 2000},
		{A: 4, B: 5, TravelMs: 2000},
		{A: 3, B: 5, TravelMs: 2000},
	}
	cfg.Ferries = []ferry.FerryConfig{
		{ID: "fb", Home: 1, Capacity: 6, SpeedPermille: 1000},
		{ID: "fm", Home: 3, Capacity: 6, SpeedPermille: 1000},
	}
	return cfg
}

// scnConfig3 は 3 島の台本の設定。DurationMs は scnConfig5 と同じ。
func scnConfig3() Config {
	cfg := testConfig()
	cfg.DurationMs = 1800000
	cfg.Routes = []ferry.RouteConfig{
		{A: 1, B: 2, TravelMs: 2000},
		{A: 1, B: 3, TravelMs: 2500},
	}
	cfg.Ferries = []ferry.FerryConfig{
		{ID: "fb", Home: 1, Capacity: 6, SpeedPermille: 1000},
	}
	return cfg
}

// scnStall は屋台の予約のコマンド。再送は同じ reqID でこれを作り直す（Data がバイト列まで同じになる）。
func scnStall(reqID string) ledger.Command {
	return ledger.Command{V: ledger.Version, ReqID: reqID, Origin: 1, Kind: ledger.KindStallReserve, Resource: "stall:main"}
}

// scnFirework は花火の予約のコマンド。
func scnFirework(reqID string) ledger.Command {
	return ledger.Command{V: ledger.Version, ReqID: reqID, Origin: 1, Kind: ledger.KindFireworkReserve, Resource: "firework:a"}
}

// scnStart は台本の Engine を作り、cmd.game.start を適用する（イベントは取り出さない）。
func scnStart(t *testing.T, cfg Config, seed uint64) *Engine {
	t.Helper()
	e, err := New(cfg, seed, Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustApply(t, e, Command{Type: CmdGameStart})
	return e
}

// scnTour は 1 隻の船の巡回の台本。stops を順にくり返し回る（最後の次は先頭）。
type scnTour struct {
	ferry string
	stops []uint64
	pos   int    // 今いる停泊地の stops の添字。-1 = 次に出航するときに決める
	from  uint64 // 最後に出航した島（0 = まだ出航していない）
}

// setStops は巡回を stops に切り替える。次に出航するときに、停泊している島が stops で最初に現れる位置から続ける。
func (r *scnTour) setStops(stops []uint64) {
	r.stops = slices.Clone(stops)
	r.pos = -1
}

// scnDrive は tours の順に、停泊していて出航できる（ReadyAt <= Now）船を 1 回ずつ動かす。
func scnDrive(t *testing.T, e *Engine, tours []*scnTour) {
	t.Helper()
	for _, r := range tours {
		f, _ := e.tr.Ferry(ferry.FerryID(r.ferry))
		if f.State != ferry.Docked || SimTime(f.ReadyAt) > e.Now() {
			continue
		}
		if r.pos == -1 {
			r.pos = slices.Index(r.stops, f.At)
			if r.pos == -1 {
				t.Fatalf("t=%d: ferry %s at %d is not in stops %v", e.Now(), r.ferry, f.At, r.stops)
			}
		} else if r.stops[r.pos] != f.At {
			t.Fatalf("t=%d: ferry %s at %d, want stops[%d] = %d", e.Now(), r.ferry, f.At, r.pos, r.stops[r.pos])
		}
		next := r.stops[(r.pos+1)%len(r.stops)]
		free := f.Capacity - len(f.Cargo)
		var load []uint64
		port := e.tr.Port(f.At)
		pick := func(want func(to uint64) bool) {
			for _, lid := range port {
				if len(load) >= free {
					return
				}
				l, ok := e.tr.Letter(lid)
				if !ok {
					t.Fatalf("t=%d: letter %d in port %d not found", e.Now(), lid, f.At)
				}
				if want(l.To) {
					load = append(load, uint64(lid))
				}
			}
		}
		pick(func(to uint64) bool { return to == next })
		pick(func(to uint64) bool { return to != next && to != f.At && slices.Contains(r.stops, to) })
		if len(load) > 0 {
			mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: r.ferry, LetterIDs: load})
		}
		mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: r.ferry, To: next})
		r.pos = (r.pos + 1) % len(r.stops)
		r.from = f.At
	}
}

// scnRec は台本の実行の記録。
type scnRec struct {
	evs  []DomainEvent    // DrainEvents の結果を、出た順に連結したもの
	pool []*pb.Message    // 港と船で見つけた Raft の手紙の中身の複製（見つけた順）。漂流瓶に使う
	seen []ferry.LetterID // pool に入れたか調べた手紙の ID（昇順。map を使わない）
}

// drain は e.DrainEvents() を evs に足す。テストは DrainEvents をこれ以外で呼ばない。
func (r *scnRec) drain(e *Engine) {
	r.evs = append(r.evs, e.DrainEvents()...)
}

// capture は、今の港（島 1..N の順）と船（e.tr.Ferries() の順）にある手紙のうち、まだ見ていない Raft の手紙の中身を、
// proto.Clone して pool に足す。
func (r *scnRec) capture(e *Engine) {
	take := func(lid ferry.LetterID) {
		// 手紙の ID は島ごとの番号（最大 6,000,000,000 台）なので、ID を添字にしない。
		i, found := slices.BinarySearch(r.seen, lid)
		if found {
			return
		}
		r.seen = slices.Insert(r.seen, i, lid)
		l, ok := e.tr.Letter(lid)
		if !ok || l.Kind != ferry.LetterRaft {
			return
		}
		r.pool = append(r.pool, proto.Clone(l.Raft).(*pb.Message))
	}
	for i := 1; i <= e.cfg.Islands; i++ {
		for _, lid := range e.tr.Port(uint64(i)) {
			take(lid)
		}
	}
	for _, f := range e.tr.Ferries() {
		for _, lid := range f.Cargo {
			take(lid)
		}
	}
}

// scnRun は until まで、または hook が true を返すまで、台本を進める。hook が true を返したら true を返す。
func scnRun(t *testing.T, e *Engine, r *scnRec, until SimTime, tours []*scnTour, hook func() bool) bool {
	t.Helper()
	for e.Now() < until {
		if e.Phase() != PhaseSailing {
			scnFatal(t, e, r, "phase %s before %d (DurationMs too short)", e.Phase(), until)
		}
		r.capture(e)
		scnDrive(t, e, tours)
		r.drain(e)
		if hook != nil && hook() {
			return true
		}
		w := e.Now() + scnPollMs
		for _, f := range e.tr.Ferries() {
			switch {
			case f.State == ferry.Sailing && SimTime(f.ArriveAt) < w:
				w = SimTime(f.ArriveAt)
			case f.State == ferry.Docked && SimTime(f.ReadyAt) > e.Now() && SimTime(f.ReadyAt) < w:
				w = SimTime(f.ReadyAt)
			}
		}
		e.RunUntil(min(w, until))
		if e.Phase() != PhaseSailing {
			r.drain(e)
			scnFatal(t, e, r, "phase %s before %d (DurationMs too short)", e.Phase(), until)
		}
	}
	r.drain(e)
	return false
}

// scnFatal は台本の前提が成り立たないときに、状態をログに出して t.Fatalf にする。
func scnFatal(t *testing.T, e *Engine, r *scnRec, format string, args ...any) {
	t.Helper()
	t.Logf("now = %d, phase = %s", e.Now(), e.Phase())
	for _, n := range e.nodes {
		t.Logf("status: %+v", n.Status())
	}
	for _, f := range e.tr.Ferries() {
		t.Logf("ferry: %+v", f)
	}
	if r != nil {
		for _, ev := range r.evs {
			switch d := ev.Data.(type) {
			case LeaderElected:
				t.Logf("t=%d leader_elected %+v", ev.T, d)
			case SteppedDown:
				t.Logf("t=%d stepped_down %+v", ev.T, d)
			}
		}
	}
	t.Fatalf(format, args...)
}

// scnConverged は、全ノードの状態がそろったかを返す（V-2）。
func scnConverged(t *testing.T, e *Engine) bool {
	t.Helper()
	var first []cluster.LogEntry
	for i, n := range e.nodes {
		st := n.Status()
		if st.Applied != st.Commit || st.Commit != st.LastIndex {
			return false
		}
		if st.Lead == 0 {
			return false
		}
		if i > 0 {
			st0 := e.nodes[0].Status()
			if st.Term != st0.Term || st.Commit != st0.Commit || st.Lead != st0.Lead {
				return false
			}
		}
		l, err := n.Log()
		if err != nil {
			t.Fatalf("node %d: Log: %v", i+1, err)
		}
		if i == 0 {
			first = l
		} else if !reflect.DeepEqual(l, first) {
			return false
		}
	}
	return true
}

// scnLetters は letter.created の手紙の表示用の値を、手紙の ID の昇順に並べる（ID は島ごとの番号なので添字にしない）。
func scnLetters(evs []DomainEvent) []LetterView {
	var out []LetterView
	for _, d := range ofType[LetterCreated](evs) {
		i, found := slices.BinarySearchFunc(out, d.Letter.ID, func(l LetterView, id uint64) int { return cmp.Compare(l.ID, id) })
		if found {
			out[i] = d.Letter
			continue
		}
		out = slices.Insert(out, i, d.Letter)
	}
	return out
}

// scnLetter は ls の id の手紙（なければ空の値）。
func scnLetter(ls []LetterView, id uint64) LetterView {
	i, found := slices.BinarySearchFunc(ls, id, func(l LetterView, id uint64) int { return cmp.Compare(l.ID, id) })
	if !found {
		return LetterView{}
	}
	return ls[i]
}

// scnResults はその島の ledger.applied のうち ReqID == reqID のものの Result を、出た順に返す。
func scnResults(evs []DomainEvent, island uint64, reqID string) []string {
	out := []string{}
	for _, d := range ofType[LedgerApplied](evs) {
		if d.IslandID == island && d.ReqID == reqID {
			out = append(out, d.Result)
		}
	}
	return out
}

// scnLeaderAmong は ids の順に見て、Leader の最初のノードの ID を返す（いなければ 0）。
func scnLeaderAmong(e *Engine, ids []uint64) uint64 {
	for _, id := range ids {
		if e.nodes[id-1].Status().Role == raft.StateLeader {
			return id
		}
	}
	return 0
}

// scnStaleDeliveries は「古い手紙の配達」を返す。evs を先頭から見て、島ごとに最後の node.state の Term を覚え、
// evs[from:] の letter.delivered のうち、手紙の Raft.Term が届いた島の覚えている Term より小さいものを数える。
func scnStaleDeliveries(evs []DomainEvent, from int, islands int) []LetterView {
	ls := scnLetters(evs)
	terms := make([]uint64, islands+1)
	var out []LetterView
	for i, ev := range evs {
		switch d := ev.Data.(type) {
		case NodeState:
			terms[d.ID] = d.Term
		case LetterDelivered:
			if i < from {
				continue
			}
			l := scnLetter(ls, d.LetterID)
			if l.Raft != nil && l.Raft.Term < terms[d.IslandID] {
				out = append(out, l)
			}
		}
	}
	return out
}

// scnBetween は evs のうち from <= T < to のものを返す。
func scnBetween(evs []DomainEvent, from, to SimTime) []DomainEvent {
	var out []DomainEvent
	for _, ev := range evs {
		if from <= ev.T && ev.T < to {
			out = append(out, ev)
		}
	}
	return out
}

// scnStates は evs のうち島 island の node.state を返す。
func scnStates(evs []DomainEvent, island uint64) []NodeState {
	var out []NodeState
	for _, d := range ofType[NodeState](evs) {
		if d.ID == island {
			out = append(out, d)
		}
	}
	return out
}

// scnHasReq は、ids の島の台帳がすべて reqIDs を持つかを返す。
func scnHasReq(e *Engine, ids []uint64, reqIDs ...string) bool {
	for _, id := range ids {
		for _, req := range reqIDs {
			if _, ok := e.ledgers[id-1].Lookup(req); !ok {
				return false
			}
		}
	}
	return true
}

// scnDeparts は evs の ferry.departed の (From, To) を順に返す。
func scnDeparts(evs []DomainEvent) [][2]uint64 {
	var out [][2]uint64
	for _, d := range ofType[FerryDeparted](evs) {
		out = append(out, [2]uint64{d.From, d.To})
	}
	return out
}

func TestScenarioHelpers(t *testing.T) { // SC-0
	t.Parallel()
	e := scnStart(t, scnConfig3(), testSeed)
	r := &scnRec{}
	r.drain(e)
	if !scnConverged(t, e) {
		t.Fatalf("not converged after start")
	}
	for i, n := range e.nodes {
		l, err := n.Log()
		if err != nil {
			t.Fatal(err)
		}
		if st := n.Status(); len(l) != 4 || st.Lead != 1 {
			t.Errorf("node %d: len(Log) = %d, Lead = %d; want 4, 1", i+1, len(l), st.Lead)
		}
	}
	mustPropose(t, e, 1, scnStall("s-1"))
	if scnConverged(t, e) {
		t.Fatalf("converged right after propose")
	}

	fb := &scnTour{ferry: "fb", stops: []uint64{1, 2, 1, 3}}
	tours := []*scnTour{fb}
	if !scnRun(t, e, r, 60000, tours, func() bool { return scnConverged(t, e) }) {
		scnFatal(t, e, r, "not converged by 60000")
	}
	deps := scnDeparts(r.evs)
	if want := [][2]uint64{{1, 2}, {2, 1}, {1, 3}, {3, 1}}; len(deps) < 4 || !reflect.DeepEqual(deps[:4], want) {
		t.Errorf("departures = %v, want prefix %v", deps, want)
	}
	if !slices.ContainsFunc(r.pool, func(m *pb.Message) bool { return m.GetType() == pb.MsgApp }) {
		t.Errorf("pool has no MsgApp (%d messages)", len(r.pool))
	}
	for id := uint64(1); id <= 3; id++ {
		if got := scnResults(r.evs, id, "s-1"); !reflect.DeepEqual(got, []string{"ok"}) {
			t.Errorf("island %d: s-1 = %v, want [ok]", id, got)
		}
	}

	// 島 1 に 2 から戻った（巡回のままなら次は 3）ときに、巡回を [1, 2] に切り替える。
	atOneFromTwo := func() bool {
		f, _ := e.tr.Ferry("fb")
		return f.State == ferry.Docked && f.At == 1 && fb.from == 2
	}
	if !scnRun(t, e, r, e.Now()+60000, tours, atOneFromTwo) {
		scnFatal(t, e, r, "fb did not dock at 1 from 2")
	}
	fb.setStops([]uint64{1, 2})
	n := len(r.evs)
	departed := func() bool { return len(scnDeparts(r.evs[n:])) > 0 }
	if !scnRun(t, e, r, e.Now()+60000, tours, departed) {
		scnFatal(t, e, r, "fb did not depart")
	}
	if got := scnDeparts(r.evs[n:]); got[0] != [2]uint64{1, 2} {
		t.Errorf("departure after setStops = %v, want (1, 2)", got[0])
	}
}
