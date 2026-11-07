package simconv

import (
	"encoding/json"
	"fmt"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// electionEstimateNote は node.detail の electionEstimate.note（プロトコル §5.4）。
const electionEstimateNote = "推定値"

// NodeDetailFromSim は sim.NodeDetail を node.detail の payload（プロトコル §5.4）にする。
//
// log は空でも []。progress は Leader でなければ nil（omitempty で出ない）。
// ledger はすべての資源のキーを入れる。firework は予約した reqId の文字列（予約がなければ ""）、
// stall は reqId の配列（予約がなければ []）。map のキーの順は encoding/json が並べるので、走査の順に依存しない。
func NodeDetailFromSim(d sim.NodeDetail) protocol.NodeDetail {
	out := protocol.NodeDetail{
		ID:        d.ID,
		Role:      protocol.Role(d.Role),
		Term:      d.Term,
		Vote:      d.Vote,
		Lead:      d.Lead,
		Commit:    d.Commit,
		Applied:   d.Applied,
		LastIndex: d.LastIndex,
		Log:       make([]protocol.LogEntry, len(d.Log)),
		Ledger:    make(map[string]json.RawMessage, len(d.Ledger)),
		ElectionEstimate: protocol.ElectionEstimate{
			TicksSinceLeaderContact: d.TicksSinceLeaderContact,
			Note:                    electionEstimateNote,
		},
	}
	for i, e := range d.Log {
		out.Log[i] = protocol.LogEntry{
			Index:     e.Index,
			Term:      e.Term,
			Type:      protocol.EntryType(e.Type),
			Summary:   e.Summary,
			Committed: e.Committed,
		}
	}
	if len(d.Progress) > 0 {
		out.Progress = make([]protocol.Progress, len(d.Progress))
		for i, p := range d.Progress {
			out.Progress[i] = protocol.Progress{
				ID:           p.ID,
				Match:        p.Match,
				Next:         p.Next,
				State:        protocol.ProgressState(p.State),
				RecentActive: p.RecentActive,
			}
		}
	}
	for _, r := range d.Ledger {
		out.Ledger[r.ID] = ledgerValue(r)
	}
	return out
}

// ledgerValue は資源 1 つの ledger の値（JSON）を作る。firework は文字列、それ以外（stall）は文字列の配列。
func ledgerValue(r sim.ResourceView) json.RawMessage {
	var v any
	if r.Kind == "firework" {
		holder := ""
		if len(r.Holders) > 0 {
			holder = r.Holders[0]
		}
		v = holder
	} else {
		holders := r.Holders
		if holders == nil {
			holders = []string{}
		}
		v = holders
	}
	b, err := json.Marshal(v)
	if err != nil {
		// 文字列と文字列の配列の Marshal は失敗しない。
		panic(fmt.Sprintf("simconv: marshal ledger %q: %v", r.ID, err))
	}
	return b
}
