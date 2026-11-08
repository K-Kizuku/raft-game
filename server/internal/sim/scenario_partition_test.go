package sim

import (
	"bytes"
	"math/rand/v2"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"github.com/K-Kizuku/raft-game/server/internal/cluster"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// 分断・復旧・古い手紙・重複・依頼の再送のシナリオテスト（T-110・SC-1〜SC-3）。

// 台本の時間（台本の定数で、ゲームの数値ではない）。Raft の選挙の時間（ElectionTick 40 × 500ms = 20〜40 秒）と
// 船の 1 周（fb 15 秒・fm 10.5 秒）から決めた。
const (
	scnBeforeMs    SimTime = 60000  // 分断の前の期限
	scnPartitionMs SimTime = 360000 // 分断の長さ
	scnConvergeMs  SimTime = 120000 // 収束の期限
)

// scnAtOneFrom3 は SC-1・SC-2 の分断の前の hook。台帳を見る島が reqIDs を持ち、fb が島 3 から島 1 に着いた瞬間に true。
func scnAtOneFrom3(e *Engine, fb *scnTour, ids []uint64, reqIDs ...string) func() bool {
	return func() bool {
		if !scnHasReq(e, ids, reqIDs...) {
			return false
		}
		f, _ := e.tr.Ferry(ferry.FerryID(fb.ferry))
		return f.State == ferry.Docked && f.At == 1 && fb.from == 3 && SimTime(f.ReadyAt) == e.Now()+SimTime(e.cfg.DockMs)
	}
}

// scnCheckFireworkOnce は FR-REQ-06 の最後の検査（SC-1 の 9.）。
func scnCheckFireworkOnce(t *testing.T, e *Engine, r *scnRec) {
	t.Helper()
	for id := uint64(1); id <= uint64(e.cfg.Islands); id++ {
		for _, c := range []struct {
			req  string
			want []string
		}{
			{"f-1", []string{"ok", "duplicate"}},
			{"s-1", []string{"ok"}},
			{"s-2", []string{"ok"}},
		} {
			if got := scnResults(r.evs, id, c.req); !reflect.DeepEqual(got, c.want) {
				t.Errorf("island %d: %s = %v, want %v", id, c.req, got, c.want)
			}
		}
		s := e.ledgers[id-1].Snapshot()
		i := slices.IndexFunc(s.Resources, func(rs ledger.ResourceState) bool { return rs.ID == "firework:a" })
		if i < 0 || !reflect.DeepEqual(s.Resources[i].Holders, []string{"f-1"}) {
			t.Errorf("island %d: firework:a = %+v, want holders [f-1]", id, s.Resources)
		}
	}
	data, err := ledger.Encode(scnFirework("f-1"))
	if err != nil {
		t.Fatal(err)
	}
	for i, n := range e.nodes {
		l, err := n.Log()
		if err != nil {
			t.Fatalf("node %d: Log: %v", i+1, err)
		}
		var hits []cluster.LogEntry
		for _, ent := range l {
			if bytes.Equal(ent.Data, data) {
				hits = append(hits, ent)
			}
		}
		if len(hits) != 2 || hits[0].Term <= 2 || hits[1].Term <= 2 {
			t.Errorf("node %d: f-1 entries = %+v, want 2 with Term > 2", i+1, hits)
		}
	}
	if e.Phase() != PhaseSailing {
		t.Errorf("Phase = %s, want sailing", e.Phase())
	}
}

// scnTypeCount は型の名前ごとの数（名前の昇順に並べる。map を使わない）。
type scnTypeCount struct {
	name string
	n    int
}

// scnCountType は cs（名前の昇順）の name の数を 1 増やす。
func scnCountType(cs []scnTypeCount, name string) []scnTypeCount {
	i, ok := slices.BinarySearchFunc(cs, name, func(c scnTypeCount, s string) int { return strings.Compare(c.name, s) })
	if ok {
		cs[i].n++
		return cs
	}
	return slices.Insert(cs, i, scnTypeCount{name: name, n: 1})
}

// scnNodeView は漂流瓶の前後で比べるノードの値。
type scnNodeView struct {
	term, lead, commit uint64
	log                []cluster.LogEntry
}

func scnViews(t *testing.T, e *Engine) []scnNodeView {
	t.Helper()
	out := make([]scnNodeView, len(e.nodes))
	for i, n := range e.nodes {
		st := n.Status()
		l, err := n.Log()
		if err != nil {
			t.Fatalf("node %d: Log: %v", i+1, err)
		}
		out[i] = scnNodeView{term: st.Term, lead: st.Lead, commit: st.Commit, log: l}
	}
	return out
}

// scnDrift は SC-3（漂流瓶）。scnLeaderInMinority の 7. で呼ぶ。
func scnDrift(t *testing.T, e *Engine, r *scnRec, tours []*scnTour, seed uint64) {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, 0x5c3))
	tD := e.Now()
	nD := len(r.evs)
	before := scnViews(t, e)
	var sent, old, same int
	var types []scnTypeCount
	drop := func() bool {
		m := r.pool[rng.IntN(len(r.pool))]
		switch cur := e.nodes[m.GetTo()-1].Status().Term; {
		case m.GetTerm() < cur:
			old++
		case m.GetTerm() == cur:
			same++
		}
		deliverRaw(t, e, proto.Clone(m).(*pb.Message))
		sent++
		types = scnCountType(types, m.GetType().String())
		return false
	}
	scnRun(t, e, r, tD+60000, tours, drop)
	if !scnRun(t, e, r, e.Now()+scnConvergeMs, tours, func() bool { return scnConverged(t, e) }) {
		scnFatal(t, e, r, "not converged after drift bottles")
	}
	evs := r.evs[nD:]
	if sent < 200 {
		t.Errorf("drift bottles = %d, want >= 200", sent)
	}
	if old < 20 || same < 20 {
		t.Errorf("old, same = %d, %d; want >= 20 each", old, same)
	}
	var pool []scnTypeCount
	for _, m := range r.pool {
		pool = scnCountType(pool, m.GetType().String())
	}
	for _, typ := range []pb.MessageType{
		pb.MsgApp, pb.MsgAppResp, pb.MsgHeartbeat, pb.MsgHeartbeatResp,
		pb.MsgPreVote, pb.MsgPreVoteResp, pb.MsgVote, pb.MsgVoteResp,
	} {
		if !slices.ContainsFunc(pool, func(c scnTypeCount) bool { return c.name == typ.String() }) {
			t.Errorf("pool has no %s: %v", typ, pool)
		}
	}
	if got := ofType[LedgerApplied](evs); len(got) != 0 {
		t.Errorf("ledger.applied after drift = %+v, want none", got)
	}
	if got := ofType[LeaderElected](evs); len(got) != 0 {
		t.Errorf("leader_elected after drift = %+v, want none", got)
	}
	if got := ofType[SteppedDown](evs); len(got) != 0 {
		t.Errorf("stepped_down after drift = %+v, want none", got)
	}
	if after := scnViews(t, e); !reflect.DeepEqual(after, before) {
		t.Errorf("nodes changed after drift:\n before %+v\n after  %+v", before, after)
	}
	var b strings.Builder
	for _, c := range types {
		b.WriteString(" " + c.name + "=" + strconv.Itoa(c.n))
	}
	t.Logf("seed %d: drift: bottles=%d old=%d same=%d pool=%d types:%s", seed, sent, old, same, len(r.pool), b.String())
}

