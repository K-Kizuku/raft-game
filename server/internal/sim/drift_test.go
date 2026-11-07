package sim

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// driftTestConfig は漂流瓶のテストの設定（3 島の三角形・船 2 隻）。
// RaftTick を 300 にして、漂流瓶の時刻（1000 の倍数）に RaftTick が重ならないようにする。
func driftTestConfig(defs ...DriftDef) Config {
	cfg := detConfig3()
	cfg.DurationMs = 180000
	cfg.Raft.TickMs = 300
	cfg.Raft.HeartbeatTick = 3
	cfg.Raft.ElectionTick = 60
	cfg.Drifts = defs
	return cfg
}

func driftAt(at int64, letters int, minAgeMs int64) DriftDef {
	return DriftDef{WindowStart: at, WindowEnd: at, Letters: letters, MinAgeMs: minAgeMs}
}

// driftDrive は until まで、停泊していて出航できる船に、港の手紙を隣の島へおまかせで積んで出すボット。
// 失敗したコマンドは何も変えないので無視する。時間はボットの乱数で決めた間隔で進める。取り出したイベントを返す。
func driftDrive(e *Engine, r *rand.Rand, until SimTime) []DomainEvent {
	var all []DomainEvent
	for e.Phase() == PhaseSailing && e.Now() < until {
		now := e.Now()
		for _, f := range e.tr.Ferries() {
			if f.State != ferry.Docked || SimTime(f.ReadyAt) > now {
				continue
			}
			nb := invNeighbors(e, f.At)
			to := nb[r.IntN(len(nb))]
			for _, id := range e.tr.Port(f.At) {
				if l, ok := e.tr.Letter(id); ok && slices.Contains(nb, l.To) && r.IntN(2) == 0 {
					to = l.To
					break
				}
			}
			e.Apply(Command{Type: CmdFerryAutoload, FerryID: string(f.ID), To: to})
			e.Apply(Command{Type: CmdFerryDepart, FerryID: string(f.ID), To: to})
		}
		e.RunUntil(min(until, now+SimTime(100+r.IntN(900))))
		all = append(all, e.DrainEvents()...)
	}
	return all
}

// driftCreatedAt は時刻 at の letter.created を返す。
func driftCreatedAt(evs []DomainEvent, at SimTime) []LetterView {
	var out []LetterView
	for _, c := range ofTypeAt[LetterCreated](evs) {
		if c.T == at {
			out = append(out, c.D.Letter)
		}
	}
	return out
}

// sameRaftView は v が記録 rec の手紙と同じ送り主・宛先・Raft の中身か。
func sameRaftView(v LetterView, rec driftRecord) bool {
	m := rec.msg
	return v.Kind == "raft" && v.From == rec.from && v.To == m.GetTo() && v.Raft != nil &&
		v.Raft.Type == m.GetType().String() && v.Raft.Term == m.GetTerm() && v.Raft.Index == m.GetIndex() &&
		v.Raft.LogTerm == m.GetLogTerm() && v.Raft.Commit == m.GetCommit() && len(v.Raft.Entries) == len(m.GetEntries())
}

