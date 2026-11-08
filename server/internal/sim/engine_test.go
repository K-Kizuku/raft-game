package sim

import (
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

const testSeed = 20261101

// testConfig はテストの基本の設定（値はテストの中の値）。
func testConfig() Config {
	return Config{
		Islands:       3,
		InitialLeader: 1,
		DurationMs:    120000,
		Raft: RaftConfig{
			TickMs:          500,
			HeartbeatTick:   2,
			ElectionTick:    40,
			MaxSizePerMsg:   4096,
			MaxInflightMsgs: 4,
		},
		Routes: []ferry.RouteConfig{
			{A: 1, B: 2, TravelMs: 3750},
			{A: 2, B: 3, TravelMs: 5250},
		},
		Ferries: []ferry.FerryConfig{
			{ID: "f1", Home: 1, Capacity: 4, SpeedPermille: 1000},
		},
		DockMs:        1500,
		PortCap:       24,
		PerLinkAppCap: 4,
		Resources: []ledger.ResourceDef{
			{ID: "firework:a", Kind: ledger.ResourceFirework},
			{ID: "stall:main", Kind: ledger.ResourceStall, Capacity: 2},
		},
	}
}

func newEngine(t *testing.T, cfg Config, opt Options) *Engine {
	t.Helper()
	e, err := New(cfg, testSeed, opt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

// startedEngine は testConfig の Engine を作って cmd.game.start を適用し、イベントを取り出しておく。
func startedEngine(t *testing.T) *Engine {
	t.Helper()
	return startedEngineWith(t, testConfig(), Options{CheckInvariants: true})
}

func startedEngineWith(t *testing.T, cfg Config, opt Options) *Engine {
	t.Helper()
	e := newEngine(t, cfg, opt)
	mustApply(t, e, Command{Type: CmdGameStart})
	e.DrainEvents()
	return e
}

func mustApply(t *testing.T, e *Engine, cmd Command) Ack {
	t.Helper()
	ack := e.Apply(cmd)
	if !ack.OK {
		t.Fatalf("Apply(%+v) = %+v, want OK", cmd, ack)
	}
	return ack
}

// mustPropose は cmd を island のノードに提案し、後処理まで行う。
func mustPropose(t *testing.T, e *Engine, island uint64, cmd ledger.Command) {
	t.Helper()
	data, err := ledger.Encode(cmd)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := e.propose(island, data); err != nil {
		t.Fatalf("propose(%d): %v", island, err)
	}
	e.afterStep()
}

// ofType は evs のうち型が T のものの中身を返す。
func ofType[T EventData](evs []DomainEvent) []T {
	var out []T
	for _, ev := range evs {
		if d, ok := ev.Data.(T); ok {
			out = append(out, d)
		}
	}
	return out
}

// datas はイベントの中身だけを並べる。
func datas(evs []DomainEvent) []EventData {
	out := make([]EventData, len(evs))
	for i, ev := range evs {
		out[i] = ev.Data
	}
	return out
}

// checkEvents は evs の中身の列が want と一致するかを確かめる（Type と Data の一致も確かめる）。
func checkEvents(t *testing.T, evs []DomainEvent, want []EventData) {
	t.Helper()
	for _, ev := range evs {
		if ev.Type != ev.Data.EventType() {
			t.Errorf("event %d: Type %s but Data is %T", ev.Seq, ev.Type, ev.Data)
		}
	}
	if got := datas(evs); !reflect.DeepEqual(got, want) {
		t.Errorf("events:\n got  %+v\n want %+v", got, want)
	}
}

func TestNewValidation(t *testing.T) { // N-1
	t.Parallel()
	tests := []struct {
		name string
		mod  func(*Config)
	}{
		{"islands 0", func(c *Config) { c.Islands = 0 }},
		{"initial leader 0", func(c *Config) { c.InitialLeader = 0 }},
		{"initial leader 4", func(c *Config) { c.InitialLeader = 4 }},
		{"duration 0", func(c *Config) { c.DurationMs = 0 }},
		{"tick 0", func(c *Config) { c.Raft.TickMs = 0 }},
		{"election <= heartbeat", func(c *Config) { c.Raft.ElectionTick = c.Raft.HeartbeatTick }},
		{"portCap 0", func(c *Config) { c.PortCap = 0 }},
		{"resource kind budget", func(c *Config) { c.Resources[0].Kind = "budget" }},
		{"route to 4", func(c *Config) { c.Routes[1].B = 4 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig()
			tt.mod(&cfg)
			e, err := New(cfg, testSeed, Options{})
			if err == nil || e != nil {
				t.Fatalf("New = %v, %v; want error", e, err)
			}
		})
	}
	t.Run("testConfig", func(t *testing.T) {
		t.Parallel()
		if _, err := New(testConfig(), testSeed, Options{}); err != nil {
			t.Errorf("New(testConfig()) = %v", err)
		}
	})
}

func TestNewInitialState(t *testing.T) { // N-2
	t.Parallel()
	e := newEngine(t, testConfig(), Options{})
	if e.Phase() != PhaseIntro || e.Now() != 0 {
		t.Errorf("Phase, Now = %s, %d; want intro, 0", e.Phase(), e.Now())
	}
	if evs := e.DrainEvents(); len(evs) != 0 {
		t.Errorf("DrainEvents = %v, want empty", evs)
	}
	if in := e.InputLog(); len(in) != 0 {
		t.Errorf("InputLog = %v, want empty", in)
	}
	h := e.TraceHash()
	if got := hex.EncodeToString(h[:]); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("TraceHash = %s, want SHA-256 of empty", got)
	}
}

func TestIntroPhase(t *testing.T) { // N-3
	t.Parallel()
	e := newEngine(t, testConfig(), Options{CheckInvariants: true})
	e.RunUntil(10000)
	if e.Now() != 0 {
		t.Errorf("Now = %d after RunUntil in intro, want 0", e.Now())
	}
	if evs := e.DrainEvents(); len(evs) != 0 {
		t.Errorf("events in intro: %v", evs)
	}
	for _, cmd := range []Command{
		{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{1}},
		{Type: CmdFerryDepart, FerryID: "f1", To: 2},
	} {
		ack := e.Apply(cmd)
		if ack.OK || ack.Code != CodePhaseInvalid || ack.T != 0 {
			t.Errorf("Apply(%s) = %+v, want phase_invalid", cmd.Type, ack)
		}
	}
	if in := e.InputLog(); len(in) != 0 {
		t.Errorf("InputLog = %v, want empty", in)
	}
	if evs := e.DrainEvents(); len(evs) != 0 {
		t.Errorf("events after failed commands: %v", evs)
	}
}

func TestGameStart(t *testing.T) { // S-1
	t.Parallel()
	e := newEngine(t, testConfig(), Options{CheckInvariants: true})
	ack := e.Apply(Command{Type: CmdGameStart})
	if ack != (Ack{OK: true, T: 0}) {
		t.Fatalf("start = %+v", ack)
	}
	if e.Phase() != PhaseSailing {
		t.Errorf("Phase = %s, want sailing", e.Phase())
	}
	evs := e.DrainEvents()
	ns := func(id uint64, role string) NodeState {
		return NodeState{ID: id, Role: role, Term: 2, Vote: 1, Lead: 1, Commit: 4, Applied: 4, LastIndex: 4}
	}
	checkEvents(t, evs, []EventData{
		PhaseChanged{Phase: PhaseOpening},
		LeaderElected{ID: 1, Term: 2},
		ns(1, "leader"),
		ns(2, "follower"),
		ns(3, "follower"),
		PhaseChanged{Phase: PhaseSailing},
	})
	for i, ev := range evs {
		if ev.T != 0 || ev.Seq != uint64(i+1) {
			t.Errorf("event %d: T, Seq = %d, %d; want 0, %d", i, ev.T, ev.Seq, i+1)
		}
	}
	want := []InputEntry{{T: 0, Cmd: Command{Type: CmdGameStart}}}
	if got := e.InputLog(); !reflect.DeepEqual(got, want) {
		t.Errorf("InputLog = %+v, want %+v", got, want)
	}
}

func TestGameStartTwice(t *testing.T) { // S-2
	t.Parallel()
	e := startedEngine(t)
	hash := e.TraceHash()
	ack := e.Apply(Command{Type: CmdGameStart})
	if ack.OK || ack.Code != CodePhaseInvalid {
		t.Errorf("second start = %+v, want phase_invalid", ack)
	}
	if evs := e.DrainEvents(); len(evs) != 0 {
		t.Errorf("events = %v, want none", evs)
	}
	if e.TraceHash() != hash {
		t.Error("TraceHash changed")
	}
	if n := len(e.InputLog()); n != 1 {
		t.Errorf("len(InputLog) = %d, want 1", n)
	}
}

func createdLetter(id, from, to uint64, label string, at SimTime) LetterView {
	return LetterView{ID: id, Kind: "raft", From: from, To: to, CreatedAt: at, Loc: portLoc(from), Label: label}
}

// summarizeLetters は letter.created の中身から raft を除いた値（比較用）を返す。
func summarizeLetters(evs []DomainEvent) []EventData {
	var out []EventData
	for _, ev := range evs {
		switch d := ev.Data.(type) {
		case LetterCreated:
			v := d.Letter
			v.Raft = nil
			out = append(out, LetterCreated{Letter: v})
		case LetterDropped, LetterMoved, LetterDelivered:
			out = append(out, d)
		}
	}
	return out
}

func TestHeartbeat(t *testing.T) { // T-1
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(999)
	if got := summarizeLetters(e.DrainEvents()); len(got) != 0 {
		t.Errorf("letters before 1000: %+v", got)
	}
	e.RunUntil(1000)
	evs := e.DrainEvents()
	for _, ev := range evs {
		if ev.T != 1000 {
			t.Errorf("event %d at %d, want 1000", ev.Seq, ev.T)
		}
	}
	if got, want := summarizeLetters(evs), []EventData{
		LetterCreated{Letter: createdLetter(1, 1, 2, "heartbeat", 1000)},
		LetterCreated{Letter: createdLetter(2, 1, 3, "heartbeat", 1000)},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("letters at 1000:\n got  %+v\n want %+v", got, want)
	}
	e.RunUntil(2000)
	if got, want := summarizeLetters(e.DrainEvents()), []EventData{
		LetterCreated{Letter: createdLetter(3, 1, 2, "heartbeat", 2000)},
		LetterDropped{LetterID: 1, Reason: "superseded"},
		LetterCreated{Letter: createdLetter(4, 1, 3, "heartbeat", 2000)},
		LetterDropped{LetterID: 2, Reason: "superseded"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("letters at 2000:\n got  %+v\n want %+v", got, want)
	}
}

func TestRunUntilStepsDoNotMatter(t *testing.T) { // T-2
	t.Parallel()
	const end = 30000
	run := func(steps func(e *Engine)) ([]DomainEvent, [32]byte) {
		e := startedEngine(t)
		steps(e)
		if e.Now() != end {
			t.Fatalf("Now = %d, want %d", e.Now(), end)
		}
		return e.DrainEvents(), e.TraceHash()
	}
	a, ha := run(func(e *Engine) { e.RunUntil(end) })
	b, hb := run(func(e *Engine) {
		for i := 1; i <= 600; i++ {
			e.RunUntil(SimTime(i * 50))
		}
	})
	c, hc := run(func(e *Engine) {
		steps := []SimTime{7, 13, 500, 1, 999, 250, 3, 1500, 2, 61}
		for i := 0; e.Now() < end; i++ {
			e.RunUntil(min(e.Now()+steps[i%len(steps)], end))
		}
	})
	if len(a) == 0 {
		t.Fatal("no events")
	}
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(a, c) {
		t.Errorf("events differ: len %d, %d, %d", len(a), len(b), len(c))
	}
	if ha != hb || ha != hc {
		t.Error("TraceHash differs")
	}
}

func TestCheckQuorumStepDown(t *testing.T) { // T-3
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(45000)
	evs := e.DrainEvents()
	downs := ofType[SteppedDown](evs)
	if want := []SteppedDown{{ID: 1, Term: 2, Reason: "check_quorum"}}; !reflect.DeepEqual(downs, want) {
		t.Errorf("stepped_down = %+v, want %+v", downs, want)
	}
	if got := ofType[LeaderElected](evs); len(got) != 0 {
		t.Errorf("leader_elected after start = %+v, want none", got)
	}
	for _, ev := range evs {
		if ev.Type == EvSteppedDown {
			t.Logf("stepped down at t=%d", ev.T)
		}
	}
}

func TestRoundEnd(t *testing.T) { // T-4
	t.Parallel()
	t.Run("arrive at end", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig()
		cfg.DurationMs = 3750
		e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
		mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
		e.RunUntil(100000)
		evs := e.DrainEvents()
		var at []DomainEvent
		for _, ev := range evs {
			if ev.T == 3750 {
				at = append(at, ev)
			}
		}
		if len(ofType[FerryArrived](at)) != 1 {
			t.Errorf("ferry.arrived at 3750 missing: %+v", datas(at))
		}
		if len(at) == 0 || !reflect.DeepEqual(at[len(at)-1].Data, PhaseChanged{Phase: PhaseEnded}) {
			t.Errorf("last event at 3750 is not phase.changed{ended}: %+v", datas(at))
		}
		if last := evs[len(evs)-1]; last.T != 3750 {
			t.Errorf("last event at %d, want 3750", last.T)
		}
		if e.Now() != 3750 || e.Phase() != PhaseEnded {
			t.Errorf("Now, Phase = %d, %s; want 3750, ended", e.Now(), e.Phase())
		}
		e.RunUntil(200000)
		if evs := e.DrainEvents(); len(evs) != 0 || e.Now() != 3750 {
			t.Errorf("RunUntil after end: %d events, Now %d", len(evs), e.Now())
		}
		for _, cmd := range []Command{
			{Type: CmdGameStart},
			{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{1}},
			{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: []uint64{1}},
			{Type: CmdFerryDepart, FerryID: "f1", To: 1},
		} {
			if ack := e.Apply(cmd); ack.OK || ack.Code != CodePhaseInvalid || ack.T != 3750 {
				t.Errorf("Apply(%s) after end = %+v, want phase_invalid", cmd.Type, ack)
			}
		}
	})
	t.Run("arrive after end", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig()
		cfg.DurationMs = 3749
		e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
		mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
		e.RunUntil(100000)
		evs := e.DrainEvents()
		if got := ofType[FerryArrived](evs); len(got) != 0 {
			t.Errorf("ferry.arrived = %+v, want none", got)
		}
		if e.Now() != 3749 || e.Phase() != PhaseEnded {
			t.Errorf("Now, Phase = %d, %s; want 3749, ended", e.Now(), e.Phase())
		}
	})
}

func TestRunUntilPast(t *testing.T) { // T-5
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(5000)
	e.DrainEvents()
	hash := e.TraceHash()
	e.RunUntil(4000)
	if e.Now() != 5000 {
		t.Errorf("Now = %d, want 5000", e.Now())
	}
	if evs := e.DrainEvents(); len(evs) != 0 || e.TraceHash() != hash {
		t.Errorf("RunUntil(past) produced %d events", len(evs))
	}
}
