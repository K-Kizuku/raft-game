package sim

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// ndAdvancer は 500ms ずつ時間を進め、query が true なら RunUntil のたびに全ノードの NodeDetail を呼ぶ（ND-1）。
func ndAdvancer(t *testing.T, query bool, calls *int) detAdvancer {
	return func(e *Engine, to SimTime) {
		for e.Phase() == PhaseSailing && e.Now() < to {
			e.RunUntil(min(e.Now()+500, to))
			if !query {
				continue
			}
			for id := uint64(1); id <= uint64(e.cfg.Islands); id++ {
				if _, ok := e.NodeDetail(id); !ok {
					t.Fatalf("NodeDetail(%d) at %d = false in phase %s", id, e.Now(), e.Phase())
				}
				*calls++
			}
		}
	}
}

func eventsJSON(t *testing.T, evs []DomainEvent) []string {
	t.Helper()
	out := make([]string, len(evs))
	for i, ev := range evs {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal event %d: %v", i, err)
		}
		out[i] = string(b)
	}
	return out
}

func TestNodeDetailDoesNotChangeTrace(t *testing.T) { // ND-1
	t.Parallel()
	for _, c := range detCases(2, 2) {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := detRecord(t, c.cfg, c.seed, true)
			detCheckEnd(t, c.cfg, rec)
			calls := 0
			a := detReplay(t, c.cfg, c.seed, rec.Ops, Options{}, ndAdvancer(t, true, &calls), false)
			b := detReplay(t, c.cfg, c.seed, rec.Ops, Options{}, ndAdvancer(t, false, nil), false)
			if calls == 0 {
				t.Fatal("NodeDetail was never called")
			}
			if a.Phase != PhaseEnded || b.Phase != PhaseEnded {
				t.Fatalf("phase = %s, %s; want ended", a.Phase, b.Phase)
			}
			if a.Hash != b.Hash {
				t.Error("TraceHash differs between the engine with NodeDetail and without")
			}
			if !reflect.DeepEqual(eventsJSON(t, a.Events), eventsJSON(t, b.Events)) {
				t.Errorf("events differ: %s", detDiff(a.Events, b.Events))
			}
			if a.Hash != rec.Hash {
				t.Error("TraceHash differs from the record")
			}
		})
	}
}

func TestNodeDetailUnavailable(t *testing.T) { // ND-2
	t.Parallel()
	e := newEngine(t, testConfig(), Options{})
	for id := uint64(0); id <= 4; id++ {
		if _, ok := e.NodeDetail(id); ok {
			t.Errorf("intro: NodeDetail(%d) = true, want false", id)
		}
	}
	e = startedEngine(t)
	for _, id := range []uint64{0, 4} {
		if _, ok := e.NodeDetail(id); ok {
			t.Errorf("NodeDetail(%d) = true, want false", id)
		}
	}
	for id := uint64(1); id <= 3; id++ {
		if _, ok := e.NodeDetail(id); !ok {
			t.Errorf("NodeDetail(%d) = false, want true", id)
		}
	}
}

// mustDetail は NodeDetail(id) を返す。false なら失敗にする。
func mustDetail(t *testing.T, e *Engine, id uint64) NodeDetail {
	t.Helper()
	d, ok := e.NodeDetail(id)
	if !ok {
		t.Fatalf("NodeDetail(%d) = false", id)
	}
	return d
}

// checkDetailMatchesState は、詳細の値が最後に出した node.state（Snapshot の nodes）と同じかを確かめる。
func checkDetailMatchesState(t *testing.T, e *Engine, d NodeDetail) {
	t.Helper()
	ns := e.Snapshot().Nodes[d.ID-1]
	got := NodeState{ID: d.ID, Role: d.Role, Term: d.Term, Vote: d.Vote, Lead: d.Lead, Commit: d.Commit, Applied: d.Applied, LastIndex: d.LastIndex}
	if got != ns {
		t.Errorf("node %d: detail %+v, want node.state %+v", d.ID, got, ns)
	}
}

