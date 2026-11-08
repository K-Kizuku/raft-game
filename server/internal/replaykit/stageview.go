package replaykit

import (
	"strconv"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/stageconv"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// StageView は snapshot の stage（プロトコル §5.1）を作る。municipality・population の asOf・provenance・note・source は
// islands（islands.json の表示用の値。T-201b）から埋める。len(islands) != len(st.Islands) なら空の文字列。
func StageView(st *stage.Stage, islands []stage.IslandDisplay) protocol.StageView {
	v := protocol.StageView{
		Title:     st.Title,
		Islands:   make([]protocol.Island, len(st.Islands)),
		Routes:    make([]protocol.Route, len(st.Routes)),
		Resources: make([]protocol.Resource, len(st.Resources)),
		RelayCap:  st.Transport.RelayCap,
	}
	withDisplay := len(islands) == len(st.Islands)
	for i, is := range st.Islands {
		isl := protocol.Island{
			ID:         is.NodeID,
			Name:       is.Name,
			Layout:     protocol.IslandLayout{X: int(is.X), Y: int(is.Y)},
			Population: protocol.Population{Value: int(is.Population)},
			Color:      is.Color,
		}
		if withDisplay {
			d := islands[i]
			isl.Municipality = d.MunicipalityLabel()
			isl.Population.AsOf = d.Population.AsOf
			isl.Population.Provenance = protocol.Provenance(d.Population.Provenance)
			isl.Population.Note = d.Population.Note
			isl.Population.Source = d.Population.Source
		}
		if pm := is.Postmaster; pm != nil {
			isl.Postmaster = &protocol.Postmaster{Name: pm.Name, Tone: pm.Tone, Portrait: pm.Portrait}
		}
		v.Islands[i] = isl
	}
	for i, r := range st.Routes {
		v.Routes[i] = protocol.Route{
			ID:         RouteID(r.A, r.B),
			A:          r.A,
			B:          r.B,
			TravelMs:   r.TravelMs,
			Provenance: protocol.Provenance(r.Provenance),
		}
	}
	for i, r := range st.Resources {
		v.Resources[i] = protocol.Resource{ID: r.ID, Kind: string(r.Kind), Label: r.Label, Critical: r.Critical}
	}
	v.DemoStorm = demoStormView(st)
	return v
}

// RouteID は航路の ID（"<小さい島 ID>-<大きい島 ID>"。プロトコル §1）。
func RouteID(a, b uint64) string {
	if a > b {
		a, b = b, a
	}
	return strconv.FormatUint(a, 10) + "-" + strconv.FormatUint(b, 10)
}

// demoStormView は snapshot の stage.demoStorm を作る（プロトコル §5.1。T-305b）。demo.storm がなければ nil。
// セッションが demo かどうかに関係なく、ステージの値として入れる（表示するかは画面が決める）。
// routeIds は demo.storm.routes の定義の順のまま（重複を除かない）、各組を RouteID（小さい ID が先）にしたもの。
func demoStormView(st *stage.Stage) *protocol.DemoStormView {
	if st.Demo == nil || st.Demo.Storm == nil {
		return nil
	}
	ds := st.Demo.Storm
	v := &protocol.DemoStormView{RouteIDs: make([]string, len(ds.Routes))}
	for i, r := range ds.Routes {
		v.RouteIDs[i] = RouteID(r[0], r[1])
	}
	if ds.Flavor != nil {
		f := stageconv.Text(*ds.Flavor)
		v.Flavor = &f
	}
	return v
}