// SD-1・SD-2 漂流瓶（at 130000・letters: 3・minAgeMs: 30000）→ 130000 に、100000 以前に配達した Raft の手紙の複製の
// letter.created が最大 3 件。送り主の港に元の宛先のまま置かれ、中身は元のメッセージと同じ（proto.Equal）。元は書き換えない。
func TestDriftCopiesOldLetters(t *testing.T) {
	t.Parallel()
	cfg := driftTestConfig(driftAt(130000, 3, 30000))
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	r := rand.New(rand.NewPCG(1, 2))
	driftDrive(e, r, 129999)
	for _, f := range e.tr.Ferries() {
		if f.State == ferry.Sailing && f.ArriveAt == 130000 {
			t.Fatalf("ferry %s arrives at 130000（テストの前提が崩れた）", f.ID)
		}
	}
	var cands []driftRecord
	for _, rec := range e.drifts.log {
		if rec.deliveredAt <= 100000 {
			cands = append(cands, rec)
		}
	}
	if len(cands) < 3 {
		t.Fatalf("candidates = %d, want >= 3", len(cands))
	}
	before := make([]*pb.Message, len(e.drifts.log))
	for i, rec := range e.drifts.log {
		before[i] = proto.Clone(rec.msg).(*pb.Message)
	}

	e.RunUntil(130000)
	evs := e.DrainEvents()
	got := driftCreatedAt(evs, 130000)
	if len(got) != 3 {
		t.Fatalf("letter.created at 130000 = %d, want 3: %+v", len(got), got)
	}
	found := 0
	for _, v := range got {
		if v.Loc != portLoc(v.From) || v.CreatedAt != 130000 {
			t.Errorf("letter %d: loc %+v createdAt %d, want the sender's port", v.ID, v.Loc, v.CreatedAt)
		}
		if !slices.ContainsFunc(cands, func(rec driftRecord) bool { return sameRaftView(v, rec) }) {
			t.Errorf("letter %d %+v %+v is not a copy of a letter delivered by 100000", v.ID, v, v.Raft)
		}
		if want := v.Raft.Term < e.nodes[v.From-1].Status().Term; v.Raft.Stale != want {
			t.Errorf("letter %d: stale %v, want %v", v.ID, v.Raft.Stale, want)
		}
		l, ok := e.tr.Letter(ferry.LetterID(v.ID))
		if !ok {
			continue // 港の規則で破棄された
		}
		found++
		if l.From != v.From || l.To != l.Raft.GetTo() || l.Kind != ferry.LetterRaft {
			t.Errorf("letter %d: %+v", v.ID, l)
		}
		match := false
		for _, rec := range cands {
			if rec.msg == l.Raft {
				t.Errorf("letter %d shares the message with the original", v.ID)
			}
			if rec.from == l.From && proto.Equal(rec.msg, l.Raft) {
				match = true
			}
		}
		if !match {
			t.Errorf("letter %d: message %v is not equal to any original", v.ID, l.Raft)
		}
	}
	if found == 0 {
		t.Error("all copies were dropped（中身を確かめられない）")
	}
	check := func(when string) {
		t.Helper()
		for i, m := range before {
			if !proto.Equal(e.drifts.log[i].msg, m) {
				t.Errorf("%s: original message %d was changed: %v -> %v", when, i, m, e.drifts.log[i].msg)
			}
		}
	}
	check("after drift")
	driftDrive(e, r, cfg.DurationMs)
	check("after round")
}

// SD-3 候補が無い時刻 → イベントも乱数も出さない（定義が無いときとトレースが同じ）。
func TestDriftNoCandidates(t *testing.T) {
	t.Parallel()
	run := func(c Config) (*Engine, [32]byte) {
		e := startedEngineWith(t, c, Options{CheckInvariants: true})
		driftDrive(e, rand.New(rand.NewPCG(3, 4)), c.DurationMs)
		return e, e.TraceHash()
	}
	withDrift, h1 := run(driftTestConfig(driftAt(20000, 3, 30000), driftAt(1000, 3, 0)))
	_, h2 := run(driftTestConfig())
	if h1 != h2 {
		t.Error("drift without candidates changed the trace")
	}
	if !withDrift.drifts.list[0].done || !withDrift.drifts.list[1].done {
		t.Error("drift was not processed")
	}
	if withDrift.drifts.rng.Uint64() != Sub(testSeed, StreamDrift).Uint64() {
		t.Error("drift without candidates drew random numbers")
	}
}

// 候補が letters より少ない → ある分だけ。
func TestDriftFewerCandidates(t *testing.T) {
	t.Parallel()
	cfg := driftTestConfig(driftAt(20000, 100000, 0))
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	driftDrive(e, rand.New(rand.NewPCG(5, 6)), 19999)
	n := len(e.drifts.log)
	if n == 0 {
		t.Fatal("no deliveries before the drift")
	}
	e.RunUntil(20000)
	if got := driftCreatedAt(e.DrainEvents(), 20000); len(got) != n {
		t.Errorf("letter.created at 20000 = %d, want %d（候補の数）", len(got), n)
	}
}

