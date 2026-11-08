package main

import (
	"fmt"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// PE-1 roundEndError（T-305b）。
func TestRoundEndError(t *testing.T) {
	t.Parallel()
	const dur = 120000
	demo := &stage.Stage{DurationMs: dur, Demo: &stage.Demo{EndOnAllCriticalSecured: true}}
	noEnd := &stage.Stage{DurationMs: dur, Demo: &stage.Demo{EndOnAllCriticalSecured: false}}
	noDemo := &stage.Stage{DurationMs: dur}
	early := sim.SimTime(9000)
	mismatch := fmt.Sprintf("ラウンドが終わらない: t %d が durationMs %d と違う", early, dur)

	if err := roundEndError(demo, sim.PhaseSailing, dur, 3, true); err == nil ||
		err.Error() != fmt.Sprintf("ラウンドが終わらない: phase %s（want %s）", sim.PhaseSailing, sim.PhaseEnded) {
		t.Errorf("sailing: err = %v", err)
	}
	if err := roundEndError(noDemo, sim.PhaseEnded, dur, 0, true); err != nil {
		t.Errorf("now == durationMs: err = %v", err)
	}
	for _, stars := range []int{2, 3} {
		if err := roundEndError(demo, sim.PhaseEnded, early, stars, true); err != nil {
			t.Errorf("demo, stars %d: err = %v", stars, err)
		}
	}
	for _, c := range []struct {
		name   string
		st     *stage.Stage
		stars  int
		scored bool
	}{
		{"stars 1", demo, 1, true},
		{"not scored", demo, 2, false},
		{"no demo", noDemo, 2, true},
		{"endOnAllCriticalSecured false", noEnd, 2, true},
	} {
		if err := roundEndError(c.st, sim.PhaseEnded, early, c.stars, c.scored); err == nil || err.Error() != mismatch {
			t.Errorf("%s: err = %v, want %q", c.name, err, mismatch)
		}
	}
}
