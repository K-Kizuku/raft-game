package stage

import (
	"errors"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// SimConfig は検証済みのステージ st から、sim.New に渡す設定を作る。
// st は変えない。返す値は st とスライスを共有しない（呼び出し側が書き換えてもよい）。
// sim.Config で表せない値（raft.checkQuorum・raft.preVote が false）は *Error を返す。
// 依頼・イベント・スコアなど、sim.Config にまだ欄がない値は使わない（後のタスクが足す）。
func SimConfig(st *Stage) (sim.Config, error) {
	if st == nil {
		return sim.Config{}, errors.New("stage: SimConfig: nil stage")
	}

	// cluster は CheckQuorum・PreVote を常に有効にする（ADR-0007）。黙って落とすと、
	// ステージに書いた値と違う動きになるので誤りにする。relayCap（P1）は使わない。
	var issues []Issue
	if !st.Raft.CheckQuorum {
		issues = append(issues, Issue{
			Path:    "raft.checkQuorum",
			Message: "false は未対応（cluster は CheckQuorum を常に有効にする。ADR-0007）",
		})
	}
	if !st.Raft.PreVote {
		issues = append(issues, Issue{
			Path:    "raft.preVote",
			Message: "false は未対応（cluster は PreVote を常に有効にする。ADR-0007）",
		})
	}
	if len(issues) > 0 {
		return sim.Config{}, &Error{File: st.ID, Issues: issues}
	}

	routes := make([]ferry.RouteConfig, len(st.Routes))
	for i, r := range st.Routes {
		routes[i] = ferry.RouteConfig{A: r.A, B: r.B, TravelMs: r.TravelMs}
	}
	ferries := make([]ferry.FerryConfig, len(st.Ferries))
	for i, f := range st.Ferries {
		ferries[i] = ferry.FerryConfig{
			ID:            ferry.FerryID(f.ID),
			Name:          f.Name,
			Home:          f.Home,
			Capacity:      f.Capacity,
			SpeedPermille: f.SpeedPermille,
		}
	}
	resources := make([]ledger.ResourceDef, len(st.Resources))
	for i, r := range st.Resources {
		resources[i] = ledger.ResourceDef{ID: r.ID, Kind: ledger.ResourceKind(r.Kind), Capacity: r.Capacity}
	}

	return sim.Config{
		Islands:       len(st.Islands),
		InitialLeader: st.Raft.InitialLeader,
		DurationMs:    sim.SimTime(st.DurationMs),
		Raft: sim.RaftConfig{
			TickMs:          sim.SimTime(st.Raft.TickMs),
			HeartbeatTick:   st.Raft.HeartbeatTick,
			ElectionTick:    st.Raft.ElectionTick,
			MaxSizePerMsg:   st.Raft.MaxSizePerMsg,
			MaxInflightMsgs: st.Raft.MaxInflightMsgs,
		},
		Routes:        routes,
		Ferries:       ferries,
		DockMs:        st.Transport.DockMs,
		PortCap:       st.Transport.PortCap,
		PerLinkAppCap: st.Transport.PerLinkAppCap,
		Resources:     resources,
	}, nil
}
