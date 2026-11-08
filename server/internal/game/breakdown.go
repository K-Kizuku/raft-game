package game

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
)

// BreakdownDef は台本の故障 1 つ（events[] の kind: breakdown）。値はすべてステージ定義から渡す。
type BreakdownDef struct {
	WindowStart int64 // at と書いたときは WindowEnd と同じ
	WindowEnd   int64
	DurationMs  int64
	Ferry       string // 船の ID。"" はシードで選ぶ（ステージ定義の any）
}

// Breakdown は時刻と船が決まった故障。
type Breakdown struct {
	At         int64
	DurationMs int64
	Ferry      string
}

// ErrInvalidBreakdown は故障の定義の誤り。PlanBreakdowns のエラーはこれを包む。
var ErrInvalidBreakdown = errors.New("game: invalid breakdown")

// PlanBreakdowns は、定義の順に時刻と船を決める。ferries は船の ID（ステージ定義の順）。
// すべての定義を検査してから乱数を引く。引く順は定義ごとに「時刻（WindowEnd > WindowStart のときだけ Int64N(幅 + 1)）→
// 船（Ferry が "" のときだけ IntN(len(ferries))）」。defs が空なら nil を返し、rng を使わない。
func PlanBreakdowns(defs []BreakdownDef, ferries []string, durationMs int64, rng *rand.Rand) ([]Breakdown, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	for i, d := range defs {
		if err := checkBreakdown(i, d, ferries, durationMs); err != nil {
			return nil, err
		}
	}
	out := make([]Breakdown, len(defs))
	for i, d := range defs {
		at := d.WindowStart
		if d.WindowEnd > d.WindowStart {
			at += rng.Int64N(d.WindowEnd - d.WindowStart + 1)
		}
		f := d.Ferry
		if f == "" {
			f = ferries[rng.IntN(len(ferries))]
		}
		out[i] = Breakdown{At: at, DurationMs: d.DurationMs, Ferry: f}
	}
	return out, nil
}

// checkBreakdown は defs[i] を検査する。誤りは ErrInvalidBreakdown を包んで返す（最初の 1 件だけ）。
func checkBreakdown(i int, d BreakdownDef, ferries []string, durationMs int64) error {
	p := fmt.Sprintf("breakdowns[%d]", i)
	switch {
	case d.WindowStart < 0:
		return fmt.Errorf("%w: %s.windowStart: %d < 0", ErrInvalidBreakdown, p, d.WindowStart)
	case d.WindowEnd < d.WindowStart:
		return fmt.Errorf("%w: %s.windowEnd: %d < windowStart %d", ErrInvalidBreakdown, p, d.WindowEnd, d.WindowStart)
	case d.WindowEnd > durationMs:
		return fmt.Errorf("%w: %s.windowEnd: %d > durationMs %d", ErrInvalidBreakdown, p, d.WindowEnd, durationMs)
	case d.DurationMs < 1:
		return fmt.Errorf("%w: %s.durationMs: %d < 1", ErrInvalidBreakdown, p, d.DurationMs)
	case len(ferries) == 0:
		return fmt.Errorf("%w: %s: no ferries", ErrInvalidBreakdown, p)
	case d.Ferry != "" && !slices.Contains(ferries, d.Ferry):
		return fmt.Errorf("%w: %s.ferry: unknown ferry %q", ErrInvalidBreakdown, p, d.Ferry)
	}
	return nil
}
