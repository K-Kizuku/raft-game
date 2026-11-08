package sim

import "github.com/K-Kizuku/raft-game/server/internal/game"

// RoundResult はラウンドの結果（プロトコル §5.5 の RoundResult のうち T-203 の分）。統計のほかの欄は TechStats、
// 「今日の出来事」の材料は StoryFacts が返す（T-208）。
type RoundResult struct {
	Score ResultScore `json:"score"`
	Stars int         `json:"stars"`
	Stats ResultStats `json:"stats"`
}

// ResultScore はスコアの合計と内訳。
type ResultScore struct {
	Total     int64          `json:"total"`
	Breakdown ScoreBreakdown `json:"breakdown"`
}

// ScoreBreakdown はスコアの内訳。Expired・FerryCost は 0 以下（プロトコル §5.5 の例のとおり負の数）。
// Requests + Expired + FerryCost + Fairness == Total。
type ScoreBreakdown struct {
	Requests  int64 `json:"requests"`
	Expired   int64 `json:"expired"`
	FerryCost int64 `json:"ferryCost"`
	Fairness  int64 `json:"fairness"`
}

// ResultStats は依頼の結果の数。
type ResultStats struct {
	Succeeded int64 `json:"succeeded"`
	Rejected  int64 `json:"rejected"`
	Expired   int64 `json:"expired"`
}

// Result はラウンドの結果を返す。PhaseEnded で採点するときだけ true。状態・イベント・ハッシュを変えない。
// ★はラウンドの終わりの値で判定する。重要な資源が 1 つもないステージは「すべて確定」とみなす。
func (e *Engine) Result() (RoundResult, bool) {
	if !e.score.on || e.phase != PhaseEnded {
		return RoundResult{}, false
	}
	b := &e.score
	all := true
	for _, s := range b.secured {
		all = all && s
	}
	return RoundResult{
		Score: ResultScore{Total: b.total, Breakdown: b.breakdown},
		Stars: game.Stars(e.cfg.Score, b.successes, all, b.total),
		Stats: ResultStats{Succeeded: b.successes, Rejected: b.rejected, Expired: b.expired},
	}, true
}
