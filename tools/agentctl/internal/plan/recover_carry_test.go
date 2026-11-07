package plan

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/gate"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/snapshot"
)

const (
	carryHead = "1111111111111111111111111111111111111111"
	carryS    = "3333333333333333333333333333333333333333"
)

// carryGate は、Merge Gate のテストの (a)（main の取り込みだけ。testdata/carry-over.json）の入力。
func carryGate(t *testing.T) *gate.Input {
	t.Helper()
	data, err := os.ReadFile("../gate/testdata/carry-over.json")
	if err != nil {
		t.Fatal(err)
	}
	var in gate.Input
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	return &in
}

func carryConfig(t *testing.T) *config.Config {
	t.Helper()
	c, err := config.Load("../gate/testdata/config.json")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func carrySnapshot(g *gate.Input) *snapshot.Snapshot {
	return &snapshot.Snapshot{
		Issues: []snapshot.Issue{iss(3, "in-review", nil, "docs/**")},
		Pulls: []snapshot.Pull{{Number: 12, State: "open", Author: "K-Kizuku", HeadRef: "agent/issue-3", HeadSHA: carryHead,
			Body: "Closes #3", Comments: []snapshot.Comment{reviewComment(12, carryS)}, Gate: g}},
	}
}

func recoverActions(t *testing.T, s *snapshot.Snapshot, c *config.Config) []Action {
	t.Helper()
	return Recover(s, c, trusted).Actions
}

func TestRecoverCarryOver(t *testing.T) {
	c := carryConfig(t)

	// (a) main の取り込みだけ → request-review を出さない。
	if acts := recoverActions(t, carrySnapshot(carryGate(t)), c); len(acts) != 0 {
		t.Errorf("actions = %+v（request-review を出さない）", acts)
	}

	tests := map[string]struct {
		mutate func(g *gate.Input, c *config.Config) *gate.Input
	}{
		"判定の材料がない": {func(g *gate.Input, c *config.Config) *gate.Input { return nil }},
		"設定が false": {func(g *gate.Input, c *config.Config) *gate.Input {
			c.Review.CarryOverOnBaseMerge = false
			return g
		}},
		"S のあとに作者のコミット": {func(g *gate.Input, c *config.Config) *gate.Input {
			g.CarryOver.Commits = []gate.Commit{
				{SHA: carryHead, Parents: []string{"6666666666666666666666666666666666666666", "4444444444444444444444444444444444444444"}},
				{SHA: "6666666666666666666666666666666666666666", Parents: []string{carryS}},
			}
			return g
		}},
		"判定の材料の HEAD が PR の HEAD と違う": {func(g *gate.Input, c *config.Config) *gate.Input {
			g.PullRequest.HeadSHA = "7777777777777777777777777777777777777777"
			return g
		}},
		"判定の材料が別の PR のもの": {func(g *gate.Input, c *config.Config) *gate.Input {
			g.PullRequest.Number = 13
			return g
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := carryConfig(t)
			s := carrySnapshot(tt.mutate(carryGate(t), c))
			acts := recoverActions(t, s, c)
			if len(acts) != 1 || acts[0].Action != ActionRequestReview || acts[0].PR != 12 {
				t.Errorf("actions = %+v（request-review を出す）", acts)
			}
		})
	}
}
