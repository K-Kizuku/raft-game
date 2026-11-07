package game

// Action は route の判断。
type Action uint8

const (
	ActPropose Action = iota + 1 // 自分が Leader なので Propose する
	ActLetter                    // 知っている Leader（To）へ依頼書を書く
	ActWait                      // 代表が分からないので待つ（次の RaftTick でやり直す）
)

// Decision は役場の判断の結果。
type Decision struct {
	Action Action
	To     uint64 // ActLetter のときの宛先
}

// Decide は、island の役場が、そのノードの役割と知っている Leader から判断する（DesignDoc §8.1）。
//
//	isLeader          → ActPropose
//	lead == 0         → ActWait（代表が分からない）
//	lead == island    → ActWait（Leader でないのに自分を Leader と思っている。ふつうは起きない）
//	そのほか          → ActLetter{To: lead}
func Decide(isLeader bool, lead, island uint64) Decision {
	switch {
	case isLeader:
		return Decision{Action: ActPropose}
	case lead == 0 || lead == island:
		return Decision{Action: ActWait}
	}
	return Decision{Action: ActLetter, To: lead}
}
