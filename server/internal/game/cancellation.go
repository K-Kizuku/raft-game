package game

import (
	"cmp"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
)

// CancellationDef は欠航の定義 1 つ（events[] の kind: cancellation。FR-EVT-08）。値はすべてステージ定義から渡す。
type CancellationDef struct {
	Island              uint64 // 島のノード ID（1..N）
	ProbabilityPermille int64  // 区間ごとの欠航の確率（千分率、0..1000）
	SlotMs              int64  // 区間の長さ
	DurationMs          int64  // 止める長さ
}

// Cancellation は時刻の決まった欠航。
type Cancellation struct {
	Island uint64
	Start  int64 // 止め始める時刻（k × SlotMs）
	End    int64 // Start + DurationMs（この時刻から出航できる）
}

// ErrInvalidCancellation は欠航の定義の誤り。PlanCancellations のエラーはこれを包む。
var ErrInvalidCancellation = errors.New("game: invalid cancellation")

// maxCancellationSlots は、定義 1 つで乱数を引く回数（区間の境目の数）の上限。
const maxCancellationSlots = 10000

// PlanCancellations は、すべての定義を検査してから、定義の順・区間の順（k = 1, 2, …、k×SlotMs < durationMs）に
// rng.IntN(1000) を 1 回ずつ引き、当たった欠航を返す（データ仕様 §4.4）。確率が 0 でも 1000 でも引く。
// k = 0（ラウンドの始め）は引かない。返す列は Start の昇順で、同じ Start は定義の順（安定な並べ替え）。
// defs が空なら nil を返し、rng を使わない。当たりが 0 件なら nil。
func PlanCancellations(defs []CancellationDef, islands int, durationMs int64, rng *rand.Rand) ([]Cancellation, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	for i, d := range defs {
		if err := checkCancellation(i, d, islands, durationMs); err != nil {
			return nil, err
		}
	}
	var out []Cancellation
	for _, d := range defs {
		for t := d.SlotMs; t < durationMs; t += d.SlotMs {
			if int64(rng.IntN(1000)) < d.ProbabilityPermille {
				out = append(out, Cancellation{Island: d.Island, Start: t, End: t + d.DurationMs})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Cancellation) int { return cmp.Compare(a.Start, b.Start) })
	return out, nil
}

// cancellationSlots は区間の境目の数（k×slotMs < durationMs を満たす k >= 1 の数）。slotMs >= 1 のときだけ呼ぶ。
func cancellationSlots(slotMs, durationMs int64) int64 {
	if durationMs <= 0 {
		return 0
	}
	return (durationMs - 1) / slotMs
}

// checkCancellation は defs[i] を検査する。誤りは ErrInvalidCancellation を包んで返す（最初の 1 件だけ）。
func checkCancellation(i int, d CancellationDef, islands int, durationMs int64) error {
	p := fmt.Sprintf("cancellations[%d]", i)
	n := uint64(max(islands, 0))
	switch {
	case d.Island < 1 || d.Island > n:
		return fmt.Errorf("%w: %s.island: %d is not in 1..%d", ErrInvalidCancellation, p, d.Island, n)
	case d.ProbabilityPermille < 0 || d.ProbabilityPermille > 1000:
		return fmt.Errorf("%w: %s.probabilityPermille: %d is not in 0..1000", ErrInvalidCancellation, p, d.ProbabilityPermille)
	case d.SlotMs < 1:
		return fmt.Errorf("%w: %s.slotMs: %d < 1", ErrInvalidCancellation, p, d.SlotMs)
	case d.DurationMs < 1:
		return fmt.Errorf("%w: %s.durationMs: %d < 1", ErrInvalidCancellation, p, d.DurationMs)
	}
	if s := cancellationSlots(d.SlotMs, durationMs); s > maxCancellationSlots {
		return fmt.Errorf("%w: %s.slotMs: %d slots > %d", ErrInvalidCancellation, p, s, maxCancellationSlots)
	}
	return nil
}