func TestNodeDetailLeader(t *testing.T) { // ND-3
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(1000)
	mustPropose(t, e, 1, reserve("s-0001"))
	mustPropose(t, e, 1, reserve("s-0002"))
	e.DrainEvents()

	before := e.TraceHash()
	seq, input, now := e.seq, len(e.input), e.Now()
	d := mustDetail(t, e, 1)
	if e.TraceHash() != before || e.seq != seq || len(e.input) != input || e.Now() != now || len(e.DrainEvents()) != 0 {
		t.Fatal("NodeDetail changed the engine")
	}

	checkDetailMatchesState(t, e, d)
	if d.Role != "leader" || d.Vote != 1 || d.Lead != 1 || d.TicksSinceLeaderContact != 0 {
		t.Errorf("detail = %+v, want leader voting for itself with 0 ticks", d)
	}
	if len(d.Log) == 0 || len(d.Log) > nodeDetailLogLimit {
		t.Fatalf("len(Log) = %d", len(d.Log))
	}
	if last := d.Log[len(d.Log)-1].Index; last != d.LastIndex {
		t.Errorf("last log index = %d, want %d", last, d.LastIndex)
	}
	summaries := []string{}
	for i, ent := range d.Log {
		if i > 0 && ent.Index != d.Log[i-1].Index+1 {
			t.Errorf("log[%d].Index = %d, want %d", i, ent.Index, d.Log[i-1].Index+1)
		}
		if ent.Committed != (ent.Index <= d.Commit) {
			t.Errorf("log[%d] (index %d) Committed = %v, commit %d", i, ent.Index, ent.Committed, d.Commit)
		}
		if ent.Type == "normal" && ent.Summary != "" {
			summaries = append(summaries, ent.Summary)
		}
	}
	if want := []string{"stall.reserve s-0001", "stall.reserve s-0002"}; !reflect.DeepEqual(summaries, want) {
		t.Errorf("summaries = %q, want %q", summaries, want)
	}
	if d.Log[0].Type != "conf_change" || d.Log[0].Summary != "" {
		t.Errorf("log[0] = %+v, want conf_change with empty summary", d.Log[0])
	}
	if d.Log[len(d.Log)-1].Committed {
		t.Errorf("last entry %+v is committed before replication", d.Log[len(d.Log)-1])
	}

	if len(d.Progress) != 2 || d.Progress[0].ID != 2 || d.Progress[1].ID != 3 {
		t.Fatalf("Progress = %+v, want followers 2 and 3", d.Progress)
	}
	for _, pr := range d.Progress {
		if pr.Match > d.LastIndex || pr.Next <= pr.Match {
			t.Errorf("progress %+v: lastIndex %d", pr, d.LastIndex)
		}
		switch pr.State {
		case "probe", "replicate", "snapshot":
		default:
			t.Errorf("progress %d state = %q", pr.ID, pr.State)
		}
	}

	want := []ResourceView{
		{ID: "firework:a", Kind: "firework", Holders: []string{}},
		{ID: "stall:main", Kind: "stall", Holders: []string{}},
	}
	if !reflect.DeepEqual(d.Ledger, want) {
		t.Errorf("Ledger = %+v, want %+v", d.Ledger, want)
	}
}

func TestNodeDetailLedgerHolders(t *testing.T) {
	t.Parallel()
	c := detCases(1, 0)[0]
	rec := detRecord(t, c.cfg, c.seed, true)
	e := detReplayEngine(t, c.cfg, c.seed, rec.Ops)
	found := false
	for id := uint64(1); id <= uint64(c.cfg.Islands); id++ {
		d := mustDetail(t, e, id)
		snap := e.ledgers[id-1].Snapshot()
		for i, r := range d.Ledger {
			if r.ID != snap.Resources[i].ID || !reflect.DeepEqual(r.Holders, snap.Resources[i].Holders) {
				t.Errorf("node %d: ledger[%d] = %+v, want %+v", id, i, r, snap.Resources[i])
			}
			if len(r.Holders) > 0 {
				found = true
				r.Holders[0] = "changed"
				if e.ledgers[id-1].Snapshot().Resources[i].Holders[0] == "changed" {
					t.Error("Holders shares memory with the ledger")
				}
			}
		}
		if d.Role != "leader" && d.Progress != nil {
			t.Errorf("node %d (%s): Progress = %+v, want nil", id, d.Role, d.Progress)
		}
	}
	if !found {
		t.Skip("no reservation in the record")
	}
}

// detReplayEngine は ops を再生した Engine を返す（ラウンドの終わりまで）。
func detReplayEngine(t *testing.T, cfg Config, seed uint64, ops []detOp) *Engine {
	t.Helper()
	p := newDetPlayer(t, cfg, seed, ops, Options{}, detJump(cfg, seed), false)
	for !p.done() {
		p.step()
	}
	p.finish()
	return p.e
}

