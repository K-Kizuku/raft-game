package game

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func storyTestNames() StoryNames {
	return StoryNames{
		Islands:   []string{"星砂島", "月影島", "灯台島"},
		Resources: map[string]string{"firework:a": "大花火", "stall:main": "屋台の出店枠"},
	}
}

func TestWriteStoryLines(t *testing.T) { // G-1
	t.Parallel()
	tests := []struct {
		name string
		fact StoryFact
		want StoryLine
	}{
		{
			"storm isolated",
			StoryFact{T: 10000, Kind: StoryStorm, Routes: [][2]uint64{{2, 3}}, Isolated: []uint64{3}},
			StoryLine{10000, "嵐で灯台島が孤立しました。", []string{"Network Partition"}},
		},
		{
			"storm isolated 2",
			StoryFact{T: 10000, Kind: StoryStorm, Routes: [][2]uint64{{1, 2}, {2, 3}}, Isolated: []uint64{1, 3}},
			StoryLine{10000, "嵐で星砂島・灯台島が孤立しました。", []string{"Network Partition"}},
		},
		{
			"storm routes",
			StoryFact{T: 20000, Kind: StoryStorm, Routes: [][2]uint64{{1, 2}, {2, 3}}, Isolated: []uint64{}},
			StoryLine{20000, "嵐で星砂島と月影島の間、月影島と灯台島の間の航路が止まりました。", []string{"Network Partition"}},
		},
		{
			"stepped_down check_quorum",
			StoryFact{T: 72000, Kind: StorySteppedDown, Island: 1, Term: 2, Reason: "check_quorum"},
			StoryLine{72000, "星砂島は過半数の島と連絡が取れなくなったので、自分から代表を降りました。", []string{"CheckQuorum"}},
		},
		{
			"stepped_down higher_term",
			StoryFact{T: 80000, Kind: StorySteppedDown, Island: 2, Term: 3, Reason: "higher_term"},
			StoryLine{80000, "月影島は、もっと新しい任期（Term）の手紙を受け取ったので、代表を降りました。", []string{"Term"}},
		},
		{
			"leader_elected",
			StoryFact{T: 91000, Kind: StoryLeaderElected, Island: 3, Term: 3},
			StoryLine{91000, "灯台島が Term 3 の新しい代表に選ばれました。", []string{"Leader Election"}},
		},
		{
			"first_commit",
			StoryFact{T: 9000, Kind: StoryFirstCommit, Island: 1, Resource: "stall:main"},
			StoryLine{9000, "星砂島の屋台の出店枠の依頼が過半数の島の台帳に記録され、共和国で確定しました。", []string{"Log Replication", "Commit", "Quorum"}},
		},
		{
			"rejected already_reserved",
			StoryFact{T: 9100, Kind: StoryRejected, Island: 1, Resource: "firework:a", Reason: "already_reserved"},
			StoryLine{9100, "星砂島の大花火の予約は、先に別の予約が確定していたので取れませんでした。どの島の台帳も同じ順に記録するので、結果はどの島でも同じです。", []string{"Replicated State Machine"}},
		},
		{
			"rejected full",
			StoryFact{T: 9200, Kind: StoryRejected, Island: 2, Resource: "stall:main", Reason: "full"},
			StoryLine{9200, "月影島の屋台の出店枠の予約は、先に確定した予約で枠が埋まっていたので取れませんでした。どの島の台帳も同じ順に記録するので、結果はどの島でも同じです。", []string{"Replicated State Machine"}},
		},
		{
			"duplicate",
			StoryFact{T: 33000, Kind: StoryDuplicate, Island: 1, Resource: "stall:main"},
			StoryLine{33000, "星砂島の役場が屋台の出店枠の依頼を送り直したので、同じ依頼が台帳に 2 回記録されました。2 回目は処理済みとして扱われ、二重に予約されませんでした。", []string{"Idempotency"}},
		},
		{
			"stale_ignored",
			StoryFact{T: 130000, Kind: StoryStaleIgnored, Island: 1, Term: 2},
			StoryLine{130000, "星砂島が出した古い任期（Term 2）の手紙が届きましたが、受け取った島は無視しました。", []string{"Term"}},
		},
		{
			"prevote_rejected",
			StoryFact{T: 140000, Kind: StoryPreVoteRejected, Island: 1},
			StoryLine{140000, "星砂島は代表と連絡が取れず、立候補してよいかほかの島に相談しましたが、断られました。", []string{"PreVote"}},
		},
		{
			"silence",
			StoryFact{T: 0, Kind: StorySilence, Island: 3, DurationMs: 41999},
			StoryLine{0, "灯台島には 41 秒のあいだ、手紙が 1 通も届きませんでした。", []string{"Heartbeat", "Election Timeout"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := WriteStory([]StoryFact{tt.fact}, storyTestNames())
			if !reflect.DeepEqual(got, []StoryLine{tt.want}) {
				t.Errorf("WriteStory(%+v) =\n %+v\nwant\n %+v", tt.fact, got, []StoryLine{tt.want})
			}
		})
	}
}

func storyTimes(lines []StoryLine) []int64 {
	out := []int64{}
	for _, l := range lines {
		out = append(out, l.T)
	}
	return out
}

func TestWriteStoryOrderAndLimit(t *testing.T) { // G-2
	t.Parallel()
	names := storyTestNames()
	facts := []StoryFact{
		{T: 30000, Kind: StoryLeaderElected, Island: 3, Term: 3},
		{T: 10000, Kind: StoryLeaderElected, Island: 1, Term: 1},
		{T: 20000, Kind: StoryStaleIgnored, Island: 2, Term: 2},
		{T: 20000, Kind: StoryPreVoteRejected, Island: 1},
	}
	got := WriteStory(facts, names)
	if want := []int64{10000, 20000, 20000, 30000}; !reflect.DeepEqual(storyTimes(got), want) {
		t.Fatalf("times = %v, want %v", storyTimes(got), want)
	}
	if got[1].Concepts[0] != "Term" || got[2].Concepts[0] != "PreVote" {
		t.Errorf("same T order = %+v, want stale_ignored then prevote_rejected", got[1:3])
	}

	var many []StoryFact
	for i := range int64(3) {
		many = append(many,
			StoryFact{T: 1000 * (3 - i), Kind: StoryStorm, Isolated: []uint64{1}},
			StoryFact{T: 1000*(3-i) + 100, Kind: StoryLeaderElected, Island: 2, Term: uint64(3 + i)},
		)
	}
	many = append(many,
		StoryFact{T: 500, Kind: StoryFirstCommit, Island: 1, Resource: "stall:main"},
		StoryFact{T: 200, Kind: StoryFirstCommit, Island: 2, Resource: "stall:main"},
	)
	got = WriteStory(many, names)
	count := map[string]int{}
	for _, l := range got {
		count[l.Concepts[0]]++
	}
	if want := map[string]int{"Network Partition": 2, "Leader Election": 2, "Log Replication": 1}; !reflect.DeepEqual(count, want) {
		t.Errorf("counts = %v, want %v (%+v)", count, want, got)
	}
	if want := []int64{200, 1000, 1100, 2000, 2100}; !reflect.DeepEqual(storyTimes(got), want) {
		t.Errorf("times = %v, want %v", storyTimes(got), want)
	}
	if got[0].Text != "月影島の屋台の出店枠の依頼が過半数の島の台帳に記録され、共和国で確定しました。" {
		t.Errorf("first_commit = %q, want the earliest one", got[0].Text)
	}
}

func TestWriteStoryNoNames(t *testing.T) { // G-3
	t.Parallel()
	facts := []StoryFact{
		{T: 1, Kind: StoryLeaderElected, Island: 7, Term: 2},
		{T: 2, Kind: StoryPreVoteRejected, Island: 0},
		{T: 3, Kind: StoryFirstCommit, Island: 2, Resource: "boat:x"},
	}
	got := WriteStory(facts, StoryNames{})
	if len(got) != 3 ||
		got[0].Text != "島 7が Term 2 の新しい代表に選ばれました。" ||
		got[1].Text != "島 0は代表と連絡が取れず、立候補してよいかほかの島に相談しましたが、断られました。" ||
		got[2].Text != "島 2のboat:xの依頼が過半数の島の台帳に記録され、共和国で確定しました。" {
		t.Errorf("zero names = %+v", got)
	}
	// 名前があっても、範囲外の島・知らない資源・空の名前はそれぞれの既定の言葉にする。
	names := StoryNames{Islands: []string{"星砂島", ""}, Resources: map[string]string{"stall:main": ""}}
	got = WriteStory([]StoryFact{
		{T: 1, Kind: StoryDuplicate, Island: 2, Resource: "stall:main"},
		{T: 2, Kind: StoryRejected, Island: 4, Resource: "firework:a", Reason: "already_reserved"},
	}, names)
	if len(got) != 2 ||
		got[0].Text != "島 2の役場がstall:mainの依頼を送り直したので、同じ依頼が台帳に 2 回記録されました。2 回目は処理済みとして扱われ、二重に予約されませんでした。" ||
		got[1].Text != "島 4のfirework:aの予約は、先に別の予約が確定していたので取れませんでした。どの島の台帳も同じ順に記録するので、結果はどの島でも同じです。" {
		t.Errorf("partial names = %+v", got)
	}
}

func TestWriteStoryDropsUnwritable(t *testing.T) { // G-4
	t.Parallel()
	facts := []StoryFact{
		{T: 1, Kind: "unknown", Island: 1},
		{T: 2, Kind: StorySteppedDown, Island: 1, Reason: "x"},
		{T: 3, Kind: StorySteppedDown, Island: 1, Reason: "x"},
		{T: 4, Kind: StoryRejected, Island: 1, Resource: "firework:a", Reason: "invalid_command"},
		{T: 5, Kind: StoryStorm},
		{T: 6, Kind: StoryStorm, Routes: [][2]uint64{}, Isolated: []uint64{}},
		{T: 7, Kind: StoryStorm},
		{T: 10, Kind: StorySteppedDown, Island: 2, Reason: "check_quorum"},
		{T: 11, Kind: StorySteppedDown, Island: 3, Reason: "higher_term"},
		{T: 12, Kind: StoryRejected, Island: 2, Resource: "stall:main", Reason: "full"},
		{T: 13, Kind: StoryStorm, Isolated: []uint64{1}},
		{T: 14, Kind: StoryStorm, Routes: [][2]uint64{{1, 2}}},
	}
	got := WriteStory(facts, storyTestNames())
	if want := []int64{10, 11, 12, 13, 14}; !reflect.DeepEqual(storyTimes(got), want) {
		t.Errorf("times = %v, want %v (%+v)", storyTimes(got), want, got)
	}
}

func TestWriteStoryEmptyAndShared(t *testing.T) { // G-5
	t.Parallel()
	got := WriteStory(nil, StoryNames{})
	if got == nil {
		t.Fatal("WriteStory(nil) = nil")
	}
	if b, err := json.Marshal(got); err != nil || string(b) != "[]" {
		t.Errorf("json = %s, %v; want []", b, err)
	}

	facts := []StoryFact{
		{T: 20, Kind: StoryLeaderElected, Island: 2, Term: 3},
		{T: 10, Kind: StoryStorm, Isolated: []uint64{3}},
	}
	orig := slices.Clone(facts)
	first := WriteStory(facts, storyTestNames())
	if !reflect.DeepEqual(facts, orig) {
		t.Errorf("facts changed: %+v, want %+v", facts, orig)
	}
	for _, l := range first {
		if len(l.Concepts) == 0 {
			t.Errorf("concepts of %+v is empty", l)
		}
		l.Concepts[0] = "changed"
	}
	second := WriteStory(facts, storyTestNames())
	if second[0].Concepts[0] != "Network Partition" || second[1].Concepts[0] != "Leader Election" {
		t.Errorf("second call = %+v, affected by the first result", second)
	}
}

func TestTermRanges(t *testing.T) { // G-6
	t.Parallel()
	got := TermRanges(nil)
	if got == nil {
		t.Fatal("TermRanges(nil) = nil")
	}
	if b, err := json.Marshal(got); err != nil || string(b) != "[]" {
		t.Errorf("json = %s, %v; want []", b, err)
	}
	tests := []struct {
		in   []uint64
		want [][3]uint64
	}{
		{[]uint64{1, 1, 1, 2, 2, 5}, [][3]uint64{{1, 1, 3}, {2, 4, 5}, {5, 6, 6}}},
		{[]uint64{3}, [][3]uint64{{3, 1, 1}}},
	}
	for _, tt := range tests {
		if got := TermRanges(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("TermRanges(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
