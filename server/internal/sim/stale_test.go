package sim

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// deliverRaw はテストで作った m を宛先のノードに渡し、後処理まで行う。Term を上げる状況を短く作るためのもの。
// 港・船の手紙のメッセージは書き換えない。
func deliverRaw(t *testing.T, e *Engine, m *pb.Message) {
	t.Helper()
	if err := e.nodes[m.GetTo()-1].Deliver(m); err != nil {
		t.Fatalf("Deliver(%v): %v", m, err)
	}
	e.afterStep()
}

// heartbeat はテスト用の MsgHeartbeat を作る。
func heartbeat(from, to, term uint64) *pb.Message {
	return &pb.Message{Type: pb.MsgHeartbeat.Enum(), From: ptr(from), To: ptr(to), Term: ptr(term)}
}

// staleIDs は evs のうち letter.stale の letterId を順に返す。
func staleIDs(evs []DomainEvent) []uint64 {
	out := []uint64{}
	for _, d := range ofType[LetterStale](evs) {
		out = append(out, d.LetterID)
	}
	return out
}

// snapLetter は Snapshot の Letters から id の手紙を返す。
func snapLetter(t *testing.T, s Snapshot, id uint64) LetterView {
	t.Helper()
	i := slices.IndexFunc(s.Letters, func(l LetterView) bool { return l.ID == id })
	if i < 0 {
		t.Fatalf("letter %d not in snapshot: %+v", id, s.Letters)
	}
	return s.Letters[i]
}

func TestLetterStalePortAndFerry(t *testing.T) { // ST-1
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(1000)
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{2}})
	e.DrainEvents()

	deliverRaw(t, e, heartbeat(2, 1, 3))
	evs := e.DrainEvents()
	for _, ev := range evs {
		if ev.T != 1000 {
			t.Errorf("event %d at %d, want 1000", ev.Seq, ev.T)
		}
	}
	if len(evs) != 5 {
		t.Fatalf("events = %+v, want 5", datas(evs))
	}
	created, ok := evs[3].Data.(LetterCreated)
	if !ok {
		t.Fatalf("event 4 = %+v, want letter.created", evs[3].Data)
	}
	l := created.Letter
	if l.ID != 3 || l.From != 1 || l.To != 2 || l.Label != "heartbeat_resp" || l.Raft == nil || l.Raft.Term != 3 || l.Raft.Stale {
		t.Errorf("letter.created = %+v (raft %+v)", l, l.Raft)
	}
	checkEvents(t, []DomainEvent{evs[0], evs[1], evs[2], evs[4]}, []EventData{
		SteppedDown{ID: 1, Term: 3, Reason: "higher_term"},
		LetterStale{LetterID: 1},
		LetterStale{LetterID: 2},
		NodeState{ID: 1, Role: "follower", Term: 3, Vote: 0, Lead: 2, Commit: 4, Applied: 4, LastIndex: 4},
	})

	s := e.Snapshot()
	for _, c := range []struct {
		id    uint64
		stale bool
	}{{1, true}, {2, true}, {3, false}} {
		if got := snapLetter(t, s, c.id).Raft.Stale; got != c.stale {
			t.Errorf("letter %d stale = %v, want %v", c.id, got, c.stale)
		}
	}
}

// staleEngine は ST-1 の状況（港 1 に ID 1、船に ID 2、Term 3 で ID 3）を作り、イベントを取り出しておく。
func staleEngine(t *testing.T) *Engine {
	t.Helper()
	e := startedEngine(t)
	e.RunUntil(1000)
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{2}})
	deliverRaw(t, e, heartbeat(2, 1, 3))
	e.DrainEvents()
	return e
}

func TestLetterStaleOnce(t *testing.T) { // ST-2
	t.Parallel()
	e := staleEngine(t)
	deliverRaw(t, e, heartbeat(3, 1, 4))
	evs := e.DrainEvents()
	if got, want := staleIDs(evs), []uint64{3}; !reflect.DeepEqual(got, want) {
		t.Errorf("letter.stale = %v, want %v", got, want)
	}
	created := ofType[LetterCreated](evs)
	if len(created) != 1 {
		t.Fatalf("letter.created = %+v, want 1", created)
	}
	if l := created[0].Letter; l.ID != 4 || l.To != 3 || l.Raft.Term != 4 || l.Raft.Stale {
		t.Errorf("letter.created = %+v (raft %+v), want id 4 to 3 term 4 not stale", l, l.Raft)
	}
	if e.staleTerms()[0] != 4 {
		t.Errorf("staleTerm[0] = %d, want 4", e.staleTerms()[0])
	}
}

