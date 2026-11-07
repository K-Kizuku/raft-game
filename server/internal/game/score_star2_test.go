package game

import (
	"errors"
	"testing"
)

// GS-1 ReachedStar2 が Stars(...) >= 2 と一致する（T-305b）。
func TestReachedStar2MatchesStars(t *testing.T) {
	t.Parallel()
	c := validScoreConfigForStar2()
	for _, successes := range []int64{c.TargetSuccesses - 1, c.TargetSuccesses, c.TargetSuccesses + 1} {
		for _, all := range []bool{true, false} {
			for _, total := range []int64{c.TargetScore3 - 1, c.TargetScore3, c.TargetScore3 + 1} {
				want := Stars(c, successes, all, total) >= 2
				if got := ReachedStar2(c, successes, all); got != want {
					t.Errorf("ReachedStar2(successes %d, all %v) = %v, want %v（total %d の Stars >= 2）",
						successes, all, got, want, total)
				}
			}
		}
	}
}

// GS-2 EndOnStar2 だけが真の設定は Enabled が真で Validate が誤り。採点の値をそろえれば nil。
func TestEndOnStar2Validate(t *testing.T) {
	t.Parallel()
	only := ScoreConfig{EndOnStar2: true}
	if !only.Enabled() {
		t.Errorf("ScoreConfig{EndOnStar2: true}.Enabled() = false, want true")
	}
	if err := only.Validate(); !errors.Is(err, ErrInvalidScoreConfig) {
		t.Errorf("ScoreConfig{EndOnStar2: true}.Validate() = %v, want ErrInvalidScoreConfig", err)
	}
	c := validScoreConfigForStar2()
	c.EndOnStar2 = true
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

// validScoreConfigForStar2 は Validate が nil を返す採点の設定。
func validScoreConfigForStar2() ScoreConfig {
	return ScoreConfig{
		PointsNormal: 100, PointsCritical: 300, PointsLate: 30, PointsRejected: 0, ExpiredPenalty: 50,
		FerryCostMsPerPoint: 2000, FairnessPerSatisfaction: 5, TargetSuccesses: 3, TargetScore3: 500,
		SatInitial: 60, SatSucceeded: 10, SatRejectedPenalty: 3, SatExpiredPenalty: 10,
		SilenceAfterMs: 30000, SilenceEveryMs: 10000, SilenceAmount: 2,
	}
}
