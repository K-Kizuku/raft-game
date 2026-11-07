package stage

import (
	"errors"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// SimConfig は検証済みのステージ st から、sim.New に渡す設定を作る。
// st は変えない。返す値は st とスライスを共有しない（呼び出し側が書き換えてもよい）。
// sim.Config で表せない値（raft.checkQuorum・raft.preVote が false）は *Error を返す。
// イベント・スコアなど、sim.Config にまだ欄がない値は使わない（後のタスクが足す）。
// 依頼の重みは islands[].WeightPermille（データ仕様 §4.1 で計算済み）を使い、populationExponent は使わない。
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
	storms, evIssues := stormDefs(st)
	issues = append(issues, evIssues...)
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
		Storms:        storms,
		Ferries:       ferries,
		DockMs:        st.Transport.DockMs,
		PortCap:       st.Transport.PortCap,
		PerLinkAppCap: st.Transport.PerLinkAppCap,
		Resources:     resources,
		Requests:      requestConfig(st),
		Score:         scoreConfig(st),
	}, nil
}

// requestConfig は st の requests.* から依頼の設定を作る。スライスは新しく作る。
func requestConfig(st *Stage) game.RequestConfig {
	rq := st.Requests
	script := make([]game.ScriptRequest, len(rq.Script))
	for i, s := range rq.Script {
		script[i] = game.ScriptRequest{At: s.At, Island: s.Island, Kind: s.Kind, Resource: s.Resource}
	}
	weights := make([]int64, len(st.Islands))
	for i, is := range st.Islands {
		weights[i] = is.WeightPermille
	}
	kinds := make([]game.RandomKind, len(rq.Random.Kinds))
	for i, k := range rq.Random.Kinds {
		kinds[i] = game.RandomKind{Kind: k.Kind, Resource: k.Resource, Weight: k.Weight}
	}
	var critical []string
	for _, r := range st.Resources {
		if r.Critical {
			critical = append(critical, r.ID)
		}
	}
	return game.RequestConfig{
		StageID:            st.ID,
		DeadlineNormalMs:   rq.DeadlineNormalMs,
		DeadlineCriticalMs: rq.DeadlineCriticalMs,
		RetryMs:            rq.RetryMs,
		Script:             script,
		Random: game.RandomConfig{
			RateMilliPerMin: rq.Random.RateMilliPerMin,
			SlotMs:          rq.Random.SlotMs,
			Weights:         weights,
			Kinds:           kinds,
		},
		Critical: critical,
	}
}
