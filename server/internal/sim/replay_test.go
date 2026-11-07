package sim

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/cluster"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// rpStageHash はテストのステージのハッシュ（固定の文字列）。
const rpStageHash = "sha256:rp-test"

// rpConfig3 は testConfig に航路 1-3 と船 f2 を足した設定。
func rpConfig3() Config {
	cfg := testConfig()
	cfg.Routes = append(slices.Clone(cfg.Routes), ferry.RouteConfig{A: 1, B: 3, TravelMs: 6000})
	cfg.Ferries = append(slices.Clone(cfg.Ferries), ferry.FerryConfig{ID: "f2", Home: 3, Capacity: 3, SpeedPermille: 1200})
	return cfg
}

// rpConfig5 は testConfig の島・航路・船を 5 つの島の環に置き換えた設定。
func rpConfig5() Config {
	cfg := testConfig()
	cfg.Islands = 5
	cfg.Routes = []ferry.RouteConfig{
		{A: 1, B: 2, TravelMs: 3000},
		{A: 2, B: 3, TravelMs: 4000},
		{A: 3, B: 4, TravelMs: 3500},
		{A: 4, B: 5, TravelMs: 4500},
		{A: 1, B: 5, TravelMs: 5000},
	}
	cfg.Ferries = []ferry.FerryConfig{
		{ID: "f1", Home: 1, Capacity: 4, SpeedPermille: 1000},
		{ID: "f2", Home: 3, Capacity: 4, SpeedPermille: 1000},
		{ID: "f3", Home: 5, Capacity: 3, SpeedPermille: 800},
	}
	return cfg
}

func rpLog(seed uint64, entries []InputEntry, until SimTime) ReplayLog {
	return ReplayLog{Format: ReplayFormat, StageHash: rpStageHash, Seed: seed, Entries: entries, Until: until}
}

// rpState は Engine の比べる状態。
type rpState struct {
	Now       SimTime
	Phase     Phase
	Snapshot  Snapshot
	TraceHash [32]byte
	Input     []InputEntry
	Nodes     []cluster.Status
	Ledgers   []ledger.Snapshot
	Ferries   []ferry.Ferry
	Ports     [][]ferry.LetterID // 添字 = 島の ID - 1
}

func rpCapture(e *Engine) rpState {
	s := rpState{
		Now:       e.Now(),
		Phase:     e.Phase(),
		Snapshot:  e.Snapshot(),
		TraceHash: e.TraceHash(),
		Input:     e.InputLog(),
		Ferries:   e.tr.Ferries(),
	}
	for _, n := range e.nodes {
		s.Nodes = append(s.Nodes, n.Status())
	}
	for _, l := range e.ledgers {
		s.Ledgers = append(s.Ledgers, l.Snapshot())
	}
	for id := uint64(1); id <= uint64(len(e.nodes)); id++ {
		s.Ports = append(s.Ports, e.tr.Port(id))
	}
	return s
}

// rpDiffSlice は got と want を比べ、食い違ったら最初に違う位置だけを出す。一致すれば true。
func rpDiffSlice[T any](t testing.TB, name string, got, want []T) bool {
	t.Helper()
	for i := range min(len(got), len(want)) {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("%s[%d]:\n got  %+v\n want %+v", name, i, got[i], want[i])
			return false
		}
	}
	if len(got) != len(want) {
		t.Errorf("%s: len %d, want %d", name, len(got), len(want))
		return false
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: nil and empty differ (got nil %t, want nil %t)", name, got == nil, want == nil)
		return false
	}
	return true
}

func rpDiffValue(t testing.TB, name string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %+v, want %+v", name, got, want)
	}
}

