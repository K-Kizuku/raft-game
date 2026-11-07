package stage

import "github.com/K-Kizuku/raft-game/server/internal/game"

// scoreConfig は st の scoring.*・satisfaction.* から採点の設定を作る（データ仕様 §5.6）。
func scoreConfig(st *Stage) game.ScoreConfig {
	sc, sa := st.Scoring, st.Satisfaction
	return game.ScoreConfig{
		PointsNormal:            sc.PointsNormal,
		PointsCritical:          sc.PointsCritical,
		PointsLate:              sc.PointsLate,
		PointsRejected:          sc.PointsRejected,
		ExpiredPenalty:          sc.PointsExpiredPenalty,
		FerryCostMsPerPoint:     sc.FerryCostMsPerPoint,
		FairnessPerSatisfaction: sc.FairnessPerSatisfaction,
		TargetSuccesses:         sc.TargetSuccesses,
		TargetScore3:            sc.TargetScore3,
		SatInitial:              sa.Initial,
		SatSucceeded:            sa.Succeeded,
		SatRejectedPenalty:      sa.RejectedPenalty,
		SatExpiredPenalty:       sa.ExpiredPenalty,
		SilenceAfterMs:          sa.SilenceAfterMs,
		SilenceEveryMs:          sa.SilencePenaltyEveryMs,
		SilenceAmount:           sa.SilencePenaltyAmount,
	}
}
