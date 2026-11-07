package sim

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

func autoloadCmd(to uint64) Command {
	return Command{Type: CmdFerryAutoload, FerryID: "f1", To: to}
}

func cargoOf(t *testing.T, e *Engine, id ferry.FerryID) []ferry.LetterID {
	t.Helper()
	f, ok := e.tr.Ferry(id)
	if !ok {
		t.Fatalf("ferry %q not found", id)
	}
	return f.Cargo
}

func TestAutoloadPick(t *testing.T) { // AL-1
	t.Parallel()
	raftLetter := func(id, to uint64, m *pb.Message) ferry.Letter {
		return ferry.Letter{ID: ferry.LetterID(id), Kind: ferry.LetterRaft, From: 1, To: to, Raft: m}
	}
	msg := func(typ pb.MessageType) *pb.Message { return &pb.Message{Type: typ.Enum()} }
	withEntry := &pb.Message{Type: pb.MsgApp.Enum(), Entries: []*pb.Entry{{Index: ptr(uint64(5)), Term: ptr(uint64(2))}}}
	reject := &pb.Message{Type: pb.MsgAppResp.Enum(), Reject: ptr(true)}
	port := []ferry.Letter{
		raftLetter(1, 2, msg(pb.MsgHeartbeat)),
		raftLetter(2, 2, msg(pb.MsgHeartbeatResp)),
		raftLetter(3, 2, withEntry),
		raftLetter(4, 2, msg(pb.MsgApp)),
		raftLetter(5, 2, msg(pb.MsgAppResp)),
		raftLetter(6, 2, reject),
		raftLetter(7, 2, msg(pb.MsgPreVote)),
		raftLetter(8, 2, msg(pb.MsgPreVoteResp)),
		raftLetter(9, 2, msg(pb.MsgVote)),
		raftLetter(10, 2, msg(pb.MsgVoteResp)),
		{ID: 11, Kind: ferry.LetterRequest, From: 1, To: 2, Req: &ferry.RequestNote{ReqID: "r-0001"}},
		raftLetter(12, 2, msg(pb.MsgTimeoutNow)),
		raftLetter(13, 3, withEntry),
	}
	wantRanks := []int{5, 5, 3, 3, 4, 4, 2, 2, 2, 2, 1, 6, 3}
	for i, l := range port {
		if got := autoloadRank(l); got != wantRanks[i] {
			t.Errorf("autoloadRank(letter %d, %s) = %d, want %d", l.ID, letterLabel(l), got, wantRanks[i])
		}
	}
	tests := []struct {
		to   uint64
		free int
		want []ferry.LetterID
	}{
		{2, 20, []ferry.LetterID{11, 7, 8, 9, 10, 3, 4, 5, 6, 1, 2, 12}},
		{2, 3, []ferry.LetterID{11, 7, 8}},
		{2, 0, []ferry.LetterID{}},
		{3, 4, []ferry.LetterID{13}},
		{1, 4, []ferry.LetterID{}},
	}
	shuffled := slices.Clone(port)
	rand.New(rand.NewPCG(1, 2)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	reversed := slices.Clone(port)
	slices.Reverse(reversed)
	for _, tt := range tests {
		for _, p := range [][]ferry.Letter{port, shuffled, reversed} {
			got := autoloadPick(p, tt.to, tt.free)
			if got == nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("autoloadPick(to %d, free %d) = %#v, want %v", tt.to, tt.free, got, tt.want)
			}
		}
	}
}

// autoloadEngine は AL-2 の 1. を行う。MsgApp の ID 1・3 が →2、ID 2・4 が →3、Heartbeat の ID 5 が →2、
// ID 6 が →3、依頼書の ID 7 が →2。
func autoloadEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	mustPropose(t, e, 1, reserve("s-0001"))
	mustPropose(t, e, 1, reserve("s-0002"))
	e.RunUntil(1000)
	id, _, err := e.tr.PostRequest(1000, 1, 2, ferry.RequestNote{ReqID: "r-0001"})
	if err != nil || id != 7 {
		t.Fatalf("PostRequest = %d, %v; want 7", id, err)
	}
	if got, want := e.tr.Port(1), []ferry.LetterID{1, 2, 3, 4, 5, 6, 7}; !reflect.DeepEqual(got, want) {
		t.Fatalf("port 1 = %v, want %v", got, want)
	}
	e.DrainEvents()
	return e
}