// rpCheckState は 2 つの状態を比べ、違う欄（列なら最初に違う位置）だけを出す。
func rpCheckState(t testing.TB, label string, got, want rpState) {
	t.Helper()
	rpDiffValue(t, label+": Now", got.Now, want.Now)
	rpDiffValue(t, label+": Phase", got.Phase, want.Phase)
	if got.TraceHash != want.TraceHash {
		t.Errorf("%s: TraceHash = %s, want %s", label, hex.EncodeToString(got.TraceHash[:]), hex.EncodeToString(want.TraceHash[:]))
	}
	rpDiffSlice(t, label+": InputLog", got.Input, want.Input)
	gs, ws := got.Snapshot, want.Snapshot
	rpDiffValue(t, label+": Snapshot.Phase", gs.Phase, ws.Phase)
	rpDiffValue(t, label+": Snapshot.T", gs.T, ws.T)
	rpDiffValue(t, label+": Snapshot.EndsAt", gs.EndsAt, ws.EndsAt)
	rpDiffValue(t, label+": Snapshot.LastSeq", gs.LastSeq, ws.LastSeq)
	rpDiffSlice(t, label+": Snapshot.Ferries", gs.Ferries, ws.Ferries)
	rpDiffSlice(t, label+": Snapshot.Routes", gs.Routes, ws.Routes)
	rpDiffSlice(t, label+": Snapshot.Letters", gs.Letters, ws.Letters)
	rpDiffSlice(t, label+": Snapshot.Nodes", gs.Nodes, ws.Nodes)
	rpDiffSlice(t, label+": Status", got.Nodes, want.Nodes)
	rpDiffSlice(t, label+": Ledgers", got.Ledgers, want.Ledgers)
	rpDiffSlice(t, label+": Ferries", got.Ferries, want.Ferries)
	rpDiffSlice(t, label+": Ports", got.Ports, want.Ports)
	if !reflect.DeepEqual(got, want) && !t.Failed() {
		t.Errorf("%s: state differs", label)
	}
}

// rpCheckpoint は止まる前の Engine の、ある時点の記録。
type rpCheckpoint struct {
	Kind    string  // "cmd"（コマンドの直後）・"run"（RunUntil の後）・"end"（ラウンドの終わり）
	Until   SimTime // ReplayLog.Until に渡す値
	NInput  int     // その時点の len(InputLog())
	NEvents int     // その時点までのイベントの数
	State   rpState
}

// rpRun は rpRecord の結果。
type rpRun struct {
	Input       []InputEntry // 最後の InputLog()
	Events      []DomainEvent
	Hash        [32]byte
	Acks        []Ack // 失敗を含む
	Checkpoints []rpCheckpoint
}

// rpNeighbors は航路の一覧から、島 at の隣の島を航路の順に返す。
func rpNeighbors(routes []ferry.Route, at uint64) []uint64 {
	var out []uint64
	for _, r := range routes {
		switch at {
		case r.A:
			out = append(out, r.B)
		case r.B:
			out = append(out, r.A)
		}
	}
	return out
}

