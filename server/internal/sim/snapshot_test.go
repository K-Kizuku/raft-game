package sim

import (
	"cmp"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

func TestSnapshotIntro(t *testing.T) { // SN-1
	t.Parallel()
	e := newEngine(t, testConfig(), Options{CheckInvariants: true})
	want := Snapshot{
		Phase:    PhaseIntro,
		EndsAt:   120000,
		Ferries:  []FerryView{{ID: "f1", Capacity: 4, State: "docked", IslandID: 1, Cargo: []uint64{}}},
		Routes:   []RouteView{{ID: "1-2"}, {ID: "2-3"}},
		Letters:  []LetterView{},
		Requests: []RequestState{},
		Nodes:    []NodeState{{ID: 1, Role: "follower"}, {ID: 2, Role: "follower"}, {ID: 3, Role: "follower"}},
	}
	if got := e.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot =\n %+v\nwant\n %+v", got, want)
	}
}

func TestSnapshotAfterStart(t *testing.T) { // SN-2
	t.Parallel()
	e := startedEngine(t)
	s := e.Snapshot()
	if s.Phase != PhaseSailing || s.LastSeq != 6 {
		t.Errorf("Phase, LastSeq = %s, %d; want sailing, 6", s.Phase, s.LastSeq)
	}
	ns := func(id uint64, role string) NodeState {
		return NodeState{ID: id, Role: role, Term: 2, Vote: 1, Lead: 1, Commit: 4, Applied: 4, LastIndex: 4}
	}
	if want := []NodeState{ns(1, "leader"), ns(2, "follower"), ns(3, "follower")}; !reflect.DeepEqual(s.Nodes, want) {
		t.Errorf("Nodes = %+v, want %+v", s.Nodes, want)
	}
}

// loadedEngine は SN-3 の状況（開始 → RunUntil(1000) → load f1 [1]）を作り、t = 1000 の letter.created を返す。
func loadedEngine(t *testing.T) (*Engine, []LetterView) {
	t.Helper()
	e := startedEngine(t)
	e.RunUntil(1000)
	var created []LetterView
	for _, c := range ofType[LetterCreated](e.DrainEvents()) {
		created = append(created, c.Letter)
	}
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: lids(1, 1)})
	e.DrainEvents()
	return e, created
}

func TestSnapshotLetters(t *testing.T) { // SN-3・SN-4
	t.Parallel()
	e, created := loadedEngine(t)
	if len(created) != 2 || created[0].ID != lid(1, 1) || created[1].ID != lid(1, 2) {
		t.Fatalf("letter.created = %+v", created)
	}
	s := e.Snapshot()
	l1 := created[0]
	l1.Loc = ferryLoc("f1")
	if want := []LetterView{l1, created[1]}; !reflect.DeepEqual(s.Letters, want) {
		t.Errorf("Letters =\n %+v\nwant\n %+v", s.Letters, want)
	}
	if want := lids(1, 1); !reflect.DeepEqual(s.Ferries[0].Cargo, want) {
		t.Errorf("Cargo = %v, want %v", s.Ferries[0].Cargo, want)
	}

	// SN-4
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	want := FerryView{ID: "f1", Capacity: 4, State: "sailing", From: 1, To: 2, DepartAt: 1000, ArriveAt: 4750, ReadyAt: 6250, Cargo: lids(1, 1)}
	if got := e.Snapshot().Ferries[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("sailing ferry = %+v, want %+v", got, want)
	}
	e.RunUntil(4750)
	s = e.Snapshot()
	want = FerryView{ID: "f1", Capacity: 4, State: "docked", IslandID: 2, ReadyAt: 6250, Cargo: []uint64{}}
	if got := s.Ferries[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("docked ferry = %+v, want %+v", got, want)
	}
	if slices.ContainsFunc(s.Letters, func(l LetterView) bool { return l.ID == 1 }) {
		t.Error("delivered letter 1 is in Letters")
	}
	port2 := e.tr.Port(2)
	if len(port2) == 0 {
		t.Fatal("no letter in port 2")
	}
	for _, lid := range port2 {
		l := snapLetter(t, s, uint64(lid))
		if l.From != 2 || l.Loc != portLoc(2) {
			t.Errorf("letter %d = %+v, want from 2 at port 2", lid, l)
		}
		if lid == port2[0] && l.Label != "heartbeat_resp" {
			t.Errorf("letter %d label = %s, want heartbeat_resp", lid, l.Label)
		}
	}
}