// scnLeaderInMinority は SC-1（drift が真なら SC-3 も）の台本。Leader の島 1 が少数派 {1, 2} に残る分断。
func scnLeaderInMinority(t *testing.T, seed uint64, drift bool) {
	t.Helper()
	e := scnStart(t, scnConfig5(), seed)
	r := &scnRec{}
	r.drain(e)
	fb := &scnTour{ferry: "fb", stops: []uint64{1, 2, 1, 3}}
	fm := &scnTour{ferry: "fm", stops: []uint64{3, 4, 5}}
	tours := []*scnTour{fb, fm}
	dock := SimTime(e.cfg.DockMs)

	// 1. 始め
	mustPropose(t, e, 1, scnStall("s-1"))

	// 2. 分断の前
	if !scnRun(t, e, r, scnBeforeMs, tours, scnAtOneFrom3(e, fb, []uint64{1, 2, 3}, "s-1")) {
		scnFatal(t, e, r, "s-1 not replicated to 1-3 by %d", scnBeforeMs)
	}
	tP := e.Now()
	st1 := e.nodes[0].Status()
	cA := st1.Commit
	if st1.Role != raft.StateLeader || st1.Term != 2 {
		scnFatal(t, e, r, "island 1 at tP = %+v, want leader term 2", st1)
	}
	for _, id := range []uint64{1, 2, 3} {
		if st := e.nodes[id-1].Status(); st.Applied != cA {
			scnFatal(t, e, r, "island %d at tP: Applied %d, want %d", id, st.Applied, cA)
		}
	}
	for _, id := range []uint64{4, 5} {
		if st := e.nodes[id-1].Status(); st.Commit >= cA {
			scnFatal(t, e, r, "island %d at tP: Commit %d, want < %d", id, st.Commit, cA)
		}
	}

	// 3. 分断
	tH := tP + scnPartitionMs
	if err := e.tr.Block([][2]uint64{{1, 3}}, int64(tH)); err != nil {
		t.Fatalf("Block: %v", err)
	}
	fb.setStops([]uint64{1, 2})
	mustPropose(t, e, 1, scnFirework("f-1"))
	iX := e.nodes[0].Status().LastIndex
	e.RunUntil(tP + dock)
	nIn := len(e.InputLog())
	if ack := e.Apply(Command{Type: CmdFerryDepart, FerryID: "fb", To: 3}); ack.OK || ack.Code != CodeRouteBlocked {
		t.Errorf("depart fb to 3 = %+v, want route_blocked", ack)
	}
	if n := len(e.InputLog()); n != nIn {
		t.Errorf("len(InputLog) = %d after failed depart, want %d", n, nIn)
	}
	var lead, t3 uint64
	var tL SimTime
	watch := func() bool {
		if lead != 0 {
			return false
		}
		if l := scnLeaderAmong(e, []uint64{3, 4, 5}); l != 0 {
			lead, t3, tL = l, e.nodes[l-1].Status().Term, e.Now()
			mustPropose(t, e, l, scnFirework("f-1")) // X の再送
			mustPropose(t, e, l, scnStall("s-2"))
		}
		return false
	}
	scnRun(t, e, r, tH, tours, watch)

	// 4. 分断の間の検査
	part := scnBetween(r.evs, tP, tH)
	for _, id := range []uint64{1, 2} {
		for _, s := range scnStates(part, id) {
			if s.Commit > cA || s.Applied > cA {
				t.Errorf("V-1: island %d node.state %+v, want Commit, Applied <= %d", id, s, cA)
			}
			if s.Term != 2 {
				t.Errorf("PreVote: island %d node.state %+v, want Term 2", id, s)
			}
		}
		for _, a := range ofType[LedgerApplied](part) {
			if a.IslandID == id {
				t.Errorf("V-1: island %d applied %+v during partition", id, a)
			}
		}
	}
	if !slices.ContainsFunc(scnStates(part, 2), func(s NodeState) bool { return s.LastIndex >= iX }) {
		t.Errorf("island 2 never got X (index %d) during partition", iX)
	}
	if !slices.Contains(ofType[SteppedDown](part), SteppedDown{ID: 1, Term: 2, Reason: "check_quorum"}) {
		t.Errorf("CheckQuorum: stepped_down = %+v, want {1 2 check_quorum}", ofType[SteppedDown](part))
	}
	if lead != 3 {
		scnFatal(t, e, r, "leader in majority = %d, want 3", lead)
	}
	if t3 <= 2 {
		t.Errorf("T3 = %d, want > 2", t3)
	}
	if !scnHasReq(e, []uint64{3, 4, 5}, "f-1", "s-2") {
		scnFatal(t, e, r, "majority did not commit f-1, s-2 by tH")
	}
	termH := e.nodes[2].Status().Term

	// 5. 復旧
	fb.setStops([]uint64{1, 3, 1, 2})
	nH := len(r.evs)
	if !scnRun(t, e, r, tH+scnConvergeMs, tours, func() bool { return scnConverged(t, e) }) {
		scnFatal(t, e, r, "not converged by tH + %d", scnConvergeMs)
	}
	tC := e.Now()

	// 6. 復旧の検査
	rec := r.evs[nH:]
	for _, n := range e.nodes {
		if st := n.Status(); st.Lead != 3 || st.Term != termH {
			t.Errorf("V-2: node %d = %+v, want Lead 3, Term %d", st.ID, st, termH)
		}
	}
	if got := ofType[LeaderElected](rec); len(got) != 0 {
		t.Errorf("leader_elected after recovery = %+v, want none", got)
	}
	for _, d := range ofType[SteppedDown](rec) {
		if d.ID == 3 {
			t.Errorf("island 3 stepped down after recovery: %+v", d)
		}
	}
	stale := scnStaleDeliveries(r.evs, nH, e.cfg.Islands)
	if len(stale) == 0 {
		t.Errorf("V-4: no stale letter delivered after recovery")
	}
	if !slices.ContainsFunc(stale, func(l LetterView) bool { return l.From == 1 && l.To == 3 }) {
		t.Errorf("V-4: no stale letter from 1 to 3 delivered: %+v", stale)
	}
	ls := scnLetters(r.evs)
	if !slices.ContainsFunc(ofType[LetterStale](rec), func(d LetterStale) bool { return scnLetter(ls, d.LetterID).From == 1 }) {
		t.Errorf("V-4: no letter.stale for letters from 1")
	}
	for i, n := range e.nodes {
		l, err := n.Log()
		if err != nil {
			t.Fatalf("node %d: Log: %v", i+1, err)
		}
		if slices.ContainsFunc(l, func(ent cluster.LogEntry) bool { return ent.Index == iX && ent.Term == 2 }) {
			t.Errorf("V-4: node %d still has X (index %d, term 2)", i+1, iX)
		}
	}

	// 7. 漂流瓶
	if drift {
		scnDrift(t, e, r, tours, seed)
	}

	// 8. Commit の後の再送
	mustPropose(t, e, 3, scnFirework("f-1"))
	if !scnRun(t, e, r, e.Now()+scnConvergeMs, tours, func() bool { return scnConverged(t, e) }) {
		scnFatal(t, e, r, "not converged after resend")
	}

	// 9. 最後の検査
	scnCheckFireworkOnce(t, e, r)
	t.Logf("seed %d: tP=%d leader=%d elected at %d (T3=%d) term at tH=%d converged tC-tH=%d", seed, tP, lead, tL, t3, termH, tC-tH)
}