// rpRecord はボットで 1 ラウンドを動かし、入力ログ・全イベント・ハッシュ・Ack・復元の時点を記録する。
func rpRecord(t testing.TB, cfg Config, seed uint64) rpRun {
	t.Helper()
	e, err := New(cfg, seed, Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := rand.New(rand.NewPCG(seed, 0x7e5))
	var rec rpRun
	drain := func() { rec.Events = append(rec.Events, e.DrainEvents()...) }
	mark := func(kind string, until SimTime) {
		drain()
		rec.Checkpoints = append(rec.Checkpoints, rpCheckpoint{
			Kind:    kind,
			Until:   until,
			NInput:  len(e.InputLog()),
			NEvents: len(rec.Events),
			State:   rpCapture(e),
		})
	}
	markCmd := false // この回の最初に成功したコマンドの直後に "cmd" の時点を記録する
	apply := func(cmd Command) Ack {
		ack := e.Apply(cmd)
		rec.Acks = append(rec.Acks, ack)
		if ack.OK && markCmd {
			markCmd = false
			mark("cmd", 0)
		}
		return ack
	}

	if ack := apply(Command{Type: CmdGameStart}); !ack.OK {
		t.Fatalf("start: %+v", ack)
	}
	mark("cmd", 0)
	routes := e.tr.Routes()
	for k := 0; e.Phase() == PhaseSailing; k++ {
		markCmd = k%16 == 5
		now := e.Now()

		// 1. 停泊していて出航できる船を、確率 1/2 で積んで出す。
		for _, f0 := range e.tr.Ferries() {
			f, _ := e.tr.Ferry(f0.ID)
			if f.State != ferry.Docked || SimTime(f.ReadyAt) > now || r.IntN(2) == 0 {
				continue
			}
			nb := rpNeighbors(routes, f.At)
			if r.IntN(2) == 0 {
				apply(Command{Type: CmdFerryAutoload, FerryID: string(f.ID), To: nb[r.IntN(len(nb))]})
			} else if port, free := e.tr.Port(f.At), f.Capacity-len(f.Cargo); len(port) > 0 && free > 0 {
				n := 1 + r.IntN(min(len(port), free))
				var ids []uint64
				for _, i := range r.Perm(len(port))[:n] {
					ids = append(ids, uint64(port[i]))
				}
				apply(Command{Type: CmdFerryLoad, FerryID: string(f.ID), LetterIDs: ids})
			}
			f, _ = e.tr.Ferry(f0.ID)
			var dests []uint64 // 積み荷の宛先のうち隣の島
			for _, lid := range f.Cargo {
				if l, ok := e.tr.Letter(lid); ok && slices.Contains(nb, l.To) && !slices.Contains(dests, l.To) {
					dests = append(dests, l.To)
				}
			}
			to := nb[r.IntN(len(nb))]
			if len(dests) > 0 && r.IntN(4) < 3 {
				to = dests[r.IntN(len(dests))]
			}
			apply(Command{Type: CmdFerryDepart, FerryID: string(f.ID), To: to})
		}

		// 2. 確率 1/20 で、停泊中の船の積み荷を 1 通降ろす。
		if r.IntN(20) == 0 {
			var cands []ferry.Ferry
			for _, f := range e.tr.Ferries() {
				if f.State == ferry.Docked && len(f.Cargo) > 0 {
					cands = append(cands, f)
				}
			}
			if len(cands) > 0 {
				f := cands[r.IntN(len(cands))]
				apply(Command{Type: CmdFerryUnload, FerryID: string(f.ID), LetterIDs: []uint64{uint64(f.Cargo[r.IntN(len(f.Cargo))])}})
			}
		}

		// 3. 確率 1/20 で、わざと失敗するコマンドを送る。
		if r.IntN(20) == 0 {
			var sailing, notReady []ferry.Ferry
			for _, f := range e.tr.Ferries() {
				switch {
				case f.State == ferry.Sailing:
					sailing = append(sailing, f)
				case SimTime(f.ReadyAt) > now:
					notReady = append(notReady, f)
				}
			}
			bad := Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{999999}}
			switch r.IntN(3) {
			case 0:
				if len(sailing) > 0 {
					f := sailing[r.IntN(len(sailing))]
					bad = Command{Type: CmdFerryLoad, FerryID: string(f.ID), LetterIDs: []uint64{1}}
				}
			case 1:
				if len(notReady) > 0 {
					f := notReady[r.IntN(len(notReady))]
					nb := rpNeighbors(routes, f.At)
					bad = Command{Type: CmdFerryDepart, FerryID: string(f.ID), To: nb[r.IntN(len(nb))]}
				}
			}
			apply(bad)
		}
		markCmd = false

		// 4. 時間を進める。
		e.RunUntil(now + SimTime(50+r.IntN(1950)))
		drain()
		if k%16 == 11 {
			mark("run", e.Now())
		}
	}
	mark("end", cfg.DurationMs)
	rec.Input = e.InputLog()
	rec.Hash = e.TraceHash()
	return rec
}