// 漂流瓶の定義が無ければ配達を記録しない。依頼書は記録しない（記録は配達した Raft の手紙の数と同じ）。
func TestDriftRecordsOnlyRaft(t *testing.T) {
	t.Parallel()
	cfg := driftTestConfig()
	cfg.Requests = reqConfig(stallReq(2000, 2), stallReq(4000, 3))
	e := startedEngineWith(t, cfg, Options{})
	driftDrive(e, rand.New(rand.NewPCG(7, 8)), 60000)
	if e.drifts.log != nil || e.drifts.rng != nil {
		t.Errorf("recorded without drift defs: %d", len(e.drifts.log))
	}

	cfg.Drifts = []DriftDef{driftAt(170000, 1, 0)}
	e = startedEngineWith(t, cfg, Options{})
	evs := driftDrive(e, rand.New(rand.NewPCG(7, 8)), 60000)
	var reqIDs []uint64
	for _, c := range ofType[LetterCreated](evs) {
		if c.Letter.Kind == "request" {
			reqIDs = append(reqIDs, c.Letter.ID)
		}
	}
	raft, req := 0, 0
	for _, d := range ofType[LetterDelivered](evs) {
		if slices.Contains(reqIDs, d.LetterID) {
			req++
		} else {
			raft++
		}
	}
	if req == 0 || raft == 0 {
		t.Fatalf("deliveries: raft %d, request %d（テストの前提が崩れた）", raft, req)
	}
	if len(e.drifts.log) != raft {
		t.Errorf("records = %d, want raft deliveries %d", len(e.drifts.log), raft)
	}
}

// sd4Config は SD-4 の設定。島 1（最初の Leader）を嵐で孤立させ、その後に漂流瓶を多く流す。ラッシュで依頼も出す。
func sd4Config() Config {
	cfg := driftTestConfig(
		driftAt(80000, 50, 0),
		DriftDef{WindowStart: 110000, WindowEnd: 125000, Letters: 50, MinAgeMs: 20000},
		driftAt(150000, 50, 60000),
	)
	cfg.Ferries = append(cfg.Ferries, ferry.FerryConfig{ID: "f3", Home: 2, Capacity: 4, SpeedPermille: 1000})
	cfg.Storms = []game.StormDef{{WindowStart: 40000, WindowEnd: 40000, DurationMs: 30000, Routes: [][2]uint64{{1, 2}, {1, 3}}}}
	cfg.Requests = reqConfig(stallReq(5000, 2), fireworkReq(90000, 3))
	cfg.Requests.Random = game.RandomConfig{
		Weights: []int64{1, 1, 1},
		Kinds: []game.RandomKind{
			{Kind: ledger.KindStallReserve, Resource: "stall:main", Weight: 1},
			{Kind: ledger.KindFireworkReserve, Resource: "firework:a", Weight: 1},
		},
	}
	cfg.Rushes = []game.RushDef{rushAt(20000, 3, 5000), {WindowStart: 95000, WindowEnd: 105000, Requests: 4}}
	return cfg
}