func TestNodeDetailTicksSinceLeaderContact(t *testing.T) { // ND-4
	t.Parallel()
	e := startedEngine(t)
	tickMs := e.cfg.Raft.TickMs
	check := func(id uint64, want int) {
		t.Helper()
		d := mustDetail(t, e, id)
		if d.TicksSinceLeaderContact != want {
			t.Errorf("t=%d node %d: TicksSinceLeaderContact = %d, want %d", e.Now(), id, d.TicksSinceLeaderContact, want)
		}
		if id != 1 && d.Progress != nil {
			t.Errorf("node %d: Progress = %+v, want nil", id, d.Progress)
		}
	}
	check(1, 0)
	check(2, 0)
	for _, at := range []SimTime{tickMs, 2 * tickMs, 2*tickMs + tickMs/5} {
		e.RunUntil(at)
		check(1, 0)
		check(2, int(at/tickMs))
		check(3, int(at/tickMs))
	}

	// Leader（島 1）から島 2 への手紙を 1 通、船で届ける。
	var lid ferry.LetterID
	for _, id := range e.tr.Port(1) {
		if l, ok := e.tr.Letter(id); ok && l.To == 2 {
			lid = id
			break
		}
	}
	if lid == 0 {
		t.Fatal("no letter from 1 to 2 in port 1")
	}
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{uint64(lid)}})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	f, _ := e.tr.Ferry("f1")
	arrive := SimTime(f.ArriveAt)
	if arrive%tickMs == 0 {
		t.Fatalf("arrival %d is on a tick (test setup)", arrive)
	}
	e.RunUntil(arrive - 1)
	check(2, int((arrive-1)/tickMs))
	e.RunUntil(arrive)
	check(1, 0)
	check(2, 0)
	check(3, int(arrive/tickMs))
	next := (arrive/tickMs + 1) * tickMs
	e.RunUntil(next)
	check(2, int((next-arrive)/tickMs))
	e.RunUntil(arrive + tickMs)
	check(2, 1)
}

func TestNodeDetailLogLimit(t *testing.T) { // ND-5
	t.Parallel()
	e := startedEngine(t)
	for i := range nodeDetailLogLimit + 10 {
		mustPropose(t, e, 1, reserve(fmt.Sprintf("s-%04d", i+1)))
	}
	d := mustDetail(t, e, 1)
	if d.LastIndex <= nodeDetailLogLimit {
		t.Fatalf("LastIndex = %d, want > %d", d.LastIndex, nodeDetailLogLimit)
	}
	if len(d.Log) != nodeDetailLogLimit {
		t.Fatalf("len(Log) = %d, want %d", len(d.Log), nodeDetailLogLimit)
	}
	if got := d.Log[len(d.Log)-1]; got.Index != d.LastIndex || got.Summary != fmt.Sprintf("stall.reserve s-%04d", nodeDetailLogLimit+10) {
		t.Errorf("last entry = %+v, want index %d", got, d.LastIndex)
	}
	if got := d.Log[0].Index; got != d.LastIndex-nodeDetailLogLimit+1 {
		t.Errorf("first entry index = %d, want %d", got, d.LastIndex-nodeDetailLogLimit+1)
	}
}

func TestNodeStateVote(t *testing.T) { // NS-1
	t.Parallel()
	e := newEngine(t, testConfig(), Options{})
	mustApply(t, e, Command{Type: CmdGameStart})
	evs := e.DrainEvents()
	last := map[uint64]NodeState{}
	for _, ev := range evs {
		ns, ok := ev.Data.(NodeState)
		if !ok {
			continue
		}
		last[ns.ID] = ns
		if ns.ID != 1 {
			continue
		}
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"term":2,"vote":1,"lead":1,`) {
			t.Errorf("node.state JSON = %s, want vote right after term", b)
		}
	}
	if last[1].Vote != 1 {
		t.Errorf("leader vote = %d, want 1", last[1].Vote)
	}
	for _, n := range e.Snapshot().Nodes {
		if n != last[n.ID] {
			t.Errorf("Snapshot node %d = %+v, want last node.state %+v", n.ID, n, last[n.ID])
		}
	}
}