// rpContinue は e に entries を RunUntil(T) → Apply で当て、最後に RunUntil(until) を呼ぶ。
func rpContinue(t testing.TB, e *Engine, entries []InputEntry, until SimTime) {
	t.Helper()
	for i, en := range entries {
		e.RunUntil(en.T)
		ack := e.Apply(en.Cmd)
		if !ack.OK || ack.T != en.T {
			t.Fatalf("continue: entry %d (t=%d, %s): ack %+v", i, en.T, en.Cmd.Type, ack)
		}
	}
	e.RunUntil(until)
}

func mustReplay(t testing.TB, cfg Config, in ReplayLog, opt Options) *Engine {
	t.Helper()
	e, err := Replay(cfg, rpStageHash, in, opt)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if e == nil {
		t.Fatal("Replay returned nil Engine without error")
	}
	return e
}

// rpCase は RP-3 の記録の設定とシード。
type rpCase struct {
	name string
	cfg  func() Config
	seed uint64
}

var rpCases = []rpCase{
	{"cfg3/seed1", rpConfig3, 1},
	{"cfg3/seed2", rpConfig3, 2},
	{"cfg3/seed3", rpConfig3, 3},
	{"cfg3/seed4", rpConfig3, 4},
	{"cfg5/seed1", rpConfig5, 1},
	{"cfg5/seed2", rpConfig5, 2},
	{"cfg5/seed3", rpConfig5, 3},
}

// RP-1
func TestReplayEmpty(t *testing.T) {
	t.Parallel()
	want := rpCapture(newEngine(t, testConfig(), Options{}))
	if want.Phase != PhaseIntro || want.Now != 0 || want.TraceHash != sha256.Sum256(nil) || len(want.Input) != 0 {
		t.Fatalf("fresh engine: phase %s, now %d, input %d", want.Phase, want.Now, len(want.Input))
	}
	for _, until := range []SimTime{0, 5000} {
		e := mustReplay(t, testConfig(), rpLog(testSeed, nil, until), Options{})
		rpCheckState(t, "empty", rpCapture(e), want)
		if evs := e.DrainEvents(); len(evs) != 0 {
			t.Errorf("until %d: DrainEvents = %d events, want 0", until, len(evs))
		}
	}
	e := mustReplay(t, testConfig(), rpLog(testSeed, []InputEntry{}, 0), Options{})
	rpCheckState(t, "empty slice", rpCapture(e), want)
}

// rpFixed は RP-2 の決まった操作を行う。depart の直後の状態とイベントの数、全イベントを返す。
func rpFixed(t *testing.T) (e *Engine, afterDepart rpState, nDepart int, events []DomainEvent) {
	t.Helper()
	e = newEngine(t, testConfig(), Options{CheckInvariants: true})
	mustApply(t, e, Command{Type: CmdGameStart})
	e.RunUntil(1000)
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{1}})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	events = e.DrainEvents()
	afterDepart, nDepart = rpCapture(e), len(events)
	e.RunUntil(3000)
	events = append(events, e.DrainEvents()...)
	return e, afterDepart, nDepart, events
}