func TestAutoload(t *testing.T) { // AL-2・AL-8
	t.Parallel()
	e := autoloadEngine(t, testConfig())
	if ack := e.Apply(autoloadCmd(2)); ack != (Ack{OK: true, T: 1000}) {
		t.Fatalf("autoload = %+v", ack)
	}
	var want []EventData
	for _, id := range []uint64{7, 1, 3, 5} {
		want = append(want, LetterMoved{LetterID: id, Loc: ferryLoc("f1")})
	}
	checkEvents(t, e.DrainEvents(), want)
	if got, want := cargoOf(t, e, "f1"), []ferry.LetterID{7, 1, 3, 5}; !reflect.DeepEqual(got, want) {
		t.Errorf("cargo = %v, want %v", got, want)
	}
	if got, want := e.tr.Port(1), []ferry.LetterID{2, 4, 6}; !reflect.DeepEqual(got, want) {
		t.Errorf("port 1 = %v, want %v", got, want)
	}

	// AL-8
	in := e.InputLog()
	if got, want := in[len(in)-1], (InputEntry{T: 1000, Cmd: Command{Type: "cmd.ferry.autoload", FerryID: "f1", To: 2}}); !reflect.DeepEqual(got, want) {
		t.Errorf("last input = %+v, want %+v", got, want)
	}

	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	e.RunUntil(4750)
	var delivered []uint64
	for _, d := range ofType[LetterDelivered](e.DrainEvents()) {
		delivered = append(delivered, d.LetterID)
	}
	if want := []uint64{7, 1, 3, 5}; !reflect.DeepEqual(delivered, want) {
		t.Errorf("delivered = %v, want %v", delivered, want)
	}
}

func TestAutoloadFreeSpace(t *testing.T) { // AL-3
	t.Parallel()
	cfg := testConfig()
	cfg.Ferries[0].Capacity = 2
	e := autoloadEngine(t, cfg)
	mustApply(t, e, autoloadCmd(2))
	if got, want := cargoOf(t, e, "f1"), []ferry.LetterID{7, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("cargo = %v, want %v", got, want)
	}
	if ack := e.Apply(autoloadCmd(2)); ack.OK || ack.Code != CodeCapacityExceeded {
		t.Errorf("second autoload = %+v, want capacity_exceeded", ack)
	}
}

func TestAutoloadKeepsCargo(t *testing.T) { // AL-4
	t.Parallel()
	e := autoloadEngine(t, testConfig())
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{3}})
	mustApply(t, e, autoloadCmd(2))
	if got, want := cargoOf(t, e, "f1"), []ferry.LetterID{3, 7, 1, 5}; !reflect.DeepEqual(got, want) {
		t.Errorf("cargo = %v, want %v", got, want)
	}
}

