package bot

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

const a2Seed = 20261101

// newInput は Act の前後の入力ログの差分。
func newInput(before, after []sim.InputEntry) []sim.InputEntry {
	return after[len(before):]
}

func entries(t sim.SimTime, cmds ...sim.Command) []sim.InputEntry {
	out := make([]sim.InputEntry, len(cmds))
	for i, c := range cmds {
		out[i] = sim.InputEntry{T: t, Cmd: c}
	}
	return out
}

func ferryByID(t *testing.T, s sim.Snapshot, id string) sim.FerryView {
	t.Helper()
	k := slices.IndexFunc(s.Ferries, func(f sim.FerryView) bool { return f.ID == id })
	if k < 0 {
		t.Fatalf("ferry %q not in snapshot", id)
	}
	return s.Ferries[k]
}

// A-1
func TestActPhase(t *testing.T) {
	t.Parallel()
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEngine(t, lineConfig(), 1)
			b := mustNew(t, name, 1)
			if err := b.Act(e); err != nil {
				t.Fatalf("Act in intro: %v", err)
			}
			if n := len(e.InputLog()); n != 0 {
				t.Fatalf("input log after Act in intro has %d entries", n)
			}
			if ack := e.Apply(sim.Command{Type: sim.CmdGameStart}); !ack.OK {
				t.Fatalf("start: %+v", ack)
			}
			e.RunUntil(lineConfig().DurationMs)
			if e.Phase() != sim.PhaseEnded {
				t.Fatalf("phase = %s", e.Phase())
			}
			before := len(e.InputLog())
			if err := b.Act(e); err != nil {
				t.Fatalf("Act in ended: %v", err)
			}
			if n := len(e.InputLog()); n != before {
				t.Fatalf("input log grew from %d to %d", before, n)
			}
		})
	}
}

// a2Engine は A-2 の状態（lineConfig・シード 20261101、開始 → RunUntil(1000)）の Engine と、
// 港 1 の heartbeat 1→2・1→3 の ID を返す。
func a2Engine(t *testing.T, cfg sim.Config) (e *sim.Engine, h2, h3 uint64) {
	t.Helper()
	e = startedEngine(t, cfg, a2Seed)
	e.RunUntil(1000)
	port := portLetters(e.Snapshot(), 1)
	if len(port) != 2 {
		t.Fatalf("port 1 = %+v, want 2 letters", port)
	}
	for _, l := range port {
		if l.Label != "heartbeat" || l.From != 1 {
			t.Fatalf("port 1 letter = %+v", l)
		}
		switch l.To {
		case 2:
			h2 = l.ID
		case 3:
			h3 = l.ID
		}
	}
	if h2 == 0 || h3 == 0 {
		t.Fatalf("port 1 = %+v, want heartbeat to 2 and 3", port)
	}
	return e, h2, h3
}

// A-2・A-3
func TestGreedyFirstMoves(t *testing.T) {
	t.Parallel()
	e, h2, h3 := a2Engine(t, lineConfig())
	b := mustNew(t, "greedy", a2Seed)

	before := e.InputLog()
	if err := b.Act(e); err != nil {
		t.Fatalf("Act: %v", err)
	}
	want := entries(1000, cmdLoad("f1", []uint64{h3}), cmdAutoload("f1", 2), cmdDepart("f1", 2))
	if got := newInput(before, e.InputLog()); !reflect.DeepEqual(got, want) {
		t.Fatalf("A-2 input = %+v, want %+v", got, want)
	}
	f1 := ferryByID(t, e.Snapshot(), "f1")
	if f1.State != "sailing" || f1.To != 2 || f1.ArriveAt != 4750 || !reflect.DeepEqual(f1.Cargo, []uint64{h3, h2}) {
		t.Fatalf("A-2 f1 = %+v", f1)
	}
	before = e.InputLog()
	if err := b.Act(e); err != nil {
		t.Fatalf("Act again: %v", err)
	}
	if n := len(e.InputLog()); n != len(before) {
		t.Fatalf("A-2 second Act added %d entries", n-len(before))
	}

	// A-3
	e.RunUntil(6250)
	port := portLetters(e.Snapshot(), 2)
	if len(port) != 1 || port[0].Label != "heartbeat_resp" || port[0].To != 1 {
		t.Fatalf("A-3 port 2 = %+v", port)
	}
	before = e.InputLog()
	if err := b.Act(e); err != nil {
		t.Fatalf("A-3 Act: %v", err)
	}
	want = entries(6250, cmdAutoload("f1", 1), cmdDepart("f1", 1))
	if got := newInput(before, e.InputLog()); !reflect.DeepEqual(got, want) {
		t.Fatalf("A-3 input = %+v, want %+v", got, want)
	}
}