// RP-2
func TestReplayFixed(t *testing.T) {
	t.Parallel()
	a, afterDepart, nDepart, aEvents := rpFixed(t)
	in := a.InputLog()
	if len(in) != 3 {
		t.Fatalf("InputLog = %d entries, want 3", len(in))
	}

	b := mustReplay(t, testConfig(), rpLog(testSeed, in, 3000), Options{})
	got := rpCapture(b)
	rpCheckState(t, "until 3000", got, rpCapture(a))
	want := FerryView{ID: "f1", Capacity: 4, State: "sailing", From: 1, To: 2, DepartAt: 1000, ArriveAt: 4750, ReadyAt: 6250, Cargo: []uint64{1}}
	rpDiffValue(t, "Snapshot().Ferries[0]", got.Snapshot.Ferries[0], want)
	rpDiffSlice(t, "events", b.DrainEvents(), aEvents)

	a.RunUntil(120000)
	b.RunUntil(120000)
	rpDiffSlice(t, "events after 3000", b.DrainEvents(), a.DrainEvents())
	if a.TraceHash() != b.TraceHash() {
		t.Error("TraceHash differs after RunUntil(120000)")
	}

	c := mustReplay(t, testConfig(), rpLog(testSeed, in, 0), Options{})
	if c.Now() != 1000 {
		t.Errorf("until 0: Now() = %d, want 1000", c.Now())
	}
	rpCheckState(t, "until 0", rpCapture(c), afterDepart)
	rpDiffSlice(t, "until 0: events", c.DrainEvents(), aEvents[:nDepart])
}

// RP-3
func TestReplayCheckpoints(t *testing.T) {
	t.Parallel()
	recs := make([]rpRun, len(rpCases))
	for i, tc := range rpCases {
		recs[i] = rpRecord(t, tc.cfg(), tc.seed)
	}
	rpCheckCoverage(t, recs)

	for i, tc := range rpCases {
		rec := recs[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tc.cfg()
			for j, c := range rec.Checkpoints {
				label := c.Kind + "#" + strconv.Itoa(j)
				e := mustReplay(t, cfg, rpLog(tc.seed, rec.Input[:c.NInput], c.Until), Options{})
				rpCheckState(t, label, rpCapture(e), c.State)
				evs := e.DrainEvents()
				rpDiffSlice(t, label+": events", evs, rec.Events[:c.NEvents])
				rpContinue(t, e, rec.Input[c.NInput:], cfg.DurationMs)
				evs = append(evs, e.DrainEvents()...)
				if e.TraceHash() != rec.Hash {
					t.Errorf("%s: TraceHash after continue differs", label)
				}
				rpDiffSlice(t, label+": events after continue", evs, rec.Events)
				rpDiffSlice(t, label+": InputLog after continue", e.InputLog(), rec.Input)
				if t.Failed() {
					return
				}
			}
		})
	}
}

// rpCheckCoverage は RP-3 の網羅の条件を確かめる。
func rpCheckCoverage(t *testing.T, recs []rpRun) {
	t.Helper()
	var sailingRun, portAndFerry, autoload, unload, delivered, dropped, leaderChange bool
	for i, rec := range recs {
		name := rpCases[i].name
		var kinds []string
		for _, c := range rec.Checkpoints {
			if !slices.Contains(kinds, c.Kind) {
				kinds = append(kinds, c.Kind)
			}
			var inPort, onFerry bool
			for _, p := range c.State.Ports {
				inPort = inPort || len(p) > 0
			}
			for _, f := range c.State.Ferries {
				onFerry = onFerry || len(f.Cargo) > 0
				if c.Kind == "run" && f.State == ferry.Sailing {
					sailingRun = true
				}
			}
			portAndFerry = portAndFerry || (inPort && onFerry)
		}
		for _, k := range []string{"cmd", "run", "end"} {
			if !slices.Contains(kinds, k) {
				t.Errorf("%s: no %q checkpoint", name, k)
			}
		}
		if len(rec.Acks) <= len(rec.Input) {
			t.Errorf("%s: no failed ack (%d acks, %d inputs)", name, len(rec.Acks), len(rec.Input))
		}
		for _, in := range rec.Input {
			autoload = autoload || in.Cmd.Type == CmdFerryAutoload
			unload = unload || in.Cmd.Type == CmdFerryUnload
		}
		for _, ev := range rec.Events {
			switch d := ev.Data.(type) {
			case LetterDelivered:
				delivered = delivered || d.Accepted
			case LetterDropped:
				dropped = true
			case SteppedDown:
				leaderChange = leaderChange || ev.T > 0
			case LeaderElected:
				leaderChange = leaderChange || d.Term > 2
			}
		}
		for j := range i {
			if recs[j].Hash == rec.Hash {
				t.Errorf("%s and %s have the same TraceHash", rpCases[j].name, name)
			}
		}
	}
	for _, c := range []struct {
		ok   bool
		name string
	}{
		{sailingRun, `"run" checkpoint with a sailing ferry`},
		{portAndFerry, "checkpoint with letters in a port and on a ferry"},
		{autoload, "successful cmd.ferry.autoload"},
		{unload, "successful cmd.ferry.unload"},
		{delivered, "accepted letter.delivered"},
		{dropped, "letter.dropped"},
		{leaderChange, "node.stepped_down after the opening or node.leader_elected with term > 2"},
	} {
		if !c.ok {
			t.Errorf("coverage: no %s", c.name)
		}
	}
}