// SD-4 嵐で分断した後に漂流瓶を多く流してラウンドを最後まで走らせる → panic せず（テストモードの不変条件）、
// 確定した依頼の結果がどの島でも同じ（V-4）。
func TestDriftKeepsSafety(t *testing.T) {
	t.Parallel()
	for seed := uint64(1); seed <= 8; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			cfg := sd4Config()
			e, err := New(cfg, seed, Options{CheckInvariants: true})
			if err != nil {
				t.Fatal(err)
			}
			mustApply(t, e, Command{Type: CmdGameStart})
			evs := driftDrive(e, rand.New(rand.NewPCG(seed, 9)), cfg.DurationMs)
			if e.Phase() != PhaseEnded {
				t.Fatalf("phase = %s", e.Phase())
			}
			drifted := 0
			for _, en := range e.drifts.list {
				drifted += len(driftCreatedAt(evs, en.at))
			}
			if drifted < 50 {
				t.Errorf("drifted letters = %d, want >= 50", drifted)
			}
			type result struct{ reqID, status, reason string }
			byIndex := map[uint64]result{}
			applied := 0
			for _, a := range ofType[LedgerApplied](evs) {
				r := result{a.ReqID, a.Result, a.Reason}
				if prev, ok := byIndex[a.Index]; ok && prev != r {
					t.Errorf("index %d: island %d applied %+v, another island %+v", a.Index, a.IslandID, r, prev)
				}
				byIndex[a.Index] = r
				applied++
			}
			if applied == 0 {
				t.Error("no ledger.applied（確定を確かめられない）")
			}
			t.Logf("seed %d: drifted=%d applied=%d leaders=%d", seed, drifted, applied, len(ofType[LeaderElected](evs)))
		})
	}
}

// SD-5 決定性: 同じシード・同じ入力で 2 回 → トレースのハッシュが同じ。Replay でも同じ。
func TestDriftDeterministic(t *testing.T) {
	t.Parallel()
	cfg := sd4Config()
	run := func() *Engine {
		e := startedEngineWith(t, cfg, Options{})
		driftDrive(e, rand.New(rand.NewPCG(11, 12)), cfg.DurationMs)
		return e
	}
	a, b := run(), run()
	if a.TraceHash() != b.TraceHash() {
		t.Fatal("trace differs between two runs")
	}
	rp, err := Replay(cfg, "sha256:test", ReplayLog{
		Format:    ReplayFormat,
		StageHash: "sha256:test",
		Seed:      testSeed,
		Entries:   a.InputLog(),
		Until:     cfg.DurationMs,
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rp.TraceHash() != a.TraceHash() {
		t.Error("Replay trace differs")
	}
}

// 漂流瓶の時刻（window）はシードで決まり、範囲に入る。定義の誤りは New が失敗する。
func TestDriftConfig(t *testing.T) {
	t.Parallel()
	cfg := driftTestConfig(DriftDef{WindowStart: 100000, WindowEnd: 120000, Letters: 1, MinAgeMs: 0})
	e := newEngine(t, cfg, Options{})
	if at := e.drifts.list[0].at; at < 100000 || at > 120000 {
		t.Errorf("at = %d, want in [100000, 120000]", at)
	}
	want := 100000 + Sub(testSeed, StreamDrift).Int64N(20001)
	if int64(e.drifts.list[0].at) != want {
		t.Errorf("at = %d, want %d", e.drifts.list[0].at, want)
	}
	bad := []DriftDef{
		{WindowStart: -1, WindowEnd: 0, Letters: 1},
		{WindowStart: 10, WindowEnd: 9, Letters: 1},
		{WindowStart: 0, WindowEnd: 180001, Letters: 1},
		{WindowStart: 0, WindowEnd: 0, Letters: 0},
		{WindowStart: 0, WindowEnd: 0, Letters: 1, MinAgeMs: -1},
	}
	for _, d := range bad {
		if _, err := New(driftTestConfig(d), testSeed, Options{}); err == nil {
			t.Errorf("New(%+v) succeeded, want an error", d)
		}
	}
}

// SN-1 NextNotableAt が漂流瓶の時刻を返す（船が動かず、依頼もないとき）。
func TestDriftNextNotable(t *testing.T) {
	t.Parallel()
	cfg := driftTestConfig(driftAt(70000, 1, 0))
	e := startedEngineWith(t, cfg, Options{})
	if got, ok := e.NextNotableAt(); !ok || got != 70000 {
		t.Errorf("NextNotableAt = %d, %v, want 70000", got, ok)
	}
	e.RunUntil(70000)
	if got, ok := e.NextNotableAt(); ok {
		t.Errorf("after drift: NextNotableAt = %d, want none", got)
	}
}
