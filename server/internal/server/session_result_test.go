package server

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// resultTestSession は testdata の alpha の Engine（シード 1）を持つセッションを作る。run を始めないので、
// このテストの goroutine が Engine に触れてよい。
func resultTestSession(t *testing.T) (*session, *sim.Engine, *stage.Stage) {
	t.Helper()
	alpha, ok := testCatalog(t).Lookup("alpha")
	if !ok {
		t.Fatal("alpha not found")
	}
	cfg, err := stage.SimConfig(alpha)
	if err != nil {
		t.Fatal(err)
	}
	e, err := sim.New(cfg, 1, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatal(err)
	}
	p := sessionParams{id: "s_x", st: alpha, seed: 1, mode: ModeSingle, build: "b", engine: e,
		log: discardLogger(), now: time.Now}
	return newSession(t.Context(), p), e, alpha
}

func TestBuildResultEnded(t *testing.T) { // SV-1
	t.Parallel()
	s, e, _ := resultTestSession(t)
	if ack := e.Apply(sim.Command{Type: sim.CmdGameStart}); !ack.OK {
		t.Fatalf("start: %+v", ack)
	}
	e.RunUntil(180000) // 船を動かさない
	if e.Phase() != sim.PhaseEnded {
		t.Fatalf("phase = %s, want ended", e.Phase())
	}
	r := s.buildResult()

	want, ok := e.Result()
	if !ok {
		t.Fatal("Result() = false")
	}
	b := want.Score.Breakdown
	if r.Score != (protocol.RoundScore{Total: int(want.Score.Total), Breakdown: protocol.ScoreBreakdown{
		Requests: int(b.Requests), Expired: int(b.Expired), FerryCost: int(b.FerryCost), Fairness: int(b.Fairness),
	}}) || r.Stars != want.Stars ||
		r.Stats.Succeeded != int(want.Stats.Succeeded) || r.Stats.Rejected != int(want.Stats.Rejected) || r.Stats.Expired != int(want.Stats.Expired) {
		t.Errorf("score %+v stars %d stats %+v, want %+v", r.Score, r.Stars, r.Stats, want)
	}
	if !r.InputLogAvailable {
		t.Error("inputLogAvailable = false in single")
	}
	if len(r.Stats.FinalLogs) != 3 {
		t.Fatalf("finalLogs = %+v, want 3", r.Stats.FinalLogs)
	}
	for _, l := range r.Stats.FinalLogs {
		if len(l.Terms) == 0 {
			t.Errorf("finalLogs %+v: terms is empty", l)
		}
	}
	if r.Stats.LettersDelivered == nil {
		t.Error("lettersDelivered = nil")
	}
	ts := e.TechStats()
	if r.Stats.LongestSilenceMs != ts.LongestSilenceMs || r.Stats.LeaderChanges != int(ts.LeaderChanges) ||
		r.Stats.AvgCommitLatencyMs != ts.AvgCommitLatencyMs || r.Stats.LongestSilenceMs != 180000 {
		t.Errorf("stats = %+v, TechStats = %+v", r.Stats, ts)
	}

	for _, it := range r.Story {
		t.Logf("story: %+v", it)
	}
	for _, w := range []protocol.StoryItem{
		{Text: "星砂島は過半数の島と連絡が取れなくなったので、自分から代表を降りました。", Concepts: []string{"CheckQuorum"}},
		{Text: "星砂島には 180 秒のあいだ、手紙が 1 通も届きませんでした。", Concepts: []string{"Heartbeat", "Election Timeout"}},
	} {
		if !slices.ContainsFunc(r.Story, func(it protocol.StoryItem) bool {
			return it.Text == w.Text && reflect.DeepEqual(it.Concepts, w.Concepts)
		}) {
			t.Errorf("story does not contain %+v", w)
		}
	}
	if !slices.IsSortedFunc(r.Story, func(a, b protocol.StoryItem) int { return int(a.T - b.T) }) {
		t.Errorf("story is not sorted by t: %+v", r.Story)
	}
	assertNoNullArrays(t, []byte(mustJSON(t, r)))
}

func TestStoryNames(t *testing.T) { // SV-2
	t.Parallel()
	_, _, alpha := resultTestSession(t)
	n := storyNames(alpha)
	var wantIslands []string
	for _, is := range alpha.Islands {
		wantIslands = append(wantIslands, is.Name)
	}
	if !reflect.DeepEqual(n.Islands, wantIslands) || n.Islands[0] != "星砂島" {
		t.Errorf("Islands = %v, want %v", n.Islands, wantIslands)
	}
	if want := map[string]string{"firework:a": "大花火", "stall:main": "屋台の出店枠"}; !reflect.DeepEqual(n.Resources, want) {
		t.Errorf("Resources = %v, want %v", n.Resources, want)
	}
}
