package simconv

import (
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// RoundResultFromSim は round.result・internal.result の payload（プロトコル §5.5）を作る。
// r は Engine.Result()（採点しないときや終わる前はゼロ値でよい）、ts は Engine.TechStats()、
// story は game.WriteStory の値。nil の map・スライスは空にし（JSON で null を出さない）、入力と何も共有しない。
func RoundResultFromSim(r sim.RoundResult, ts sim.TechStats, story []game.StoryLine, inputLogAvailable bool) protocol.RoundResult {
	letters := make(map[string]int, len(ts.LettersDelivered))
	for k, v := range ts.LettersDelivered {
		letters[k] = int(v)
	}
	logs := make([]protocol.FinalLog, len(ts.FinalLogs))
	for i, l := range ts.FinalLogs {
		terms := slices.Clone(l.Terms)
		if terms == nil {
			terms = [][3]uint64{}
		}
		logs[i] = protocol.FinalLog{ID: l.ID, LastIndex: l.LastIndex, Commit: l.Commit, Terms: terms}
	}
	items := make([]protocol.StoryItem, len(story))
	for i, s := range story {
		concepts := slices.Clone(s.Concepts)
		if concepts == nil {
			concepts = []string{}
		}
		items[i] = protocol.StoryItem{T: s.T, Text: s.Text, Concepts: concepts}
	}
	b := r.Score.Breakdown
	return protocol.RoundResult{
		Score: protocol.RoundScore{
			Total: int(r.Score.Total),
			Breakdown: protocol.ScoreBreakdown{
				Requests:  int(b.Requests),
				Expired:   int(b.Expired),
				FerryCost: int(b.FerryCost),
				Fairness:  int(b.Fairness),
			},
		},
		Stars: r.Stars,
		Stats: protocol.RoundStats{
			Succeeded:          int(r.Stats.Succeeded),
			Rejected:           int(r.Stats.Rejected),
			Expired:            int(r.Stats.Expired),
			LettersDelivered:   letters,
			LeaderChanges:      int(ts.LeaderChanges),
			AvgCommitLatencyMs: ts.AvgCommitLatencyMs,
			LongestSilenceMs:   ts.LongestSilenceMs,
			FinalLogs:          logs,
		},
		Story:             items,
		InputLogAvailable: inputLogAvailable,
	}
}
