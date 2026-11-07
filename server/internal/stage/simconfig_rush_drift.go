package stage

import (
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// rushDefs は st.Events の rush を、書いた順に sim.Config.Rushes の形にする。ラッシュがなければ nil。
// 依頼の種類と島の重みは requestConfig が作る Random.Kinds・Random.Weights を sim が使う（ここでは複製しない）。
func rushDefs(st *Stage) []game.RushDef {
	var defs []game.RushDef
	for _, ev := range st.Events {
		if ev.Kind != EventRush {
			continue
		}
		defs = append(defs, game.RushDef{
			WindowStart: ev.When.Start,
			WindowEnd:   ev.When.End,
			Requests:    ev.Requests,
			ForecastMs:  ev.ForecastMs,
		})
	}
	return defs
}

// driftDefs は st.Events の drift を、書いた順に sim.Config.Drifts の形にする。漂流瓶がなければ nil。
func driftDefs(st *Stage) []sim.DriftDef {
	var defs []sim.DriftDef
	for _, ev := range st.Events {
		if ev.Kind != EventDrift {
			continue
		}
		defs = append(defs, sim.DriftDef{
			WindowStart: ev.When.Start,
			WindowEnd:   ev.When.End,
			Letters:     ev.Letters,
			MinAgeMs:    ev.MinAgeMs,
		})
	}
	return defs
}
