package loadmodel

import (
	"context"
	"testing"
)

// G-9: Ready の処理が「Append → SetHardState → 送信 → 適用 → Advance」の順。
func TestProcessReadyOrder(t *testing.T) {
	t.Parallel()
	c, err := NewCluster(3, 2)
	if err != nil {
		t.Fatal(err)
	}
	type rec struct {
		node  uint64
		phase Phase
	}
	var got []rec
	c.OnPhase = func(node uint64, p Phase) { got = append(got, rec{node, p}) }
	if err := c.Advance(60_000); err != nil {
		t.Fatal(err)
	}
	if c.Leader() == 0 {
		t.Fatal("no leader after 60s of game time")
	}
	if len(got) == 0 {
		t.Fatal("no phases recorded")
	}
	// ノードごとに、1 つの Ready の中の段階が増える順であり、Append で始まり Advance で終わる。
	last := map[uint64]Phase{}
	readies, hardStates := 0, 0
	for i, r := range got {
		prev := last[r.node]
		switch {
		case r.phase == PhaseAppend:
			if prev != 0 && prev != PhaseAdvance {
				t.Fatalf("#%d node %d: append after %v (previous Ready not advanced)", i, r.node, prev)
			}
			readies++
		case prev == 0 || prev == PhaseAdvance:
			t.Fatalf("#%d node %d: %v before append", i, r.node, r.phase)
		case r.phase <= prev:
			t.Fatalf("#%d node %d: %v after %v", i, r.node, r.phase, prev)
		}
		if r.phase == PhaseSend && prev != PhaseAppend && prev != PhaseHardState {
			t.Fatalf("#%d node %d: send after %v", i, r.node, prev)
		}
		if r.phase == PhaseHardState {
			hardStates++
		}
		last[r.node] = r.phase
	}
	for node, p := range last {
		if p != PhaseAdvance {
			t.Fatalf("node %d: last phase %v, want advance", node, p)
		}
	}
	if readies == 0 || hardStates == 0 {
		t.Fatalf("readies=%d hardStates=%d, want both > 0", readies, hardStates)
	}
}

// G-3（模型の部分）: Leader が決まり、提案がすべてコミットされる。
func TestRunRaftCommitsProposals(t *testing.T) {
	t.Parallel()
	res, err := RunRaft(RaftConfig{Nodes: 3, Minutes: 1, ProposalsPerSec: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.LeaderChanges < 1 {
		t.Fatalf("leaderChanges=%d, want >= 1", res.LeaderChanges)
	}
	if res.Proposed == 0 || res.Committed < res.Proposed {
		t.Fatalf("committed=%d proposed=%d, want committed >= proposed > 0", res.Committed, res.Proposed)
	}
	if res.WallMs <= 0 || res.VirtualMinutesPerSecond <= 0 {
		t.Fatalf("wallMs=%v virtualMinutesPerSecond=%v, want > 0", res.WallMs, res.VirtualMinutesPerSecond)
	}
}

func TestSingleNodeElectsItself(t *testing.T) {
	t.Parallel()
	res, err := RunRaft(RaftConfig{Nodes: 1, Minutes: 1, ProposalsPerSec: 20})
	if err != nil {
		t.Fatal(err)
	}
	if res.LeaderChanges != 1 || res.Committed < res.Proposed || res.Proposed == 0 {
		t.Fatalf("got %+v", res)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	bad := []RaftConfig{
		{Nodes: 0, Minutes: 1}, {Nodes: 10, Minutes: 1}, {Nodes: 3, Minutes: 0},
		{Nodes: 3, Minutes: 61}, {Nodes: 3, Minutes: 1, ProposalsPerSec: 21}, {Nodes: 3, Minutes: 1, ProposalsPerSec: -1},
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v: want error", c)
		}
	}
	badLoop := []LoopConfig{
		{Nodes: 3, Speed: "3", Seconds: 1}, {Nodes: 3, Speed: "8", Seconds: 0}, {Nodes: 3, Speed: "8", Seconds: 61},
		{Nodes: 3, Speed: "8", Seconds: 1, EventsPerFlush: 1001}, {Nodes: 0, Speed: "8", Seconds: 1},
	}
	for _, c := range badLoop {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v: want error", c)
		}
	}
	if _, err := RunLoop(context.Background(), LoopConfig{Nodes: 3, Speed: "3", Seconds: 1}); err == nil {
		t.Error("RunLoop speed=3: want error")
	}
}

func TestPercentile(t *testing.T) {
	t.Parallel()
	if got := Percentile(nil, 50); got != 0 {
		t.Fatalf("empty: %v", got)
	}
	v := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, tc := range []struct {
		p    int
		want float64
	}{{50, 5}, {95, 10}, {99, 10}, {100, 10}, {10, 1}, {0, 1}} {
		if got := Percentile(v, tc.p); got != tc.want {
			t.Errorf("p%d = %v, want %v", tc.p, got, tc.want)
		}
	}
}
