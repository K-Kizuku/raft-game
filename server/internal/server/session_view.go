package server

import (
	"strconv"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// buildSnapshot は snapshot の payload（プロトコル §5.1）を作る。run の goroutine から呼ぶ。
//
// simconv.SnapshotFromSim の上に、session・clock の speed・paused・allowedSpeeds・stage・score.target を埋める。
// requests・islandsState・score の中身は T-202・T-203 が作るまで空の配列とゼロ。JSON に null の配列を出さない。
func (s *session) buildSnapshot() protocol.Snapshot {
	p := simconv.SnapshotFromSim(s.engine.Snapshot())
	p.Session = protocol.Session{
		ID:        s.id,
		StageID:   s.stageID,
		Seed:      s.seed,
		Demo:      s.demo,
		Restored:  s.restored,
		StageHash: s.st.Hash,
		Build:     s.build,
	}
	c := s.clock()
	p.Clock.Speed = c.Speed
	p.Clock.Paused = c.Paused
	p.Clock.AllowedSpeeds = allowedSpeeds(s.st)
	p.Stage = stageView(s.st)
	p.Score = protocol.Score{
		Total:           0,
		Successes:       0,
		Target:          int(s.st.Scoring.TargetSuccesses),
		CriticalSecured: []string{},
	}
	return p
}

// allowedSpeeds は speed.allowed（千分率）を倍率にする（500 → 0.5）。8 倍速の追加は T-305。
func allowedSpeeds(st *stage.Stage) []float64 {
	out := make([]float64, len(st.SpeedAllowedPermille))
	for i, v := range st.SpeedAllowedPermille {
		out[i] = float64(v) / 1000
	}
	return out
}

// stageView は snapshot の stage を作る。stage.Stage が持たない値（municipality・population の asOf・
// provenance・note・source）は空の文字列（T-302 の対象外）。
func stageView(st *stage.Stage) protocol.StageView {
	v := protocol.StageView{
		Title:     st.Title,
		Islands:   make([]protocol.Island, len(st.Islands)),
		Routes:    make([]protocol.Route, len(st.Routes)),
		Resources: make([]protocol.Resource, len(st.Resources)),
	}
	for i, is := range st.Islands {
		v.Islands[i] = protocol.Island{
			ID:           is.NodeID,
			Name:         is.Name,
			Municipality: "",
			Layout:       protocol.IslandLayout{X: int(is.X), Y: int(is.Y)},
			Population:   protocol.Population{Value: int(is.Population)},
			Color:        is.Color,
		}
	}
	for i, r := range st.Routes {
		v.Routes[i] = protocol.Route{
			ID:         routeID(r.A, r.B),
			A:          r.A,
			B:          r.B,
			TravelMs:   r.TravelMs,
			Provenance: protocol.Provenance(r.Provenance),
		}
	}
	for i, r := range st.Resources {
		v.Resources[i] = protocol.Resource{ID: r.ID, Kind: string(r.Kind), Label: r.Label, Critical: r.Critical}
	}
	return v
}

// routeID は航路の ID（"<小さい島 ID>-<大きい島 ID>"。プロトコル §1）。
func routeID(a, b uint64) string {
	if a > b {
		a, b = b, a
	}
	return strconv.FormatUint(a, 10) + "-" + strconv.FormatUint(b, 10)
}

// buildResult は round.result・internal.result の payload（プロトコル §5.5）を作る。
// 中身は T-203 が Engine.Result() を作るまでの枠: 点数・星・統計の数は 0、lettersDelivered は {}、
// finalLogs は Snapshot().Nodes から、story は []。inputLogAvailable は single なら true。
func (s *session) buildResult() protocol.RoundResult {
	snap := s.engine.Snapshot()
	logs := make([]protocol.FinalLog, len(snap.Nodes))
	for i, n := range snap.Nodes {
		logs[i] = protocol.FinalLog{ID: n.ID, LastIndex: n.LastIndex, Commit: n.Commit, Terms: [][3]uint64{}}
	}
	return protocol.RoundResult{
		Stats: protocol.RoundStats{
			LettersDelivered: map[string]int{},
			FinalLogs:        logs,
		},
		Story:             []protocol.StoryItem{},
		InputLogAvailable: s.mode == ModeSingle,
	}
}
