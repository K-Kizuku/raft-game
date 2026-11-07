package sim

import (
	"reflect"
	"testing"
)

func TestResult(t *testing.T) { // S-13
	t.Parallel()
	e := newEngine(t, scoreTestConfig(stallReq(0, 1)), Options{CheckInvariants: true})
	notYet := func(name string) {
		t.Helper()
		if r, ok := e.Result(); ok || !reflect.DeepEqual(r, RoundResult{}) {
			t.Errorf("%s: Result() = %+v, %v; want zero, false", name, r, ok)
		}
	}
	notYet("intro")
	mustApply(t, e, Command{Type: CmdGameStart})
	notYet("start")
	runE1(t, e, func(string) {})
	e.RunUntil(119999)
	if e.Phase() != PhaseSailing {
		t.Fatalf("phase = %s", e.Phase())
	}
	notYet("sailing")

	e.RunUntil(120000)
	hash, pending, seq := e.TraceHash(), len(e.pending), e.seq
	r1 := mustResult(t, e)
	r2 := mustResult(t, e)
	if !reflect.DeepEqual(r1, r2) {
		t.Errorf("Result() changed: %+v → %+v", r1, r2)
	}
	if e.TraceHash() != hash || len(e.pending) != pending || e.seq != seq {
		t.Error("Result() changed the events")
	}
	checkBreakdown(t, r1)
	if r1.Score.Total != 396 {
		t.Errorf("Result() = %+v, want total 396", r1)
	}
}