func TestAutoloadErrors(t *testing.T) { // AL-5
	t.Parallel()
	started := func(t *testing.T) *Engine { return startedEngine(t) }
	afterHeartbeat := func(t *testing.T, capacity int, load uint64) *Engine {
		cfg := testConfig()
		cfg.Ferries[0].Capacity = capacity
		e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
		e.RunUntil(1000)
		mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{load}})
		e.DrainEvents()
		return e
	}
	tests := []struct {
		name  string
		setup func(t *testing.T) *Engine
		cmd   Command
		code  string
	}{
		{"intro", func(t *testing.T) *Engine { return newEngine(t, testConfig(), Options{CheckInvariants: true}) }, autoloadCmd(2), CodePhaseInvalid},
		{"unknown ferry", started, Command{Type: CmdFerryAutoload, FerryID: "zz", To: 2}, CodeBadRequest},
		{"empty ferry id", started, Command{Type: CmdFerryAutoload, To: 2}, CodeBadRequest},
		{"sailing", func(t *testing.T) *Engine {
			e := startedEngine(t)
			mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
			e.DrainEvents()
			return e
		}, autoloadCmd(1), CodeFerryNotDocked},
		{"no route 1-3", started, autoloadCmd(3), CodeNoRoute},
		{"to here", started, autoloadCmd(1), CodeNoRoute},
		{"to 0", started, autoloadCmd(0), CodeNoRoute},
		{"to 4", started, autoloadCmd(4), CodeNoRoute},
		{"empty port", started, autoloadCmd(2), CodeLetterNotHere},
		{"only letters to 3", func(t *testing.T) *Engine { return afterHeartbeat(t, 4, 1) }, autoloadCmd(2), CodeLetterNotHere},
		{"full", func(t *testing.T) *Engine { return afterHeartbeat(t, 1, 2) }, autoloadCmd(2), CodeCapacityExceeded},
		{"ended", func(t *testing.T) *Engine {
			e := startedEngine(t)
			e.RunUntil(e.cfg.DurationMs)
			e.DrainEvents()
			return e
		}, autoloadCmd(2), CodePhaseInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := tt.setup(t)
			before := snapTransport(e)
			nIn := len(e.InputLog())
			hash := e.TraceHash()
			ack := e.Apply(tt.cmd)
			if ack.OK || ack.Code != tt.code || ack.T != e.Now() {
				t.Errorf("Apply(%+v) = %+v, want %s at %d", tt.cmd, ack, tt.code, e.Now())
			}
			if evs := e.DrainEvents(); len(evs) != 0 {
				t.Errorf("events = %+v, want none", datas(evs))
			}
			if n := len(e.InputLog()); n != nIn {
				t.Errorf("len(InputLog) = %d, want %d", n, nIn)
			}
			if e.TraceHash() != hash {
				t.Error("TraceHash changed")
			}
			if after := snapTransport(e); !reflect.DeepEqual(before, after) {
				t.Errorf("transport changed: %+v → %+v", before, after)
			}
		})
	}
}

func TestAutoloadBlockedRoute(t *testing.T) { // AL-6
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(1000)
	if err := e.tr.Block([][2]uint64{{1, 2}}, 60000); err != nil {
		t.Fatal(err)
	}
	mustApply(t, e, autoloadCmd(2))
	if got, want := cargoOf(t, e, "f1"), []ferry.LetterID{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("cargo = %v, want %v", got, want)
	}
	if ack := e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: 2}); ack.OK || ack.Code != CodeRouteBlocked {
		t.Errorf("depart = %+v, want route_blocked", ack)
	}
}

func TestAutoloadWhileDocking(t *testing.T) { // AL-7
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(1000)
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{1}})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	e.RunUntil(4750)
	f, _ := e.tr.Ferry("f1")
	if f.State != ferry.Docked || f.At != 2 || e.Now() >= SimTime(f.ReadyAt) {
		t.Fatalf("ferry = %+v at %d, want docked at 2 before readyAt", f, e.Now())
	}
	port2 := e.tr.Port(2)
	if len(port2) == 0 {
		t.Fatal("no reply in port 2")
	}
	mustApply(t, e, autoloadCmd(1))
	if got := cargoOf(t, e, "f1"); !reflect.DeepEqual(got, port2) {
		t.Errorf("cargo = %v, want %v", got, port2)
	}
	if ack := e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: 1}); ack.OK || ack.Code != CodeFerryNotReady {
		t.Errorf("depart = %+v, want ferry_not_ready", ack)
	}
}

// neighborOf は testConfig の島 at の隣の島を返す（2 は 1 か 3 をランダムに）。
func neighborOf(r *rand.Rand, at uint64) uint64 {
	if at == 2 {
		return 1 + 2*uint64(r.IntN(2))
	}
	return 2
}