func TestLetterStaleTermJump(t *testing.T) {
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(1000)
	e.DrainEvents()
	deliverRaw(t, e, heartbeat(2, 1, 7)) // Term 2 → 7
	if got, want := staleIDs(e.DrainEvents()), []uint64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("letter.stale = %v, want %v", got, want)
	}
	deliverRaw(t, e, heartbeat(3, 1, 9))
	if got, want := staleIDs(e.DrainEvents()), []uint64{3}; !reflect.DeepEqual(got, want) {
		t.Errorf("letter.stale after 9 = %v, want %v (only the term 7 reply)", got, want)
	}
}

func TestLetterStaleSenderOnly(t *testing.T) { // ST-3
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(1000)
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{1}})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	e.RunUntil(4750)
	port2 := e.tr.Port(2)
	if len(port2) == 0 {
		t.Fatal("no letter in port 2")
	}
	var want []uint64
	for _, lid := range e.tr.Port(1) {
		if l, _ := e.tr.Letter(lid); l.Kind == ferry.LetterRaft {
			want = append(want, uint64(lid))
		}
	}
	if len(want) == 0 {
		t.Fatal("no raft letter in port 1")
	}
	e.DrainEvents()

	deliverRaw(t, e, heartbeat(3, 1, 3))
	if got := staleIDs(e.DrainEvents()); !reflect.DeepEqual(got, want) {
		t.Errorf("letter.stale = %v, want %v (port 1)", got, want)
	}
	s := e.Snapshot()
	for _, lid := range port2 {
		if l := snapLetter(t, s, uint64(lid)); l.From != 2 || l.Raft.Stale {
			t.Errorf("letter %d in port 2 = %+v (raft %+v), want from 2 not stale", lid, l, l.Raft)
		}
	}
}

func TestLetterStaleRequest(t *testing.T) { // ST-4
	t.Parallel()
	e := startedEngine(t)
	id, _, err := e.postRequestForTest(0, 1, 2, ferry.RequestNote{ReqID: "r-0001"})
	if err != nil || id != 1 {
		t.Fatalf("PostRequest = %d, %v; want 1", id, err)
	}
	e.RunUntil(1000)
	e.DrainEvents()
	deliverRaw(t, e, heartbeat(2, 1, 3))
	if got, want := staleIDs(e.DrainEvents()), []uint64{2, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("letter.stale = %v, want %v", got, want)
	}
}

func TestLetterStaleJSON(t *testing.T) { // ST-5
	t.Parallel()
	b, err := json.Marshal(DomainEvent{Seq: 9, T: 1000, Type: EvLetterStale, Data: LetterStale{LetterID: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"seq":9,"t":1000,"type":"letter.stale","letterId":1}`; got != want {
		t.Errorf("JSON = %s, want %s", got, want)
	}
	raw, err := json.Marshal(LetterStale{})
	if err != nil {
		t.Fatal(err)
	}
	var keys struct {
		Seq  *json.RawMessage `json:"seq"`
		T    *json.RawMessage `json:"t"`
		Type *json.RawMessage `json:"type"`
	}
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	if keys.Seq != nil || keys.T != nil || keys.Type != nil {
		t.Errorf("LetterStale JSON %s has a reserved key", raw)
	}
	if got := (LetterStale{}).EventType(); got != "letter.stale" {
		t.Errorf("EventType = %s", got)
	}
}

func TestLetterStaleNoTermChange(t *testing.T) { // ST-6
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(45000)
	evs := e.DrainEvents()
	if got := staleIDs(evs); len(got) != 0 {
		t.Errorf("letter.stale = %v, want none", got)
	}
	if got := ofType[SteppedDown](evs); len(got) != 1 {
		t.Errorf("stepped_down = %+v, want 1 (check_quorum)", got)
	}
	if want := []uint64{2, 2, 2}; !reflect.DeepEqual(e.staleTerms(), want) {
		t.Errorf("staleTerm = %v, want %v", e.staleTerms(), want)
	}
}