func rpEnd(t *testing.T, rec rpRun) rpCheckpoint {
	t.Helper()
	c := rec.Checkpoints[len(rec.Checkpoints)-1]
	if c.Kind != "end" {
		t.Fatalf("last checkpoint is %q", c.Kind)
	}
	return c
}

// RP-4
func TestReplayEnded(t *testing.T) {
	t.Parallel()
	cfg := rpConfig3()
	rec := rpRecord(t, cfg, 1)
	c := rpEnd(t, rec)
	for _, until := range []SimTime{cfg.DurationMs, cfg.DurationMs + 60000} {
		e := mustReplay(t, cfg, rpLog(1, rec.Input[:c.NInput], until), Options{})
		if e.Phase() != PhaseEnded || e.Now() != cfg.DurationMs {
			t.Errorf("until %d: phase %s, now %d", until, e.Phase(), e.Now())
		}
		evs := e.DrainEvents()
		if len(evs) == 0 || !reflect.DeepEqual(evs[len(evs)-1].Data, PhaseChanged{Phase: PhaseEnded}) {
			t.Errorf("until %d: last event is not phase.changed{ended}", until)
		}
		if e.TraceHash() != rec.Hash {
			t.Errorf("until %d: TraceHash differs", until)
		}
		rpCheckState(t, "end", rpCapture(e), c.State)
	}
}

// RP-5
func TestReplayCheckInvariants(t *testing.T) {
	t.Parallel()
	cfg := rpConfig3()
	rec := rpRecord(t, cfg, 1)
	for j, c := range rec.Checkpoints {
		in := rpLog(1, rec.Input[:c.NInput], c.Until)
		plain := mustReplay(t, cfg, in, Options{})
		checked := mustReplay(t, cfg, in, Options{CheckInvariants: true})
		rpCheckState(t, c.Kind+"#"+strconv.Itoa(j), rpCapture(checked), rpCapture(plain))
	}
}

