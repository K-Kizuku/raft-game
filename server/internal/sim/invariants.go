package sim

import "fmt"

// checkInvariants はテストモードの検査（§11）。違反したら panic する。T-107 が I-1〜I-7 に広げる。
// 今は、全ノードの台帳の CheckInvariants（ID の昇順）と Transport.CheckInvariants を呼ぶ。
func (e *Engine) checkInvariants() {
	for _, l := range e.ledgers {
		if err := l.CheckInvariants(); err != nil {
			panic(fmt.Sprintf("sim: invariant violated at t=%d: %v", e.Now(), err))
		}
	}
	if err := e.tr.CheckInvariants(); err != nil {
		panic(fmt.Sprintf("sim: invariant violated at t=%d: %v", e.Now(), err))
	}
}
