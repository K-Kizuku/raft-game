package stage

import (
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// SP-4 SimConfig(alpha).Score。
func TestSimConfigScore(t *testing.T) {
	t.Parallel()
	st := parseStage(t, "alpha.yaml", readTestdata(t, "stages/alpha.yaml"))
	cfg, err := SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	want := game.ScoreConfig{
		PointsNormal: 100, PointsCritical: 300, PointsLate: 30, PointsRejected: 0, ExpiredPenalty: 50,
		FerryCostMsPerPoint: 2000, FairnessPerSatisfaction: 5, TargetSuccesses: 2, TargetScore3: 500,
		SatInitial: 60, SatSucceeded: 10, SatRejectedPenalty: 3, SatExpiredPenalty: 10,
		SilenceAfterMs: 30000, SilenceEveryMs: 10000, SilenceAmount: 2,
	}
	if cfg.Score != want {
		t.Errorf("SimConfig(alpha).Score = %+v, want %+v", cfg.Score, want)
	}
	if err := cfg.Score.Validate(); err != nil {
		t.Errorf("Score.Validate() = %v", err)
	}
	if cfg.Score.EndOnStar2 {
		t.Errorf("SimConfig(alpha).Score.EndOnStar2 = true, want false（alpha に demo がない）")
	}

	// beta の値が取り違えなく入る。
	beta := parseStage(t, "beta.yaml", readTestdata(t, "stages/beta.yaml"))
	wantBeta := game.ScoreConfig{
		PointsNormal: 80, PointsCritical: 250, PointsLate: 20, PointsRejected: 1, ExpiredPenalty: 40,
		FerryCostMsPerPoint: 1500, FairnessPerSatisfaction: 4, TargetSuccesses: 3, TargetScore3: 800,
		SatInitial: 50, SatSucceeded: 8, SatRejectedPenalty: 2, SatExpiredPenalty: 9,
		SilenceAfterMs: 25000, SilenceEveryMs: 5000, SilenceAmount: 1,
		EndOnStar2: true, // beta.yaml の demo.endOnAllCriticalSecured: true（T-305b）
	}
	if got := scoreConfig(beta); got != wantBeta {
		t.Errorf("scoreConfig(beta) = %+v, want %+v", got, wantBeta)
	}
}

// SP-5 SP-4 の設定で Engine がラウンドの終わりまで動き、結果を返す。
func TestSimConfigScoreEngine(t *testing.T) {
	t.Parallel()
	st := parseStage(t, "alpha.yaml", readTestdata(t, "stages/alpha.yaml"))
	cfg, err := SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	e, err := sim.New(cfg, 20261101, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("sim.New: %v", err)
	}
	if ack := e.Apply(sim.Command{Type: sim.CmdGameStart}); !ack.OK {
		t.Fatalf("Apply(game.start) = %+v", ack)
	}
	e.RunUntil(180000)
	r, ok := e.Result()
	if !ok {
		t.Fatalf("Result() = false in phase %s", e.Phase())
	}
	if r.Stars < 0 || r.Stars > 3 {
		t.Errorf("Stars = %d", r.Stars)
	}
	b := r.Score.Breakdown
	if b.Requests+b.Expired+b.FerryCost+b.Fairness != r.Score.Total {
		t.Errorf("breakdown %+v does not add up to %d", b, r.Score.Total)
	}
}
