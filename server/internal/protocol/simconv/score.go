package simconv

import (
	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// IslandStatesFromSim は sim.Snapshot の Islands を snapshot の islandsState（プロトコル §5.1）にする。
// v が nil・空（採点しない）なら空の配列（JSON は []）。順序は v のまま（sim は島 ID の順に返す）。
// lastDeliveredAt は常に値を持つ（まだ配達がなければ 0。§5.1 の注記）。入力と何も共有しない。
func IslandStatesFromSim(v []sim.IslandState) []protocol.IslandState {
	out := make([]protocol.IslandState, len(v))
	for i, is := range v {
		last := int64(is.LastDeliveredAt) // 要素ごとに別の変数
		out[i] = protocol.IslandState{
			ID:              is.ID,
			Satisfaction:    int(is.Satisfaction),
			LastDeliveredAt: &last,
		}
	}
	return out
}

// ScoreFromSim は sim.Snapshot の Score を snapshot の score（プロトコル §5.1）にする。
// s が nil（採点しない）なら Total・Successes は 0、Target は target、CriticalSecured は空の配列。
// s が nil でなければ s の値をそのまま写す（Target も s の値。target は使わない）。
// CriticalSecured は複製し、nil なら空の配列にする（JSON で null にしない）。
func ScoreFromSim(s *sim.ScoreState, target int) protocol.Score {
	if s == nil {
		return protocol.Score{Target: target, CriticalSecured: []string{}}
	}
	cs := make([]string, len(s.CriticalSecured))
	copy(cs, s.CriticalSecured)
	return protocol.Score{
		Total:           int(s.Total),
		Successes:       int(s.Successes),
		Target:          int(s.Target),
		CriticalSecured: cs,
	}
}