func TestSnapshotRoutes(t *testing.T) { // SN-5
	t.Parallel()
	e := startedEngine(t)
	if err := e.tr.Block([][2]uint64{{1, 2}}, 10000); err != nil {
		t.Fatal(err)
	}
	if got, want := e.Snapshot().Routes, []RouteView{{"1-2", ptr(SimTime(10000))}, {"2-3", nil}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Routes = %+v, want %+v", got, want)
	}
	e.RunUntil(9999)
	if got := e.Snapshot().Routes[0].BlockedUntil; got == nil || *got != 10000 {
		t.Errorf("BlockedUntil at 9999 = %v, want 10000", got)
	}
	e.RunUntil(10000)
	if got := e.Snapshot().Routes[0].BlockedUntil; got != nil {
		t.Errorf("BlockedUntil at 10000 = %d, want nil", *got)
	}
}

// engineState は Snapshot の前後で変わってはいけない値。
type engineState struct {
	hash    [32]byte
	input   []InputEntry
	now     SimTime
	pending int
	tr      transportState
}

func stateOf(e *Engine) engineState {
	return engineState{e.TraceHash(), e.InputLog(), e.Now(), len(e.pending), snapTransport(e)}
}

func TestSnapshotDoesNotChangeState(t *testing.T) { // SN-6
	t.Parallel()
	e, _ := loadedEngine(t)
	if err := e.tr.Block([][2]uint64{{1, 2}}, 10000); err != nil {
		t.Fatal(err)
	}
	before := stateOf(e)
	a := e.Snapshot()
	b := e.Snapshot()
	if after := stateOf(e); !reflect.DeepEqual(before, after) {
		t.Errorf("Snapshot changed the state:\n %+v\n %+v", before, after)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("two snapshots differ:\n %+v\n %+v", a, b)
	}
	if a.Letters[0].Raft == nil || a.Routes[0].BlockedUntil == nil {
		t.Fatalf("snapshot = %+v", a)
	}
	a.Ferries[0].Cargo[0] = 999
	a.Letters[0].Loc.IslandID = 9
	a.Letters[0].Raft.Term = 99
	a.Nodes[0].Term = 99
	*a.Routes[0].BlockedUntil = 1
	if c := e.Snapshot(); !reflect.DeepEqual(b, c) {
		t.Errorf("mutating a snapshot changed the next one:\n %+v\n %+v", b, c)
	}
	if !reflect.DeepEqual(stateOf(e), before) {
		t.Error("mutating a snapshot changed the engine")
	}

	t.Run("entries", func(t *testing.T) {
		t.Parallel()
		e, _ := committedEngine(t)
		a := e.Snapshot()
		if len(a.Letters) == 0 || len(a.Letters[0].Raft.Entries) == 0 {
			t.Fatalf("no entries in %+v", a.Letters)
		}
		b := e.Snapshot()
		a.Letters[0].Raft.Entries[0].Summary = "changed"
		a.Letters[0].Raft.Entries[0].Index = 99
		if c := e.Snapshot(); !reflect.DeepEqual(b, c) {
			t.Error("mutating Raft.Entries changed the next snapshot")
		}
	})
}

func TestSnapshotEnded(t *testing.T) { // SN-7
	t.Parallel()
	run := func(t *testing.T, duration SimTime) Snapshot {
		cfg := testConfig()
		cfg.DurationMs = duration
		e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
		mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
		e.RunUntil(100000)
		evs := e.DrainEvents()
		last := evs[len(evs)-1]
		s := e.Snapshot()
		if s.Phase != PhaseEnded || s.T != duration || s.EndsAt != duration {
			t.Errorf("Phase, T, EndsAt = %s, %d, %d; want ended, %d", s.Phase, s.T, s.EndsAt, duration)
		}
		if last.Data != (PhaseChanged{Phase: PhaseEnded}) || s.LastSeq != last.Seq {
			t.Errorf("LastSeq = %d, last event = %+v", s.LastSeq, last)
		}
		return s
	}
	t.Run("arrive at end", func(t *testing.T) {
		t.Parallel()
		if f := run(t, 3750).Ferries[0]; f.State != "docked" || f.IslandID != 2 {
			t.Errorf("ferry = %+v, want docked at 2", f)
		}
	})
	t.Run("sailing at end", func(t *testing.T) {
		t.Parallel()
		if f := run(t, 3000).Ferries[0]; f.State != "sailing" || f.ArriveAt != 3750 {
			t.Errorf("ferry = %+v, want sailing", f)
		}
	})
}

// eventModel は、クライアントと同じ規則でイベントを当てはめた手紙とノードの一覧（SN-8）。
// 手紙は ID の昇順のスライスで持つ。
type eventModel struct {
	letters []LetterView
	nodes   []NodeState
	lastSeq uint64
}

func newEventModel(n int) *eventModel {
	m := &eventModel{letters: []LetterView{}}
	for i := range n {
		m.nodes = append(m.nodes, NodeState{ID: uint64(i + 1), Role: "follower"})
	}
	return m
}

