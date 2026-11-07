package game

import (
	"errors"
	"testing"
)

// validScore は sim の scoreTestConfig().Score と同じ値（テストの中の値）。
func validScore() ScoreConfig {
	return ScoreConfig{
		PointsNormal: 100, PointsCritical: 300, PointsLate: 30, PointsRejected: 0, ExpiredPenalty: 50,
		FerryCostMsPerPoint: 2000, FairnessPerSatisfaction: 5, TargetSuccesses: 1, TargetScore3: 500,
		SatInitial: 60, SatSucceeded: 10, SatRejectedPenalty: 3, SatExpiredPenalty: 10,
		SilenceAfterMs: 1000000, SilenceEveryMs: 10000, SilenceAmount: 2,
	}
}

func TestScoreConfigValidate(t *testing.T) { // G-1
	t.Parallel()
	with := func(mod func(*ScoreConfig)) ScoreConfig {
		c := validScore()
		mod(&c)
		return c
	}
	ok := []struct {
		name string
		cfg  ScoreConfig
	}{
		{"zero", ScoreConfig{}},
		{"valid", validScore()},
		{"SatInitial 100", with(func(c *ScoreConfig) { c.SatInitial = 100 })},
		{"TargetScore3 0", with(func(c *ScoreConfig) { c.TargetScore3 = 0 })},
		{"PointsRejected 0", with(func(c *ScoreConfig) { c.PointsRejected = 0 })},
	}
	for _, tc := range ok {
		if err := tc.cfg.Validate(); err != nil {
			t.Errorf("%s: Validate = %v, want nil", tc.name, err)
		}
	}
	bad := []struct {
		name string
		mod  func(*ScoreConfig)
	}{
		{"PointsNormal -1", func(c *ScoreConfig) { c.PointsNormal = -1 }},
		{"ExpiredPenalty -1", func(c *ScoreConfig) { c.ExpiredPenalty = -1 }},
		{"FerryCostMsPerPoint 0", func(c *ScoreConfig) { c.FerryCostMsPerPoint = 0 }},
		{"FairnessPerSatisfaction -1", func(c *ScoreConfig) { c.FairnessPerSatisfaction = -1 }},
		{"TargetSuccesses 0", func(c *ScoreConfig) { c.TargetSuccesses = 0 }},
		{"TargetScore3 -1", func(c *ScoreConfig) { c.TargetScore3 = -1 }},
		{"SatInitial 101", func(c *ScoreConfig) { c.SatInitial = 101 }},
		{"SatSucceeded -1", func(c *ScoreConfig) { c.SatSucceeded = -1 }},
		{"SatExpiredPenalty 101", func(c *ScoreConfig) { c.SatExpiredPenalty = 101 }},
		{"SilenceAmount 101", func(c *ScoreConfig) { c.SilenceAmount = 101 }},
		{"SilenceAfterMs 0", func(c *ScoreConfig) { c.SilenceAfterMs = 0 }},
		{"SilenceEveryMs 0", func(c *ScoreConfig) { c.SilenceEveryMs = 0 }},
	}
	for _, tc := range bad {
		err := with(tc.mod).Validate()
		if !errors.Is(err, ErrInvalidScoreConfig) {
			t.Errorf("%s: Validate = %v, want ErrInvalidScoreConfig", tc.name, err)
		}
	}
}

func TestScoreConfigEnabled(t *testing.T) { // G-2
	t.Parallel()
	if (ScoreConfig{}).Enabled() {
		t.Error("zero value is enabled")
	}
	for i, c := range []ScoreConfig{
		{PointsNormal: 1}, {PointsRejected: 1}, {ExpiredPenalty: 1}, {FerryCostMsPerPoint: 1},
		{TargetScore3: 1}, {SatInitial: 1}, {SilenceAmount: 1},
	} {
		if !c.Enabled() {
			t.Errorf("%d: %+v is not enabled", i, c)
		}
	}
}

func TestRequestPoints(t *testing.T) { // G-3
	t.Parallel()
	c := validScore()
	c7 := validScore()
	c7.PointsRejected = 7
	tests := []struct {
		name                 string
		cfg                  ScoreConfig
		critical, onTime, ok bool
		want                 int64
	}{
		{"rejected", c, false, true, false, 0},
		{"critical on time", c, true, true, true, 300},
		{"normal on time", c, false, true, true, 100},
		{"normal late", c, false, false, true, 30},
		{"critical late", c, true, false, true, 30},
		{"normal on time rejected", c, false, true, false, 0},
		{"critical late rejected 7", c7, true, false, false, 7},
	}
	for _, tc := range tests {
		if got := RequestPoints(tc.cfg, tc.critical, tc.onTime, tc.ok); got != tc.want {
			t.Errorf("%s: RequestPoints = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestVoyageCost(t *testing.T) { // G-4
	t.Parallel()
	c := ScoreConfig{FerryCostMsPerPoint: 2000}
	for _, tc := range []struct{ in, want int64 }{
		{0, 0}, {1, 1}, {2000, 1}, {2001, 2}, {3750, 2}, {4500, 3}, {-5, 0},
	} {
		if got := VoyageCost(c, tc.in); got != tc.want {
			t.Errorf("VoyageCost(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFairnessBonus(t *testing.T) { // G-5
	t.Parallel()
	c := ScoreConfig{FairnessPerSatisfaction: 5}
	for _, tc := range []struct{ in, want int64 }{{0, 0}, {42, 210}, {100, 500}} {
		if got := FairnessBonus(c, tc.in); got != tc.want {
			t.Errorf("FairnessBonus(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestClampSatisfaction(t *testing.T) { // G-6
	t.Parallel()
	for _, tc := range []struct{ in, want int64 }{{-5, 0}, {0, 0}, {50, 50}, {100, 100}, {105, 100}} {
		if got := ClampSatisfaction(tc.in); got != tc.want {
			t.Errorf("ClampSatisfaction(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestStars(t *testing.T) { // G-7
	t.Parallel()
	c := ScoreConfig{TargetSuccesses: 4, TargetScore3: 900}
	for _, tc := range []struct {
		successes int64
		critical  bool
		total     int64
		want      int
	}{
		{3, true, 1000, 0},
		{4, false, 1000, 1},
		{4, true, 899, 2},
		{4, true, 900, 3},
		{10, false, 5000, 1},
	} {
		if got := Stars(c, tc.successes, tc.critical, tc.total); got != tc.want {
			t.Errorf("Stars(%d, %v, %d) = %d, want %d", tc.successes, tc.critical, tc.total, got, tc.want)
		}
	}
}
