package stage

import (
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// demoStorm は st の demo.storm を sim.Config.DemoStorm の形にする。書いていなければ nil。
// 航路は書いた順・向きのまま複製する。flavor（表示用）と demo.endOnAllCriticalSecured は使わない（sim は文を持たない）。
// セッションが demo かどうかに関係なく入れる（デモ操作を許すかはセッションが決める。リプレイでも同じ結果にするため）。
func demoStorm(st *Stage) *sim.DemoStorm {
	if st.Demo == nil || st.Demo.Storm == nil {
		return nil
	}
	return &sim.DemoStorm{
		Routes:     slices.Clone(st.Demo.Storm.Routes),
		DurationMs: st.Demo.Storm.DurationMs,
	}
}