// RP-6
func TestReplayErrors(t *testing.T) {
	t.Parallel()
	start := InputEntry{T: 0, Cmd: Command{Type: CmdGameStart}}
	load := func(tt SimTime, ids ...uint64) InputEntry {
		return InputEntry{T: tt, Cmd: Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: ids}}
	}
	depart := func(tt SimTime, to uint64) InputEntry {
		return InputEntry{T: tt, Cmd: Command{Type: CmdFerryDepart, FerryID: "f1", To: to}}
	}
	log := func(entries ...InputEntry) ReplayLog { return rpLog(testSeed, entries, 0) }
	with := func(in ReplayLog, f func(*ReplayLog)) ReplayLog { f(&in); return in }
	noIslands := testConfig()
	noIslands.Islands = 0

	cases := []struct {
		name      string
		cfg       Config // Islands == 0 のときだけ noIslands
		stageHash string
		in        ReplayLog
		want      error // nil なら New のエラー（ErrReplay… のどれでもない）
		contains  []string
	}{
		{"format 0", testConfig(), rpStageHash, with(log(start), func(l *ReplayLog) { l.Format = 0 }), ErrReplayFormat, nil},
		{"format 2", testConfig(), rpStageHash, with(log(start), func(l *ReplayLog) { l.Format = 2 }), ErrReplayFormat, nil},
		{"empty stage hashes", testConfig(), "", with(log(start), func(l *ReplayLog) { l.StageHash = "" }), ErrReplayStage, nil},
		{"stage mismatch", testConfig(), "sha256:a", with(log(start), func(l *ReplayLog) { l.StageHash = "sha256:b" }), ErrReplayStage, []string{"sha256:a", "sha256:b"}},
		{"format before stage", testConfig(), "sha256:a", with(log(start), func(l *ReplayLog) { l.Format = 2; l.StageHash = "sha256:b" }), ErrReplayFormat, nil},
		{"negative t", testConfig(), rpStageHash, log(InputEntry{T: -1, Cmd: Command{Type: CmdGameStart}}), ErrReplayOrder, []string{"entry 0"}},
		{"decreasing t", testConfig(), rpStageHash, log(start, depart(2000, 2), load(1000, 1)), ErrReplayOrder, []string{"entry 2"}},
		{"until before last", testConfig(), rpStageHash, with(log(start, load(1000, 1)), func(l *ReplayLog) { l.Until = 500 }), ErrReplayOrder, nil},
		{"negative until", testConfig(), rpStageHash, with(log(start), func(l *ReplayLog) { l.Until = -5 }), ErrReplayOrder, nil},
		{"stage before order", testConfig(), "sha256:a", with(log(InputEntry{T: -1, Cmd: Command{Type: CmdGameStart}}), func(l *ReplayLog) { l.StageHash = "sha256:b" }), ErrReplayStage, nil},
		{"no islands", noIslands, rpStageHash, log(start), nil, nil},
		{"no start", testConfig(), rpStageHash, log(load(0, 1)), ErrReplayDiverged, []string{CodePhaseInvalid, "entry 0"}},
		{"two starts", testConfig(), rpStageHash, log(start, start), ErrReplayDiverged, []string{CodePhaseInvalid, "entry 1"}},
		{"start not at 0", testConfig(), rpStageHash, log(InputEntry{T: 5, Cmd: Command{Type: CmdGameStart}}), ErrReplayDiverged, []string{"entry 0", "applied at t=0, recorded t=5"}},
		{"after the end", testConfig(), rpStageHash, log(start, load(200000, 1)), ErrReplayDiverged, []string{CodePhaseInvalid, "entry 1"}},
		{"at the end", testConfig(), rpStageHash, log(start, depart(120000, 2)), ErrReplayDiverged, []string{CodePhaseInvalid, "entry 1"}},
		{"session command", testConfig(), rpStageHash, log(start, InputEntry{T: 1000, Cmd: Command{Type: "cmd.game.pause"}}), ErrReplayDiverged, []string{CodeBadRequest, "entry 1"}},
		{"unknown letter", testConfig(), rpStageHash, log(start, load(1000, 999)), ErrReplayDiverged, []string{CodeLetterNotHere, "entry 1"}},
		{"no route", testConfig(), rpStageHash, log(start, depart(0, 3)), ErrReplayDiverged, []string{CodeNoRoute, "entry 1"}},
	}
	all := []error{ErrReplayFormat, ErrReplayStage, ErrReplayOrder, ErrReplayDiverged}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, err := Replay(tc.cfg, tc.stageHash, tc.in, Options{})
			if e != nil {
				t.Error("Engine is not nil")
			}
			if err == nil {
				t.Fatal("err is nil")
			}
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Errorf("err = %v, want %v", err, tc.want)
				}
			} else {
				for _, w := range all {
					if errors.Is(err, w) {
						t.Errorf("err = %v, want an error from New", err)
					}
				}
			}
			for _, s := range tc.contains {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("err = %q, want it to contain %q", err, s)
				}
			}
		})
	}
}

