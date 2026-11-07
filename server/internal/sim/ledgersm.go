package sim

import (
	"fmt"

	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// ledgerSM は *ledger.Ledger を cluster.StateMachine に合わせる包み（DesignDoc §5.6）。
type ledgerSM struct{ l *ledger.Ledger }

func (s ledgerSM) Apply(index uint64, data []byte) any { return s.l.Apply(index, data) }

// ledgerResult は cluster.Applied.Result を ledger.Result として読む。型が違えばプログラムの誤りなので panic する。
func ledgerResult(v any) ledger.Result {
	r, ok := v.(ledger.Result)
	if !ok {
		panic(fmt.Sprintf("sim: applied result is %T, want ledger.Result", v))
	}
	return r
}