func TestScenarioPartitionLeaderInMinority(t *testing.T) { // SC-1
	t.Parallel()
	for seed := uint64(1); seed <= 6; seed++ {
		t.Run("seed="+strconv.FormatUint(seed, 10), func(t *testing.T) {
			t.Parallel()
			scnLeaderInMinority(t, seed, false)
		})
	}
}

func TestScenarioDriftBottles(t *testing.T) { // SC-3
	t.Parallel()
	for seed := uint64(1); seed <= 4; seed++ {
		t.Run("seed="+strconv.FormatUint(seed, 10), func(t *testing.T) {
			t.Parallel()
			scnLeaderInMinority(t, seed, true)
		})
	}
}

func TestScenarioPartitionFollowerIsolated(t *testing.T) { // SC-2
	t.Parallel()
	for seed := uint64(1); seed <= 6; seed++ {
		t.Run("seed="+strconv.FormatUint(seed, 10), func(t *testing.T) {
			t.Parallel()
			scnFollowerIsolated(t, seed)
		})
	}
}

// scnFollowerIsolated は SC-2 の台本。Follower の島 3 が 1 つだけ孤立する分断。
func scnFollowerIsolated(t *testing.T, seed uint64) {
	t.Helper()
	e := scnStart(t, scnConfig3(), seed)
	r := &scnRec{}
	r.drain(e)
	fb := &scnTour{ferry: "fb", stops: []uint64{1, 2, 1, 3}}
	tours := []*scnTour{fb}

	// 1. 始め
	mustPropose(t, e, 1, scnStall("s-1"))

	// 2. 分断の前
	if !scnRun(t, e, r, scnBeforeMs, tours, scnAtOneFrom3(e, fb, []uint64{1, 2, 3}, "s-1")) {
		scnFatal(t, e, r, "s-1 not replicated by %d", scnBeforeMs)
	}
	tP := e.Now()
	st3 := e.nodes[2].Status()
	c0 := st3.Commit
	if st3.Applied != c0 {
		scnFatal(t, e, r, "island 3 at tP = %+v, want Applied == Commit", st3)
	}

	// 3. 分断
	tH := tP + scnPartitionMs
	if err := e.tr.Block([][2]uint64{{1, 3}}, int64(tH)); err != nil {
		t.Fatalf("Block: %v", err)
	}
	fb.setStops([]uint64{1, 2})
	mustPropose(t, e, 1, scnStall("s-2"))
	mustPropose(t, e, 1, scnStall("s-3"))
	mustPropose(t, e, 1, scnFirework("f-1"))
	scnRun(t, e, r, tH, tours, nil)

	// 4. 分断の間の検査
	part := scnBetween(r.evs, tP, tH)
	states := scnStates(part, 3)
	for _, s := range states {
		if s.Commit > c0 || s.LastIndex > c0 {
			t.Errorf("V-1: island 3 node.state %+v, want Commit, LastIndex <= %d", s, c0)
		}
		if s.Term != 2 {
			t.Errorf("PreVote: island 3 node.state %+v, want Term 2", s)
		}
		if s.Role == "candidate" {
			t.Errorf("PreVote: island 3 became candidate: %+v", s)
		}
	}
	if !slices.ContainsFunc(states, func(s NodeState) bool { return s.Role == "pre_candidate" }) {
		t.Errorf("PreVote: island 3 never became pre_candidate: %+v", states)
	}
	for _, a := range ofType[LedgerApplied](part) {
		if a.IslandID == 3 {
			t.Errorf("V-1: island 3 applied %+v during partition", a)
		}
	}
	if !scnHasReq(e, []uint64{1, 2}, "s-2", "s-3", "f-1") {
		scnFatal(t, e, r, "majority did not commit s-2, s-3, f-1 by tH")
	}

	// 5. 復旧
	fb.setStops([]uint64{1, 3, 1, 2})
	nH := len(r.evs)
	if !scnRun(t, e, r, tH+scnConvergeMs, tours, func() bool { return scnConverged(t, e) }) {
		scnFatal(t, e, r, "not converged by tH + %d", scnConvergeMs)
	}
	tC := e.Now()
	// 収束は、島 3 の港に残った PreVote が島 1 に届く前に起きる（fb が島 3 に着き、島 1 の手紙を配達した時点）。
	// そのため、その PreVote が島 1 に届き、そのうえで全ノードがそろうまで続けて進める（V-4・ADR-0007）。
	prevoteDelivered := func() bool {
		ls := scnLetters(r.evs)
		return slices.ContainsFunc(ofType[LetterDelivered](r.evs[nH:]), func(d LetterDelivered) bool {
			l := scnLetter(ls, d.LetterID)
			return l.From == 3 && l.To == 1 && l.Label == "prevote" && l.Raft != nil && l.Raft.Term > 2
		})
	}
	if !scnRun(t, e, r, e.Now()+scnConvergeMs, tours, func() bool { return prevoteDelivered() && scnConverged(t, e) }) {
		if !prevoteDelivered() {
			t.Errorf("V-4: no prevote from 3 to 1 with Term > 2 delivered after recovery")
		}
		scnFatal(t, e, r, "not converged after the prevote from 3 reached 1")
	}

	// 6. 検査
	for _, n := range e.nodes {
		if st := n.Status(); st.Lead != 1 || st.Term != 2 {
			t.Errorf("V-2: node %d = %+v, want Lead 1, Term 2", st.ID, st)
		}
	}
	all := scnBetween(r.evs, 1, e.Now()+1)
	if got := ofType[LeaderElected](all); len(got) != 0 {
		t.Errorf("leader_elected = %+v, want none", got)
	}
	if got := ofType[SteppedDown](all); len(got) != 0 {
		t.Errorf("stepped_down = %+v, want none", got)
	}
	for id := uint64(1); id <= 3; id++ {
		for _, c := range []struct {
			req  string
			want []string
		}{
			{"s-1", []string{"ok"}},
			{"s-2", []string{"ok"}},
			{"s-3", []string{"rejected"}},
			{"f-1", []string{"ok"}},
		} {
			if got := scnResults(r.evs, id, c.req); !reflect.DeepEqual(got, c.want) {
				t.Errorf("island %d: %s = %v, want %v", id, c.req, got, c.want)
			}
		}
	}
	t.Logf("seed %d: tP=%d tC-tH=%d", seed, tP, tC-tH)
}
