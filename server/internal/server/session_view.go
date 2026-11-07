package server

import (
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/replaykit"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// buildSnapshot は snapshot の payload（プロトコル §5.1）を作る。run の goroutine から呼ぶ。
//
// simconv.SnapshotFromSim の上に、session・clock の speed・paused・allowedSpeeds・stage・islandsState・score を埋める。
// islandsState・score は sim.Snapshot から作る（採点しないときは空の配列と、target だけを持つゼロ）。
// requests はまだ空の配列。JSON に null の配列を出さない。
func (s *session) buildSnapshot() protocol.Snapshot {
	snap := s.engine.Snapshot()
	p := simconv.SnapshotFromSim(snap)
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
	p.Clock.AllowedSpeeds = allowedSpeeds(s.allowed)
	p.Stage = stageView(s.st, s.islands)
	p.IslandsState = simconv.IslandStatesFromSim(snap.Islands)
	p.Score = simconv.ScoreFromSim(snap.Score, int(s.st.Scoring.TargetSuccesses))
	return p
}

// allowedSpeeds は使える速度（千分率。allowedSpeedsPermille）を倍率にする（500 → 0.5、8000 → 8）。
func allowedSpeeds(permille []int64) []float64 {
	out := make([]float64, len(permille))
	for i, v := range permille {
		out[i] = float64(v) / 1000
	}
	return out
}

// stageView は snapshot の stage を作る。municipality・population の asOf・provenance・note・source は
// islands（islands.json の表示用の値。T-201b）から埋める。len(islands) != len(st.Islands) なら空の文字列。
// 本体は replaykit（ブラウザでのリプレイと共有する。ADR-0028）。
func stageView(st *stage.Stage, islands []stage.IslandDisplay) protocol.StageView {
	return replaykit.StageView(st, islands)
}

// buildResult は round.result・internal.result の payload（プロトコル §5.5）を作る。run の goroutine から呼ぶ。
//
// 点数・★・依頼の結果の数は Engine.Result()（採点しないステージ・終わる前はゼロ値のまま使う）、ほかの統計は
// Engine.TechStats()、story は Engine.StoryFacts() から game.WriteStory で組み立てた文。inputLogAvailable は single なら true。
// JSON に null の map・配列を出さない（simconv.RoundResultFromSim）。
func (s *session) buildResult() protocol.RoundResult {
	r, _ := s.engine.Result() // 採点しない・終わる前は false（ゼロ値のまま使う）
	story := game.WriteStory(s.engine.StoryFacts(), storyNames(s.st))
	return simconv.RoundResultFromSim(r, s.engine.TechStats(), story, s.mode == ModeSingle)
}

// storyNames は文章に使う島（stage.Islands の順 = ノード ID の順）と資源（ID → Label）の名前。
// 本体は replaykit（ブラウザでのリプレイと共有する。ADR-0028）。
func storyNames(st *stage.Stage) game.StoryNames {
	return replaykit.StoryNames(st)
}
