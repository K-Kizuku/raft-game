package stage

import "github.com/K-Kizuku/raft-game/server/internal/game"

// breakdownDefs は st.Events の breakdown を、書いた順に sim.Config.Breakdowns の形にする。故障がなければ nil。
// ferry: any は ""（シードで選ぶ）にする。
func breakdownDefs(st *Stage) []game.BreakdownDef {
	var defs []game.BreakdownDef
	for _, ev := range st.Events {
		if ev.Kind != EventBreakdown {
			continue
		}
		f := ev.Ferry
		if f == anyFerry {
			f = ""
		}
		defs = append(defs, game.BreakdownDef{
			WindowStart: ev.When.Start,
			WindowEnd:   ev.When.End,
			DurationMs:  ev.DurationMs,
			Ferry:       f,
		})
	}
	return defs
}
