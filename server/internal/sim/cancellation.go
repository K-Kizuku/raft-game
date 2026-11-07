package sim

import (
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/game"
)

// BlockCauseCancellation は route.blocked の cause の欠航の値（FR-EVT-08）。嵐では cause を入れない。
const BlockCauseCancellation = "cancellation"

// initCancellations は欠航の時刻と島を決めて、嵐の帳簿に欠航の印つきで入れる（New の中。initStorms の後）。
// 帳簿に載せるので、開始・終わり・route.blocked / route.unblocked・NextNotableAt・スナップショットは嵐と同じ仕組みで動く。
// 欠航の定義がなければ乱数列を作らない（ほかの用途の乱数列は変わらない。DesignDoc §9.1）。
func (e *Engine) initCancellations(seed uint64) error {
	e.cfg.Cancellations = slices.Clone(e.cfg.Cancellations)
	if len(e.cfg.Cancellations) == 0 {
		return nil
	}
	plan, err := game.PlanCancellations(e.cfg.Cancellations, e.cfg.Islands, int64(e.cfg.DurationMs), Sub(seed, StreamCancellation))
	if err != nil {
		return fmt.Errorf("sim: cancellations: %w", err)
	}
	routes := e.tr.Routes()
	for i, c := range plan {
		var pairs [][2]uint64
		for _, r := range routes {
			if r.A == c.Island || r.B == c.Island {
				pairs = append(pairs, [2]uint64{r.A, r.B})
			}
		}
		if len(pairs) == 0 {
			return fmt.Errorf("sim: cancellations: plan[%d]: island %d has no routes", i, c.Island)
		}
		if _, err := e.addBlock(game.Storm{Start: c.Start, End: c.End, Routes: pairs}, BlockCauseCancellation, c.Island); err != nil {
			return fmt.Errorf("sim: cancellations: plan[%d]: %w", i, err)
		}
	}
	return nil
}