func (m *eventModel) find(t *testing.T, id uint64) int {
	t.Helper()
	i, ok := slices.BinarySearchFunc(m.letters, id, func(l LetterView, id uint64) int {
		switch {
		case l.ID < id:
			return -1
		case l.ID > id:
			return 1
		}
		return 0
	})
	if !ok {
		t.Fatalf("event for unknown letter %d", id)
	}
	return i
}

// apply はイベントを当てはめ、letter.stale の件数を返す。
func (m *eventModel) apply(t *testing.T, evs []DomainEvent) int {
	t.Helper()
	stale := 0
	for _, ev := range evs {
		if ev.Seq != m.lastSeq+1 {
			t.Fatalf("seq %d after %d", ev.Seq, m.lastSeq)
		}
		m.lastSeq = ev.Seq
		switch d := ev.Data.(type) {
		case LetterCreated:
			// 手紙の ID は島ごとの番号（islandId × 10^9 + n）なので、作った順と ID の順は一致しない。ID の順の位置に入れる。
			i, found := slices.BinarySearchFunc(m.letters, d.Letter.ID, func(l LetterView, id uint64) int { return cmp.Compare(l.ID, id) })
			if found {
				t.Fatalf("letter %d created twice", d.Letter.ID)
			}
			if n := len(m.letters); n > 0 && m.letters[n-1].From == d.Letter.From && m.letters[n-1].ID > d.Letter.ID {
				t.Fatalf("letter %d created after %d of the same island", d.Letter.ID, m.letters[n-1].ID)
			}
			m.letters = slices.Insert(m.letters, i, d.Letter)
		case LetterMoved:
			m.letters[m.find(t, d.LetterID)].Loc = d.Loc
		case LetterDelivered:
			m.letters = slices.Delete(m.letters, m.find(t, d.LetterID), m.find(t, d.LetterID)+1)
		case LetterDropped:
			m.letters = slices.Delete(m.letters, m.find(t, d.LetterID), m.find(t, d.LetterID)+1)
		case LetterStale:
			l := &m.letters[m.find(t, d.LetterID)]
			if l.Raft == nil || l.Raft.Stale {
				t.Fatalf("t=%d: letter.stale for %+v", ev.T, l)
			}
			l.Raft.Stale = true
			stale++
		case NodeState:
			m.nodes[d.ID-1] = d
		}
	}
	return stale
}

// snSeed は SN-8 のシード。letter.stale が 1 件以上出ることを確かめてある。
const snSeed = 1

// snapshotRun は SN-8 のランダムな操作をラウンドの終わりまで行い、各操作の後にイベントとの整合を確かめる。
func snapshotRun(t *testing.T, seed uint64) ([]DomainEvent, [32]byte, int) {
	t.Helper()
	e, err := New(testConfig(), seed, Options{CheckInvariants: true})
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(seed, 2))
	m := newEventModel(e.cfg.Islands)
	var all []DomainEvent
	stale := 0
	check := func() {
		t.Helper()
		evs := e.DrainEvents()
		all = append(all, evs...)
		stale += m.apply(t, evs)
		s := e.Snapshot()
		if !reflect.DeepEqual(s.Letters, m.letters) {
			t.Fatalf("t=%d: Letters differ:\n snapshot %+v\n events   %+v", e.Now(), s.Letters, m.letters)
		}
		if !reflect.DeepEqual(s.Nodes, m.nodes) {
			t.Fatalf("t=%d: Nodes differ:\n snapshot %+v\n events   %+v", e.Now(), s.Nodes, m.nodes)
		}
		if s.LastSeq != m.lastSeq {
			t.Fatalf("t=%d: LastSeq = %d, want %d", e.Now(), s.LastSeq, m.lastSeq)
		}
		for i, n := range e.nodes {
			// cmd.game.start の前は、開会式の Term をまだ反映していない（後処理を通っていないため）。
			if e.Phase() != PhaseIntro && e.staleTerms()[i] != n.Status().Term {
				t.Fatalf("t=%d: staleTerm[%d] = %d, Term = %d", e.Now(), i, e.staleTerms()[i], n.Status().Term)
			}
		}
	}
	check()
	mustApply(t, e, Command{Type: CmdGameStart})
	check()
	for e.Phase() != PhaseEnded {
		randomStep(t, e, r, randomOps{propose: true, notable: true})
		check()
	}
	return all, e.TraceHash(), stale
}

func TestSnapshotMatchesEvents(t *testing.T) { // SN-8
	t.Parallel()
	a, ha, stale := snapshotRun(t, snSeed)
	if stale == 0 {
		t.Error("no letter.stale in the run")
	}
	t.Logf("%d events, %d letter.stale", len(a), stale)
	b, hb, _ := snapshotRun(t, snSeed)
	if !reflect.DeepEqual(a, b) || ha != hb {
		t.Errorf("same seed gave different runs: %d vs %d events", len(a), len(b))
	}
	if n := len(ofType[LetterDelivered](a)); n == 0 {
		t.Error("no letter was delivered")
	}
}