// randomOps はランダムな操作の選び方。
type randomOps struct {
	propose bool // mustPropose 相当の提案を混ぜる（入力ログに入らない）
	notable bool // NextNotableAt の時刻まで進める操作を混ぜる
}

// randomStep は testConfig の Engine に、ランダムな操作を 1 つ行う。エラーの Ack は許す。
func randomStep(t *testing.T, e *Engine, r *rand.Rand, ops randomOps) {
	t.Helper()
	f, _ := e.tr.Ferry("f1")
	switch r.IntN(7) {
	case 0: // load
		if f.State == ferry.Docked {
			if port := e.tr.Port(f.At); len(port) > 0 {
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
			e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: neighborOf(r, f.At)})
		}
	case 3: // autoload
		to := uint64(r.IntN(5))
		if f.State == ferry.Docked && r.IntN(4) > 0 {
			to = neighborOf(r, f.At)
		}
		e.Apply(autoloadCmd(to))
	case 4:
		e.RunUntil(e.Now() + SimTime(50+r.IntN(1951)))
	case 5: // propose
		if !ops.propose {
			e.RunUntil(e.Now() + SimTime(50+r.IntN(1951)))
			return
		}
		island := 1 + uint64(r.IntN(3))
		reqIDs := []string{"s-1", "s-2", "s-3", "f-1"}
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
	case 6: // 次の出来事まで
		if !ops.notable {
			e.Apply(autoloadCmd(neighborOf(r, max(f.At, 1))))
			return
		}
		if at, ok := e.NextNotableAt(); ok {
			e.RunUntil(at)
		}
	}
}

// dtSeed は DT-1 のシード。入力ログに cmd.ferry.autoload が入ることを確かめてある。
const dtSeed = 7

func TestReplayWithAutoload(t *testing.T) { // DT-1
	t.Parallel()
	e := newEngine(t, testConfig(), Options{CheckInvariants: true})
	r := rand.New(rand.NewPCG(dtSeed, 3))
	mustApply(t, e, Command{Type: CmdGameStart})
	for e.Phase() != PhaseEnded {
		randomStep(t, e, r, randomOps{})
	}
	input := e.InputLog()
	hash := e.TraceHash()
	snap := e.Snapshot()
	n := 0
	for _, in := range input {
		if in.Cmd.Type == CmdFerryAutoload {
			n++
		}
	}
	if n == 0 {
		t.Fatal("no cmd.ferry.autoload in the input log")
	}
	t.Logf("%d inputs, %d autoload", len(input), n)

	step50 := func(e *Engine, target SimTime) {
		for e.Now()+50 < target && e.Phase() == PhaseSailing {
			e.RunUntil(e.Now() + 50)
		}
		e.RunUntil(target)
	}
	ways := []struct {
		name    string
		advance func(e *Engine, target SimTime)
	}{
		{"a", func(e *Engine, target SimTime) { e.RunUntil(target) }},
		{"b", step50},
		{"c", func(e *Engine, target SimTime) {
			for {
				at, ok := e.NextNotableAt()
				if !ok || at > target || at <= e.Now() {
					break
				}
				e.RunUntil(at)
			}
			e.RunUntil(target)
		}},
	}
	for _, w := range ways {
		t.Run(w.name, func(t *testing.T) {
			t.Parallel()
			re := newEngine(t, testConfig(), Options{CheckInvariants: true})
			for _, in := range input {
				w.advance(re, in.T)
				if ack := re.Apply(in.Cmd); !ack.OK || ack.T != in.T {
					t.Fatalf("replay Apply(%+v) at %d = %+v", in.Cmd, in.T, ack)
				}
			}
			w.advance(re, re.cfg.DurationMs)
			if re.TraceHash() != hash {
				t.Error("TraceHash differs from the first run")
			}
			if got := re.Snapshot(); !reflect.DeepEqual(got, snap) {
				t.Errorf("Snapshot differs:\n got  %+v\n want %+v", got, snap)
			}
		})
	}
}
