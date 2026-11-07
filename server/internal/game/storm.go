package game

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
)

// StormDef は台本の嵐 1 つ（ステージ定義の events[] の kind: storm）。値はすべてステージ定義から渡す。
type StormDef struct {
	WindowStart int64       // 開始の時刻の範囲の始め（at と書いたときは WindowEnd と同じ）
	WindowEnd   int64       // 開始の時刻の範囲の終わり（含む）
	DurationMs  int64       // 遮断する長さ
	Routes      [][2]uint64 // 遮断する航路（ノード ID の組。書いた順・向きのまま）
	ForecastMs  int64       // 開始の何 ms 前に予報するか。0 なら予報しない
}

// Storm は時刻が決まった嵐。時刻はゲーム内時間のミリ秒。
type Storm struct {
	Start       int64       // 遮断の始め
	End         int64       // 遮断の終わり（Start + DurationMs。この時刻から出航できる）
	HasForecast bool        // ForecastMs > 0
	ForecastAt  int64       // 予報の時刻（Start − ForecastMs）。HasForecast でなければ 0
	Routes      [][2]uint64 // StormDef.Routes の複製
}

// ErrInvalidStorm は嵐の定義の誤り。PlanStorms のエラーはこれを包む。
var ErrInvalidStorm = errors.New("game: invalid storm")

// CloneStormDefs は Routes も複製した列を返す。nil なら nil。
func CloneStormDefs(defs []StormDef) []StormDef {
	if defs == nil {
		return nil
	}
	out := make([]StormDef, len(defs))
	for i, d := range defs {
		d.Routes = slices.Clone(d.Routes)
		out[i] = d
	}
	return out
}

// PlanStorms は、ラウンドのすべての嵐の時刻を決める。islands は島の数 N、durationMs はラウンドの長さ。
// 返す列は defs と同じ順・同じ長さ。defs が空なら nil。rng は幅のある嵐の数だけ引く。
//
// すべての定義を検査してから乱数を引く。開始の時刻は、定義の順に、WindowEnd > WindowStart のものだけ
// WindowStart + rng.Int64N(WindowEnd − WindowStart + 1) で決める（両端を含む一様。設計書にない判断）。
// at（WindowStart == WindowEnd）は乱数を引かない。
func PlanStorms(defs []StormDef, islands int, durationMs int64, rng *rand.Rand) ([]Storm, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	for i, d := range defs {
		if err := checkStorm(i, d, islands, durationMs); err != nil {
			return nil, err
		}
	}
	out := make([]Storm, len(defs))
	for i, d := range defs {
		start := d.WindowStart
		if d.WindowEnd > d.WindowStart {
			start += rng.Int64N(d.WindowEnd - d.WindowStart + 1)
		}
		s := Storm{
			Start:  start,
			End:    start + d.DurationMs,
			Routes: slices.Clone(d.Routes),
		}
		if d.ForecastMs > 0 {
			s.HasForecast = true
			s.ForecastAt = start - d.ForecastMs
		}
		out[i] = s
	}
	return out, nil
}

// checkStorm は defs[i] を検査する。誤りは ErrInvalidStorm を包んで返す（最初の 1 件だけ）。
func checkStorm(i int, d StormDef, islands int, durationMs int64) error {
	p := fmt.Sprintf("storms[%d]", i)
	switch {
	case d.WindowStart < 0:
		return fmt.Errorf("%w: %s.windowStart: %d < 0", ErrInvalidStorm, p, d.WindowStart)
	case d.WindowEnd < d.WindowStart:
		return fmt.Errorf("%w: %s.windowEnd: %d < windowStart %d", ErrInvalidStorm, p, d.WindowEnd, d.WindowStart)
	case d.WindowEnd > durationMs:
		return fmt.Errorf("%w: %s.windowEnd: %d > durationMs %d", ErrInvalidStorm, p, d.WindowEnd, durationMs)
	case d.DurationMs < 1:
		return fmt.Errorf("%w: %s.durationMs: %d < 1", ErrInvalidStorm, p, d.DurationMs)
	case len(d.Routes) == 0:
		return fmt.Errorf("%w: %s.routes: empty", ErrInvalidStorm, p)
	}
	n := uint64(max(islands, 0))
	for j, r := range d.Routes {
		rp := fmt.Sprintf("%s.routes[%d]", p, j)
		for _, id := range r {
			if id < 1 || id > n {
				return fmt.Errorf("%w: %s: island %d is not in 1..%d", ErrInvalidStorm, rp, id, n)
			}
		}
		if r[0] == r[1] {
			return fmt.Errorf("%w: %s: same island %d", ErrInvalidStorm, rp, r[0])
		}
		for k, q := range d.Routes[:j] {
			if (q[0] == r[0] && q[1] == r[1]) || (q[0] == r[1] && q[1] == r[0]) {
				return fmt.Errorf("%w: %s: same route as routes[%d]", ErrInvalidStorm, rp, k)
			}
		}
	}
	if d.ForecastMs < 0 || d.ForecastMs > d.WindowStart {
		return fmt.Errorf("%w: %s.forecastMs: %d is not in 0..%d", ErrInvalidStorm, p, d.ForecastMs, d.WindowStart)
	}
	return nil
}
