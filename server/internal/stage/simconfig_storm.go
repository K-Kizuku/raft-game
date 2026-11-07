package stage

import (
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/game"
)

// stormDefs は st.Events の storm を、書いた順に sim.Config.Storms の形にする。嵐がなければ nil。
// flavor（表示用。T-301）と demo.storm（T-305）は使わない。
//
// storm と breakdown 以外のイベント（P1）は、今は使わず Issue にもしない。Issue #32 の仕様は events[i].kind の Issue にするが、
// 既存の SC-4（beta.yaml の Issue は raft.preVote の 1 件だけ）と食い違うので、Orchestrator の判断を待つ（#32 の報告）。
// 返す []Issue はそのための欄で、今は常に nil。
func stormDefs(st *Stage) ([]game.StormDef, []Issue) {
	var defs []game.StormDef
	for _, ev := range st.Events {
		if ev.Kind != EventStorm {
			continue
		}
		defs = append(defs, game.StormDef{
			WindowStart: ev.When.Start,
			WindowEnd:   ev.When.End,
			DurationMs:  ev.DurationMs,
			Routes:      slices.Clone(ev.Routes),
			ForecastMs:  ev.ForecastMs,
		})
	}
	return defs, nil
}
