package simconv_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// compactJSON は JSON を空白なしの形にそろえる（キーの順は変えない）。
func compactJSON(t *testing.T, b []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// NDC-1: プロトコル §5.4 の形になる（testdata/node-detail.json と同じ）。
func TestNodeDetailFromSimLeader(t *testing.T) {
	t.Parallel()
	d := sim.NodeDetail{
		ID: 2, Role: "leader", Term: 3, Vote: 2, Lead: 2, Commit: 18, Applied: 18, LastIndex: 19,
		Log: []sim.LogEntryDetail{{Index: 19, Term: 3, Type: "normal", Summary: "firework.reserve stage2-0009", Committed: false}},
		Progress: []sim.ProgressView{
			{ID: 1, Match: 15, Next: 16, State: "probe", RecentActive: false},
			{ID: 3, Match: 19, Next: 20, State: "replicate", RecentActive: true},
		},
		Ledger: []sim.ResourceView{
			{ID: "firework:a", Kind: "firework", Holders: []string{"stage2-0002"}},
			{ID: "stall:main", Kind: "stall", Holders: []string{"stage2-0001", "stage2-0005"}},
		},
		TicksSinceLeaderContact: 0,
	}
	got, err := json.Marshal(simconv.NodeDetailFromSim(d))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../testdata/node-detail.json")
	if err != nil {
		t.Fatal(err)
	}
	var gotV, wantV protocol.NodeDetail
	if err := decodeStrict(got, &gotV); err != nil {
		t.Fatalf("decode %s: %v", got, err)
	}
	if err := decodeStrict(want, &wantV); err != nil {
		t.Fatal(err)
	}
	g, _ := json.Marshal(gotV)
	w, _ := json.Marshal(wantV)
	if compactJSON(t, g) != compactJSON(t, w) {
		t.Errorf("json:\n got  %s\n want %s", g, w)
	}
}

// NDC-1: Follower・予約なし・ログなし。
func TestNodeDetailFromSimEmpty(t *testing.T) {
	t.Parallel()
	d := sim.NodeDetail{
		ID: 3, Role: "follower", Term: 2, Vote: 1, Lead: 1,
		Log: []sim.LogEntryDetail{},
		Ledger: []sim.ResourceView{
			{ID: "firework:a", Kind: "firework", Holders: []string{}},
			{ID: "stall:main", Kind: "stall", Holders: nil},
		},
		TicksSinceLeaderContact: 12,
	}
	got, err := json.Marshal(simconv.NodeDetailFromSim(d))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":3,"role":"follower","term":2,"vote":1,"lead":1,"commit":0,"applied":0,"lastIndex":0,` +
		`"log":[],"ledger":{"firework:a":"","stall:main":[]},` +
		`"electionEstimate":{"ticksSinceLeaderContact":12,"note":"推定値"}}`
	if string(got) != want {
		t.Errorf("json:\n got  %s\n want %s", got, want)
	}

	// log が nil でも []、資源がなければ ledger は {}。progress は空なら出ない。
	got, err = json.Marshal(simconv.NodeDetailFromSim(sim.NodeDetail{ID: 1, Role: "leader", Progress: []sim.ProgressView{}}))
	if err != nil {
		t.Fatal(err)
	}
	want = `{"id":1,"role":"leader","term":0,"vote":0,"lead":0,"commit":0,"applied":0,"lastIndex":0,` +
		`"log":[],"ledger":{},"electionEstimate":{"ticksSinceLeaderContact":0,"note":"推定値"}}`
	if string(got) != want {
		t.Errorf("json:\n got  %s\n want %s", got, want)
	}
}

// NodeDetailFromSim は入力と記憶を共有しない。
func TestNodeDetailFromSimDoesNotShare(t *testing.T) {
	t.Parallel()
	d := sim.NodeDetail{
		Log:      []sim.LogEntryDetail{{Index: 1}},
		Progress: []sim.ProgressView{{ID: 2}},
		Ledger:   []sim.ResourceView{{ID: "stall:main", Kind: "stall", Holders: []string{"a"}}},
	}
	p := simconv.NodeDetailFromSim(d)
	p.Log[0].Index = 9
	p.Progress[0].ID = 9
	if d.Log[0].Index != 1 || d.Progress[0].ID != 2 {
		t.Errorf("NodeDetailFromSim shares memory with its input: %+v", d)
	}
}
