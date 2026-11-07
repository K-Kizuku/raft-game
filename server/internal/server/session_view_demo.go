package server

import (
	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/stageconv"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// demoStormView は snapshot の stage.demoStorm を作る（プロトコル §5.1。T-305b）。demo.storm がなければ nil。
// セッションが demo かどうかに関係なく、ステージの値として入れる（表示するかは画面が決める）。
// routeIds は demo.storm.routes の定義の順のまま（重複を除かない）、各組を routeID（小さい ID が先）にしたもの。
func demoStormView(st *stage.Stage) *protocol.DemoStormView {
	if st.Demo == nil || st.Demo.Storm == nil {
		return nil
	}
	ds := st.Demo.Storm
	v := &protocol.DemoStormView{RouteIDs: make([]string, len(ds.Routes))}
	for i, r := range ds.Routes {
		v.RouteIDs[i] = routeID(r[0], r[1])
	}
	if ds.Flavor != nil {
		f := stageconv.Text(*ds.Flavor)
		v.Flavor = &f
	}
	return v
}