// A-4
func TestGreedyTwinFerries(t *testing.T) {
	t.Parallel()
	e, _, h3 := a2Engine(t, twinConfig())
	// 取り直さなければ、f2 は f1 と同じ手紙を load しようとする（このテストが取り直しを確かめている前提）。
	if p := greedyPlan(e.Snapshot(), 1); len(p) == 0 || !reflect.DeepEqual(p[0], cmdLoad("f2", []uint64{h3})) {
		t.Fatalf("f2 plan on the first snapshot = %+v, want load %d first", p, h3)
	}
	b := mustNew(t, "greedy", a2Seed)
	before := e.InputLog()
	if err := b.Act(e); err != nil {
		t.Fatalf("Act: %v", err)
	}
	want := entries(1000, cmdLoad("f1", []uint64{h3}), cmdAutoload("f1", 2), cmdDepart("f1", 2))
	if got := newInput(before, e.InputLog()); !reflect.DeepEqual(got, want) {
		t.Fatalf("input = %+v, want %+v", got, want)
	}
}

// A-5
func TestApplyPlanStopsOnFailure(t *testing.T) {
	t.Parallel()
	e := startedEngine(t, lineConfig(), 1)
	before := len(e.InputLog())
	err := applyPlan(e, []sim.Command{cmdDepart("f1", 99), cmdDepart("f1", 2)})
	if err == nil || !strings.Contains(err.Error(), sim.CodeNoRoute) {
		t.Fatalf("applyPlan = %v, want %s", err, sim.CodeNoRoute)
	}
	if f1 := ferryByID(t, e.Snapshot(), "f1"); f1.State != "docked" {
		t.Fatalf("f1 = %+v", f1)
	}
	if n := len(e.InputLog()); n != before {
		t.Fatalf("input log grew from %d to %d", before, n)
	}
}

// PL-1
func TestPlayArgs(t *testing.T) {
	t.Parallel()
	b := mustNew(t, "greedy", 1)
	intro := newEngine(t, lineConfig(), 1)
	sailing := startedEngine(t, lineConfig(), 1)
	ended := startedEngine(t, lineConfig(), 1)
	ended.RunUntil(lineConfig().DurationMs)

	tests := []struct {
		name  string
		e     *sim.Engine
		b     Bot
		phase sim.Phase
	}{
		{"nil engine", nil, b, ""},
		{"nil bot", intro, nil, sim.PhaseIntro},
		{"sailing", sailing, b, sim.PhaseSailing},
		{"ended", ended, b, sim.PhaseEnded},
	}
	for _, tt := range tests {
		var before []sim.InputEntry
		if tt.e != nil {
			before = tt.e.InputLog()
		}
		if err := Play(tt.e, tt.b); err == nil {
			t.Errorf("%s: Play returned nil", tt.name)
		}
		if tt.e == nil {
			continue
		}
		if got := tt.e.InputLog(); !reflect.DeepEqual(got, before) {
			t.Errorf("%s: input log changed: %+v", tt.name, got)
		}
		if tt.e.Phase() != tt.phase {
			t.Errorf("%s: phase = %s, want %s", tt.name, tt.e.Phase(), tt.phase)
		}
	}
}

// PL-2
func TestPlayFlow(t *testing.T) {
	t.Parallel()
	cfg := lineConfig()
	e := newEngine(t, cfg, 1)
	if err := Play(e, mustNew(t, "greedy", 1)); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if e.Phase() != sim.PhaseEnded || e.Now() != cfg.DurationMs {
		t.Fatalf("phase %s now %d", e.Phase(), e.Now())
	}
	in := e.InputLog()
	if want := (sim.InputEntry{T: 0, Cmd: sim.Command{Type: sim.CmdGameStart}}); !reflect.DeepEqual(in[0], want) {
		t.Fatalf("input[0] = %+v", in[0])
	}
	evs := e.DrainEvents()
	if len(evs) == 0 || evs[0].Seq != 1 {
		t.Fatalf("first event = %+v", evs[:min(1, len(evs))])
	}
	last := evs[len(evs)-1]
	if pc, ok := last.Data.(sim.PhaseChanged); !ok || pc.Phase != sim.PhaseEnded || last.T != cfg.DurationMs {
		t.Fatalf("last event = %+v", last)
	}
}

// playRun は name のボットで cfg・seed のラウンドを Play し、Engine とすべてのイベントを返す。
func playRun(t *testing.T, name string, cfg sim.Config, seed uint64) (*sim.Engine, []sim.DomainEvent) {
	t.Helper()
	e := newEngine(t, cfg, seed)
	if err := Play(e, mustNew(t, name, seed)); err != nil {
		t.Fatalf("%s seed %d: Play: %v", name, seed, err)
	}
	return e, e.DrainEvents()
}

