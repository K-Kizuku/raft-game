package stage

import "github.com/K-Kizuku/raft-game/server/internal/game"

// cancellationDefs は st.Events の cancellation を、書いた順に sim.Config.Cancellations の形にする。欠航がなければ nil。
// provenance・source（表示用）は使わない。
func cancellationDefs(st *Stage) []game.CancellationDef {
	var defs []game.CancellationDef
	for _, ev := range st.Events {
		if ev.Kind != EventCancellation {
			continue
		}
		defs = append(defs, game.CancellationDef{
			Island:              ev.Island,
			ProbabilityPermille: ev.ProbabilityPermille,
			SlotMs:              ev.SlotMs,
			DurationMs:          ev.DurationMs,
		})
	}
	return defs
}
