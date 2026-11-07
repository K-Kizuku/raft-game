package simconv_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

func TestRoundResultFromSimZero(t *testing.T) { // RC-1
	t.Parallel()
	b, err := json.Marshal(simconv.RoundResultFromSim(sim.RoundResult{}, sim.TechStats{}, nil, false))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "null") {
		t.Errorf("JSON has null: %s", s)
	}
	for _, want := range []string{`"lettersDelivered":{}`, `"finalLogs":[]`, `"story":[]`} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON %s does not contain %s", s, want)
		}
	}

	// 中の nil（terms・concepts）も空にする。
	b, err = json.Marshal(simconv.RoundResultFromSim(sim.RoundResult{},
		sim.TechStats{FinalLogs: []sim.FinalLog{{ID: 1}}},
		[]game.StoryLine{{T: 1, Text: "x"}}, false))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Contains(s, "null") || !strings.Contains(s, `"terms":[]`) || !strings.Contains(s, `"concepts":[]`) {
		t.Errorf("JSON = %s, want terms [] and concepts [] without null", s)
	}
}

// rcExample はプロトコル §5.5 の例と同じ値の入力。
func rcExample() (sim.RoundResult, sim.TechStats, []game.StoryLine) {
	r := sim.RoundResult{
		Score: sim.ResultScore{Total: 1520, Breakdown: sim.ScoreBreakdown{Requests: 1300, Expired: -100, FerryCost: -180, Fairness: 500}},
		Stars: 2,
		Stats: sim.ResultStats{Succeeded: 7, Rejected: 2, Expired: 2},
	}
	ts := sim.TechStats{
		LettersDelivered:   map[string]int64{"heartbeat": 40, "append": 22, "append_resp": 19, "vote": 3, "request": 9},
		LeaderChanges:      1,
		AvgCommitLatencyMs: 18400,
		LongestSilenceMs:   41000,
		FinalLogs:          []sim.FinalLog{{ID: 1, LastIndex: 24, Commit: 24, Terms: [][3]uint64{{1, 1, 9}, {3, 10, 24}}}},
	}
	story := []game.StoryLine{{
		T:        72000,
		Text:     "嵐で福江島が孤立しました。福江島は過半数と連絡が取れなくなったので、自分から代表を降りました。",
		Concepts: []string{"CheckQuorum"},
	}}
	return r, ts, story
}

func TestRoundResultFromSimValues(t *testing.T) { // RC-2
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "round-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want protocol.RoundResult
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	r, ts, story := rcExample()
	if got := simconv.RoundResultFromSim(r, ts, story, true); !reflect.DeepEqual(got, want) {
		t.Errorf("RoundResultFromSim =\n %+v\nwant\n %+v", got, want)
	}

	got := simconv.RoundResultFromSim(r, ts, []game.StoryLine{{T: 5, Text: "y"}}, true)
	if c := got.Story[0].Concepts; c == nil || len(c) != 0 {
		t.Errorf("Concepts = %#v, want []", c)
	}
}

func TestRoundResultFromSimNoSharing(t *testing.T) { // RC-3
	t.Parallel()
	r, ts, story := rcExample()
	got := simconv.RoundResultFromSim(r, ts, story, true)
	got.Stats.LettersDelivered["heartbeat"] = 0
	got.Stats.LettersDelivered["x"] = 1
	got.Stats.FinalLogs[0].Terms[0][0] = 99
	got.Story[0].Concepts[0] = "changed"

	r2, ts2, story2 := rcExample()
	if !reflect.DeepEqual(r, r2) || !reflect.DeepEqual(ts, ts2) || !reflect.DeepEqual(story, story2) {
		t.Errorf("input changed:\n %+v %+v %+v", r, ts, story)
	}
}
