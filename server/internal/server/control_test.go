package server

import (
	"math"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// LP-2: pauseState の表。
func TestPauseState(t *testing.T) {
	t.Parallel()
	check := func(name string, p pauseState, paused, auto bool) {
		t.Helper()
		if p.isPaused() != paused || p.auto != auto {
			t.Errorf("%s: %+v, want paused %v auto %v", name, p, paused, auto)
		}
	}

	var p pauseState
	check("最初", p, false, false)
	p.detach()
	check("detach", p, true, true)
	if !p.attach() {
		t.Error("attach after detach = false")
	}
	check("attach", p, false, false)
	if p.attach() {
		t.Error("attach twice = true")
	}

	p = pauseState{}
	p.set(true)
	p.detach()
	check("set(true) → detach", p, true, false)
	if p.attach() {
		t.Error("attach after set(true) = true")
	}
	check("set(true) → detach → attach", p, true, false)

	p = pauseState{}
	p.hold()
	check("hold", p, true, false)
	p.detach()
	if p.attach() {
		t.Error("attach after hold = true")
	}
	check("hold → attach", p, true, false)

	p = pauseState{}
	p.detach()
	p.set(false)
	check("detach → set(false)", p, false, false)
	if p.attach() {
		t.Error("attach after set(false) = true")
	}

	p = pauseState{}
	p.detach()
	p.hold()
	check("detach → hold", p, true, false)
	p.set(true)
	p.set(true)
	check("set(true) を 2 回", p, true, false)
}

// LP-3: allowedSpeedsPermille と parseSpeedPermille。
func TestSpeeds(t *testing.T) {
	t.Parallel()
	st := &stage.Stage{SpeedAllowedPermille: []int64{500, 1000, 2000, 4000}}
	if got := allowedSpeedsPermille(st, false); !slices.Equal(got, []int64{500, 1000, 2000, 4000}) {
		t.Errorf("demo false = %v", got)
	}
	got := allowedSpeedsPermille(st, true)
	if !slices.Equal(got, []int64{500, 1000, 2000, 4000, 8000}) {
		t.Errorf("demo true = %v", got)
	}
	got[0] = 1
	if st.SpeedAllowedPermille[0] != 500 {
		t.Error("returned slice shares the stage's slice")
	}
	got = allowedSpeedsPermille(st, false)
	got[0] = 1
	if st.SpeedAllowedPermille[0] != 500 {
		t.Error("returned slice shares the stage's slice (demo false)")
	}
	with8 := &stage.Stage{SpeedAllowedPermille: []int64{1000, 8000}}
	if got := allowedSpeedsPermille(with8, true); !slices.Equal(got, []int64{1000, 8000}) {
		t.Errorf("stage with 8000 = %v", got)
	}

	allowed := allowedSpeedsPermille(st, false)
	for _, tt := range []struct {
		v    float64
		want int64
	}{{0.5, 500}, {1, 1000}, {2, 2000}, {4, 4000}} {
		if p, ok := parseSpeedPermille(tt.v, allowed); !ok || p != tt.want {
			t.Errorf("parseSpeedPermille(%v) = (%d, %v), want (%d, true)", tt.v, p, ok, tt.want)
		}
	}
	for _, v := range []float64{3, 0, -1, 0.5004, math.NaN(), math.Inf(1), math.Inf(-1), 8, 1e300} {
		if p, ok := parseSpeedPermille(v, allowed); ok {
			t.Errorf("parseSpeedPermille(%v) = (%d, true), want false", v, p)
		}
	}
	if p, ok := parseSpeedPermille(8, allowedSpeedsPermille(st, true)); !ok || p != 8000 {
		t.Errorf("parseSpeedPermille(8) with demo = (%d, %v)", p, ok)
	}
}

// LP-4: skipTarget。
func TestSkipTarget(t *testing.T) {
	t.Parallel()
	if got, ok := skipTarget(1000, 31000, true); !ok || got != 31000 {
		t.Errorf("(1000, 31000, true) = (%d, %v)", got, ok)
	}
	if got, ok := skipTarget(1000, 1500, true); !ok || got != 1500 {
		t.Errorf("(1000, 1500, true) = (%d, %v)", got, ok)
	}
	if _, ok := skipTarget(1000, 31001, true); ok {
		t.Error("(1000, 31001, true) = true")
	}
	if _, ok := skipTarget(1000, 2000, false); ok {
		t.Error("(1000, 2000, false) = true")
	}
	if _, ok := skipTarget(0, 0, false); ok {
		t.Error("(0, 0, false) = true")
	}
}
