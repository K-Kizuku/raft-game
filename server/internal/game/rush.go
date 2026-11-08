package game

import (
	"errors"
	"fmt"
	"math/rand/v2"
)

// RushDef は台本のラッシュ 1 つ（events[] の kind: rush）。値はすべてステージ定義から渡す。
type RushDef struct {
	WindowStart int64 // at と書いたときは WindowEnd と同じ
	WindowEnd   int64
	Requests    int   // 発生させる依頼の数（1 以上）
	ForecastMs  int64 // 0 なら予報しない
}

// Rush は時刻と中身が決まったラッシュ。
type Rush struct {
	At          int64
	ForecastAt  int64 // HasForecast のときだけ意味がある（At − ForecastMs）
	HasForecast bool
	Requests    []ScriptRequest // At はすべて Rush.At。決めた順
	Islands     []uint64        // 依頼が発生する島（重複なし・ID の昇順）
}

// ErrInvalidRush はラッシュの定義の誤り。PlanRush のエラーはこれを包む。
var ErrInvalidRush = errors.New("game: invalid rush")

// PlanRush は、定義の順に時刻・島・種類を決める。weights は島ごとの依頼の重み（Random.Weights と同じ。添字 = ノード ID − 1）、
// kinds はランダムな依頼の種類と重み（Random.Kinds）。defs が空なら nil を返し、rng を使わない。
//
// すべての定義を検査してから乱数を引く。引く順は定義ごとに「時刻（WindowEnd > WindowStart のときだけ Int64N(幅 + 1)）→
// 依頼 1 件ごとに、島（Int64N(まだ選んでいない島の重みの合計)）→ 種類（Int64N(ΣWeight)）」。
// 島は、まだ選んでいない島から重みに比例して選ぶ（ID の昇順に重みを累積し、引いた値より大きくなった最初の島）。
// 重みのある島を全部選び終えたら、全部の島に戻してまた選ぶ。重み 0 の島は選ばない。
func PlanRush(defs []RushDef, weights []int64, kinds []RandomKind, durationMs int64, rng *rand.Rand) ([]Rush, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	for i, d := range defs {
		if err := checkRush(i, d, weights, kinds, durationMs); err != nil {
			return nil, err
		}
	}
	var wsum, ksum int64
	for _, w := range weights {
		wsum += w
	}
	for _, k := range kinds {
		ksum += k.Weight
	}
	out := make([]Rush, len(defs))
	for i, d := range defs {
		at := d.WindowStart
		if d.WindowEnd > d.WindowStart {
			at += rng.Int64N(d.WindowEnd - d.WindowStart + 1)
		}
		r := Rush{At: at, Requests: make([]ScriptRequest, 0, d.Requests)}
		if d.ForecastMs > 0 {
			r.HasForecast = true
			r.ForecastAt = at - d.ForecastMs
		}
		chosen := make([]bool, len(weights)) // この巡で選んだ島
		used := make([]bool, len(weights))   // このラッシュで一度でも選んだ島
		remain := wsum
		for range d.Requests {
			if remain == 0 {
				clear(chosen)
				remain = wsum
			}
			k := pickIsland(weights, chosen, rng.Int64N(remain))
			chosen[k] = true
			used[k] = true
			remain -= weights[k]
			kind := kinds[pickKind(kinds, rng.Int64N(ksum))]
			r.Requests = append(r.Requests, ScriptRequest{
				At:       at,
				Island:   uint64(k + 1),
				Kind:     kind.Kind,
				Resource: kind.Resource,
			})
		}
		for k, u := range used {
			if u {
				r.Islands = append(r.Islands, uint64(k+1))
			}
		}
		out[i] = r
	}
	return out, nil
}

// pickIsland は、x（0 ≤ x < まだ選んでいない島の重みの合計）から島の添字を選ぶ。
// 選んでいない島を ID の昇順に見て、重みの累積が x より大きくなった最初の 1 つ。
func pickIsland(weights []int64, chosen []bool, x int64) int {
	var acc int64
	last := -1
	for k, w := range weights {
		if chosen[k] || w == 0 {
			continue
		}
		last = k
		acc += w
		if acc > x {
			return k
		}
	}
	return last
}

// checkRush は defs[i] を検査する。誤りは ErrInvalidRush を包んで返す（最初の 1 件だけ）。
func checkRush(i int, d RushDef, weights []int64, kinds []RandomKind, durationMs int64) error {
	p := fmt.Sprintf("rushes[%d]", i)
	switch {
	case d.WindowStart < 0:
		return fmt.Errorf("%w: %s.windowStart: %d < 0", ErrInvalidRush, p, d.WindowStart)
	case d.WindowEnd < d.WindowStart:
		return fmt.Errorf("%w: %s.windowEnd: %d < windowStart %d", ErrInvalidRush, p, d.WindowEnd, d.WindowStart)
	case d.WindowEnd > durationMs:
		return fmt.Errorf("%w: %s.windowEnd: %d > durationMs %d", ErrInvalidRush, p, d.WindowEnd, durationMs)
	case d.Requests < 1:
		return fmt.Errorf("%w: %s.requests: %d < 1", ErrInvalidRush, p, d.Requests)
	case d.ForecastMs < 0 || d.ForecastMs > d.WindowStart:
		return fmt.Errorf("%w: %s.forecastMs: %d is not in 0..%d", ErrInvalidRush, p, d.ForecastMs, d.WindowStart)
	case len(kinds) == 0:
		return fmt.Errorf("%w: %s: no request kinds", ErrInvalidRush, p)
	}
	var ksum int64
	for j, k := range kinds {
		if k.Weight < 0 {
			return fmt.Errorf("%w: %s: kinds[%d].weight %d < 0", ErrInvalidRush, p, j, k.Weight)
		}
		ksum += k.Weight
	}
	if ksum < 1 {
		return fmt.Errorf("%w: %s: kinds weights sum to %d", ErrInvalidRush, p, ksum)
	}
	var wsum int64
	for j, w := range weights {
		if w < 0 {
			return fmt.Errorf("%w: %s: weights[%d] %d < 0", ErrInvalidRush, p, j, w)
		}
		wsum += w
	}
	if wsum < 1 {
		return fmt.Errorf("%w: %s: weights sum to %d", ErrInvalidRush, p, wsum)
	}
	return nil
}
