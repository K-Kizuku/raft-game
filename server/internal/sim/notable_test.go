package sim

import (
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

func checkNotable(t *testing.T, e *Engine, want SimTime, wantOK bool) {
	t.Helper()
	if got, ok := e.NextNotableAt(); got != want || ok != wantOK {
		t.Errorf("t=%d: NextNotableAt = (%d, %v), want (%d, %v)", e.Now(), got, ok, want, wantOK)
	}
}

func TestNextNotableAtNone(t *testing.T) { // NN-1
	t.Parallel()
	e := newEngine(t, testConfig(), Options{CheckInvariants: true})
	checkNotable(t, e, 0, false)
	mustApply(t, e, Command{Type: CmdGameStart})
	checkNotable(t, e, 0, false)
}

func TestNextNotableAtOneFerry(t *testing.T) { // NN-2
	t.Parallel()
	e := startedEngine(t)
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	before := stateOf(e)
	checkNotable(t, e, 3750, true)
	if after := stateOf(e); !reflect.DeepEqual(before, after) {
		t.Errorf("NextNotableAt changed the state:\n %+v\n %+v", before, after)
	}
	e.RunUntil(3749)
	checkNotable(t, e, 3750, true)
	e.DrainEvents()
	e.RunUntil(3750)
	evs := e.DrainEvents()
	arrived := ofType[FerryArrived](at(evs, 3750))
	if want := []FerryArrived{{FerryID: "f1", IslandID: 2, ReadyAt: 5250}}; !reflect.DeepEqual(arrived, want) {
		t.Errorf("ferry.arrived = %+v, want %+v", arrived, want)
	}
	checkNotable(t, e, 0, false)
}

func TestNextNotableAtTwoFerries(t *testing.T) { // NN-3
	t.Parallel()
	cfg := testConfig()
	cfg.Ferries = append(cfg.Ferries, ferry.FerryConfig{ID: "f2", Home: 3, Capacity: 4, SpeedPermille: 1000})
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f2", To: 2})
	checkNotable(t, e, 5250, true)
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	checkNotable(t, e, 3750, true)
	e.RunUntil(3750)
	checkNotable(t, e, 5250, true)

	t.Run("same time", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig()
		cfg.Ferries = append(cfg.Ferries, ferry.FerryConfig{ID: "f2", Home: 1, Capacity: 4, SpeedPermille: 1000})
		e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
		mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
		mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f2", To: 2})
		checkNotable(t, e, 3750, true)
	})
}

func TestNextNotableAtRoundEnd(t *testing.T) { // NN-4
	t.Parallel()
	for _, tt := range []struct {
		duration SimTime
		want     SimTime
		ok       bool
	}{
		{3749, 0, false},
		{3750, 3750, true},
	} {
		cfg := testConfig()
		cfg.DurationMs = tt.duration
		e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
		mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
		checkNotable(t, e, tt.want, tt.ok)
		e.RunUntil(tt.duration)
		if e.Phase() != PhaseEnded {
			t.Fatalf("Phase = %s, want ended", e.Phase())
		}
		checkNotable(t, e, 0, false)
	}
}

// skipRun は NN-5 の「次の出来事まで進める」をラウンドが終わるまでくり返し、入力ログ・全イベント・TraceHash を返す。
func skipRun(t *testing.T) ([]InputEntry, []DomainEvent, [32]byte) {
	t.Helper()
	e := newEngine(t, testConfig(), Options{CheckInvariants: true})
	mustApply(t, e, Command{Type: CmdGameStart})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	all := e.DrainEvents()
	next := []uint64{2, 3, 2, 1} // 1 → 2 → 3 → 2 → 1 → …（最初の 1 → 2 は上で出航した）
	hops, skips := 1, 0
	for e.Phase() != PhaseEnded {
		if f, _ := e.tr.Ferry("f1"); f.State == ferry.Docked {
			e.RunUntil(SimTime(f.ReadyAt))
			if e.Phase() != PhaseSailing {
				break
			}
			mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: next[hops%len(next)]})
			hops++
		}
		if target, ok := e.NextNotableAt(); ok {
			all = append(all, e.DrainEvents()...)
			e.RunUntil(target)
			evs := e.DrainEvents()
			all = append(all, evs...)
			if len(ofType[FerryArrived](at(evs, target))) == 0 {
				t.Errorf("no ferry.arrived at %d after skipping", target)
			}
			skips++
		} else {
			e.RunUntil(e.Now() + 1000)
		}
	}
	all = append(all, e.DrainEvents()...)
	if skips < 2 {
		t.Errorf("skipped %d times, want 2 or more", skips)
	}
	return e.InputLog(), all, e.TraceHash()
}

func TestNextNotableAtSkip(t *testing.T) { // NN-5
	t.Parallel()
	input, want, hash := skipRun(t)

	e := newEngine(t, testConfig(), Options{CheckInvariants: true})
	step := func(target SimTime) {
		for e.Now()+50 < target && e.Phase() == PhaseSailing {
			e.RunUntil(e.Now() + 50)
		}
		e.RunUntil(target)
	}
	for _, in := range input {
		step(in.T)
		mustApply(t, e, in.Cmd)
	}
	step(e.cfg.DurationMs)
	if got := e.DrainEvents(); !reflect.DeepEqual(got, want) {
		t.Errorf("events differ: %d vs %d", len(got), len(want))
	}
	if e.TraceHash() != hash {
		t.Error("TraceHash differs")
	}
}
