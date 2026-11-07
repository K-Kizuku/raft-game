package game

import (
	"errors"
	"fmt"
)

// SatisfactionMax は満足度の上限（ゲームデザイン §5.2 の 0〜100）。範囲の定義で、調整する値ではない
// （stage の検査の上限 maxSatisfaction と同じ）。下限は 0。
const SatisfactionMax = 100

// ScoreConfig は採点・満足度・クリア判定の設定（ステージ定義の scoring.*・satisfaction.*）。既定値を持たない。
// ゼロ値は「採点しない」（Enabled が false。sim はスコアと満足度のイベントを出さない）。
type ScoreConfig struct {
	PointsNormal            int64 // 通常の依頼が期限内に確定
	PointsCritical          int64 // 重要な依頼が期限内に確定
	PointsLate              int64 // 期限を過ぎてから確定
	PointsRejected          int64 // 予約失敗
	ExpiredPenalty          int64 // 期限切れで引く点（0 以上）
	FerryCostMsPerPoint     int64 // 運航コストの分母
	FairnessPerSatisfaction int64 // 公平ボーナスの係数
	TargetSuccesses         int64 // ★1 の確定数
	TargetScore3            int64 // ★3 のスコア
	SatInitial              int64 // 満足度の初期値
	SatSucceeded            int64 // 確定で足す
	SatRejectedPenalty      int64 // 予約失敗で引く
	SatExpiredPenalty       int64 // 期限切れで引く
	SilenceAfterMs          int64 // 音信不通とみなすまでの時間
	SilenceEveryMs          int64 // 音信不通の減点の間隔
	SilenceAmount           int64 // 音信不通の 1 回の減点
}

// ErrInvalidScoreConfig は ScoreConfig の誤り。Validate のエラーはこれを包む。
var ErrInvalidScoreConfig = errors.New("game: invalid score config")

// Enabled はゼロ値でないかを返す。
func (c ScoreConfig) Enabled() bool { return c != ScoreConfig{} }

// Validate は値を検査する。ゼロ値は nil（採点しない）。誤りは最初の 1 件を、欄の名前が分かる文で
// fmt.Errorf("%w: …", ErrInvalidScoreConfig, …) にして返す。
func (c ScoreConfig) Validate() error {
	if !c.Enabled() {
		return nil
	}
	type field struct {
		name string
		v    int64
	}
	for _, f := range []field{
		{"PointsNormal", c.PointsNormal},
		{"PointsCritical", c.PointsCritical},
		{"PointsLate", c.PointsLate},
		{"PointsRejected", c.PointsRejected},
		{"ExpiredPenalty", c.ExpiredPenalty},
	} {
		if f.v < 0 {
			return fmt.Errorf("%w: %s %d < 0", ErrInvalidScoreConfig, f.name, f.v)
		}
	}
	if c.FerryCostMsPerPoint < 1 {
		return fmt.Errorf("%w: FerryCostMsPerPoint %d < 1", ErrInvalidScoreConfig, c.FerryCostMsPerPoint)
	}
	if c.FairnessPerSatisfaction < 0 {
		return fmt.Errorf("%w: FairnessPerSatisfaction %d < 0", ErrInvalidScoreConfig, c.FairnessPerSatisfaction)
	}
	if c.TargetSuccesses < 1 {
		return fmt.Errorf("%w: TargetSuccesses %d < 1", ErrInvalidScoreConfig, c.TargetSuccesses)
	}
	if c.TargetScore3 < 0 {
		return fmt.Errorf("%w: TargetScore3 %d < 0", ErrInvalidScoreConfig, c.TargetScore3)
	}
	for _, f := range []field{
		{"SatInitial", c.SatInitial},
		{"SatSucceeded", c.SatSucceeded},
		{"SatRejectedPenalty", c.SatRejectedPenalty},
		{"SatExpiredPenalty", c.SatExpiredPenalty},
		{"SilenceAmount", c.SilenceAmount},
	} {
		if f.v < 0 || f.v > SatisfactionMax {
			return fmt.Errorf("%w: %s %d is not in 0..%d", ErrInvalidScoreConfig, f.name, f.v, SatisfactionMax)
		}
	}
	if c.SilenceAfterMs < 1 {
		return fmt.Errorf("%w: SilenceAfterMs %d < 1", ErrInvalidScoreConfig, c.SilenceAfterMs)
	}
	if c.SilenceEveryMs < 1 {
		return fmt.Errorf("%w: SilenceEveryMs %d < 1", ErrInvalidScoreConfig, c.SilenceEveryMs)
	}
	return nil
}

// RequestPoints は依頼 1 件の得点（ゲームデザイン §5.1）。ok は依頼元の台帳の結果が ok か。
func RequestPoints(c ScoreConfig, critical, onTime, ok bool) int64 {
	switch {
	case !ok:
		return c.PointsRejected
	case !onTime:
		return c.PointsLate
	case critical:
		return c.PointsCritical
	default:
		return c.PointsNormal
	}
}

// VoyageCost は 1 回の航行の運航コスト ceil(travelMs ÷ FerryCostMsPerPoint)（travelMs >= 0）。
// travelMs <= 0 なら 0。FerryCostMsPerPoint は Validate で 1 以上を確かめておく。
func VoyageCost(c ScoreConfig, travelMs int64) int64 {
	if travelMs <= 0 {
		return 0
	}
	return (travelMs + c.FerryCostMsPerPoint - 1) / c.FerryCostMsPerPoint
}

// FairnessBonus は公平ボーナス FairnessPerSatisfaction × minSatisfaction。
func FairnessBonus(c ScoreConfig, minSatisfaction int64) int64 {
	return c.FairnessPerSatisfaction * minSatisfaction
}

// ClampSatisfaction は v を 0..SatisfactionMax に収める。
func ClampSatisfaction(v int64) int64 {
	return min(max(v, 0), SatisfactionMax)
}

// Stars はクリアの段階（0〜3。ゲームデザイン §5.3）。
//
//	successes < TargetSuccesses → 0
//	allCriticalSecured でない   → 1
//	total < TargetScore3        → 2
//	そのほか                     → 3
func Stars(c ScoreConfig, successes int64, allCriticalSecured bool, total int64) int {
	switch {
	case successes < c.TargetSuccesses:
		return 0
	case !allCriticalSecured:
		return 1
	case total < c.TargetScore3:
		return 2
	default:
		return 3
	}
}