// RP-7
func TestReplayDoesNotShare(t *testing.T) {
	t.Parallel()
	a, _, _, _ := rpFixed(t)
	in := rpLog(testSeed, a.InputLog(), 3000)
	orig := a.InputLog() // 深い複製
	e := mustReplay(t, testConfig(), in, Options{})
	rpDiffSlice(t, "Entries after Replay", in.Entries, orig)
	before := e.InputLog()
	in.Entries[1].Cmd.LetterIDs[0] = 999
	rpDiffSlice(t, "InputLog after changing Entries", e.InputLog(), before)
}

func rpLastRun(t *testing.T, rec rpRun) rpCheckpoint {
	t.Helper()
	for j := len(rec.Checkpoints) - 1; j >= 0; j-- {
		if rec.Checkpoints[j].Kind == "run" {
			return rec.Checkpoints[j]
		}
	}
	t.Fatal("no run checkpoint")
	return rpCheckpoint{}
}

// RP-8
func TestReplayTwice(t *testing.T) {
	t.Parallel()
	cfg := rpConfig5()
	rec := rpRecord(t, cfg, 1)
	c := rpLastRun(t, rec)
	in := rpLog(1, rec.Input[:c.NInput], c.Until)
	a := mustReplay(t, cfg, in, Options{})
	b := mustReplay(t, cfg, in, Options{})
	rpCheckState(t, "twice", rpCapture(b), rpCapture(a))
	rpDiffSlice(t, "events", b.DrainEvents(), a.DrainEvents())
}

// RP-9
func TestReplayRestoreTwice(t *testing.T) {
	t.Parallel()
	cfg := rpConfig3()
	rec := rpRecord(t, cfg, 2)
	var runs []rpCheckpoint
	for _, c := range rec.Checkpoints {
		if c.Kind == "run" {
			runs = append(runs, c)
		}
	}
	if len(runs) < 2 {
		t.Fatalf("%d run checkpoints, want 2 or more", len(runs))
	}
	c1, c2 := runs[0], runs[1]
	e := mustReplay(t, cfg, rpLog(2, rec.Input[:c1.NInput], c1.Until), Options{})
	rpContinue(t, e, rec.Input[c1.NInput:c2.NInput], c2.Until)
	rpCheckState(t, "first restore", rpCapture(e), c2.State)
	e2 := mustReplay(t, cfg, rpLog(2, e.InputLog(), c2.Until), Options{})
	rpCheckState(t, "second restore", rpCapture(e2), c2.State)
}

// RP-10
func TestReplaySameTime(t *testing.T) {
	t.Parallel()
	a := newEngine(t, testConfig(), Options{CheckInvariants: true})
	mustApply(t, a, Command{Type: CmdGameStart})
	a.RunUntil(1000)
	mustApply(t, a, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{1}})
	mustApply(t, a, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{2}})
	mustApply(t, a, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	a.RunUntil(2000)
	in := a.InputLog()
	var at1000 int
	for _, en := range in {
		if en.T == 1000 {
			at1000++
		}
	}
	if len(in) != 4 || at1000 != 3 {
		t.Fatalf("InputLog = %d entries (%d at t=1000), want 4 (3)", len(in), at1000)
	}
	b := mustReplay(t, testConfig(), rpLog(testSeed, in, 2000), Options{})
	rpCheckState(t, "same time", rpCapture(b), rpCapture(a))
	rpDiffSlice(t, "events", b.DrainEvents(), a.DrainEvents())
}

func BenchmarkReplayRound(b *testing.B) {
	cfg := rpConfig5()
	rec := rpRecord(b, cfg, 1)
	in := rpLog(1, rec.Input, cfg.DurationMs)
	for b.Loop() {
		if _, err := Replay(cfg, rpStageHash, in, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