// PL-3
func TestPlayManySeeds(t *testing.T) {
	t.Parallel()
	for _, name := range Names() {
		for _, c := range testConfigs {
			t.Run(name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				cfg := c.cfg()
				total := 0
				for seed := uint64(1); seed <= 8; seed++ {
					_, evs := playRun(t, name, cfg, seed)
					departed := make([]bool, len(cfg.Ferries))
					delivered := 0
					for _, ev := range evs {
						switch d := ev.Data.(type) {
						case sim.FerryDeparted:
							k := slices.IndexFunc(cfg.Ferries, func(f ferry.FerryConfig) bool { return string(f.ID) == d.FerryID })
							departed[k] = true
						case sim.LetterDelivered:
							if d.Accepted {
								delivered++
							}
						}
					}
					for k, ok := range departed {
						if !ok {
							t.Errorf("seed %d: ferry %s never departed", seed, cfg.Ferries[k].ID)
						}
					}
					if delivered == 0 {
						t.Errorf("seed %d: no accepted delivery", seed)
					}
					total += delivered
				}
				t.Logf("PL-3 %s/%s: accepted deliveries over seeds 1..8 = %d", name, c.name, total)
			})
		}
	}
}

// PL-4
func TestPlayDeterminism(t *testing.T) {
	t.Parallel()
	for _, name := range Names() {
		for _, c := range testConfigs[1:] {
			for seed := uint64(1); seed <= 3; seed++ {
				t.Run(name+"/"+c.name+"/"+strconv.FormatUint(seed, 10), func(t *testing.T) {
					t.Parallel()
					cfg := c.cfg()
					e1, ev1 := playRun(t, name, cfg, seed)
					e2, ev2 := playRun(t, name, cfg, seed)
					if !reflect.DeepEqual(e1.InputLog(), e2.InputLog()) {
						t.Fatal("input logs differ")
					}
					if !reflect.DeepEqual(ev1, ev2) {
						t.Fatal("events differ")
					}
					if e1.TraceHash() != e2.TraceHash() {
						t.Fatal("trace hashes differ")
					}

					// 再生: ボットを動かさずに入力ログだけで再現できる。
					r := newEngine(t, cfg, seed)
					for k, in := range e1.InputLog() {
						r.RunUntil(in.T)
						ack := r.Apply(in.Cmd)
						if !ack.OK || ack.T != in.T {
							t.Fatalf("replay #%d %+v: ack %+v", k, in, ack)
						}
					}
					r.RunUntil(cfg.DurationMs)
					if r.TraceHash() != e1.TraceHash() {
						t.Fatal("replayed trace hash differs")
					}
				})
			}
		}
	}

	t.Run("random seeds differ", func(t *testing.T) {
		t.Parallel()
		var hashes [][32]byte
		for seed := uint64(1); seed <= 8; seed++ {
			e, _ := playRun(t, "random", ringConfig(), seed)
			h := e.TraceHash()
			if !slices.Contains(hashes, h) {
				hashes = append(hashes, h)
			}
		}
		if len(hashes) < 2 {
			t.Fatalf("only %d distinct trace hashes", len(hashes))
		}
	})
}

// W-1
func TestNextWake(t *testing.T) {
	t.Parallel()
	dockedAt := func(readyAt sim.SimTime) sim.FerryView {
		f := docked("f1", 1, 4)
		f.ReadyAt = readyAt
		return f
	}
	sailing := sim.FerryView{ID: "f1", State: "sailing", ReadyAt: 1100, ArriveAt: 1000, Cargo: []uint64{}}
	tests := []struct {
		name       string
		ferries    []sim.FerryView
		notable    sim.SimTime
		hasNotable bool
		want       sim.SimTime
	}{
		{"nothing", nil, 0, false, 1500},
		{"notable", nil, 1200, true, 1200},
		{"notable now", nil, 1000, true, 1500},
		{"notable after poll", nil, 1600, true, 1500},
		{"ready", []sim.FerryView{dockedAt(1300)}, 0, false, 1300},
		{"already ready", []sim.FerryView{dockedAt(900)}, 0, false, 1500},
		{"notable and ready", []sim.FerryView{dockedAt(1300)}, 1400, true, 1300},
		{"sailing", []sim.FerryView{sailing}, 0, false, 1500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := snap(1000, 3, routes(lineRoutes...), tt.ferries, nil)
			if got := nextWake(s, tt.notable, tt.hasNotable, 500); got != tt.want {
				t.Fatalf("nextWake = %d, want %d", got, tt.want)
			}
		})
	}
}
