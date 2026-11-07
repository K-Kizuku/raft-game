package sim

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
)

// statsLogTerms は e のノード i のログの Term を帯にしたもの（TechStats を通さずに計算する）。
func statsLogTerms(t *testing.T, e *Engine, i int) [][3]uint64 {
	t.Helper()
	ents, err := e.nodes[i].Log()
	if err != nil {
		t.Fatalf("node %d: Log: %v", i+1, err)
	}
	terms := make([]uint64, len(ents))
	for j, ent := range ents {
		terms[j] = ent.Term
	}
	return game.TermRanges(terms)
}

// statsCheckEmpty は、統計が開始の前・直後の値であることを確かめる（ST-1）。
func statsCheckEmpty(t *testing.T, e *Engine, wantTerms [][3]uint64) {
	t.Helper()
	ts := e.TechStats()
	if ts.LettersDelivered == nil || len(ts.LettersDelivered) != 0 {
		t.Errorf("LettersDelivered = %#v, want empty non-nil map", ts.LettersDelivered)
	}
	if ts.LeaderChanges != 0 || ts.AvgCommitLatencyMs != 0 || ts.LongestSilenceMs != 0 {
		t.Errorf("TechStats = %+v, want zero counts", ts)
	}
	if len(ts.FinalLogs) != 3 {
		t.Fatalf("FinalLogs = %+v, want 3", ts.FinalLogs)
	}
	for i, fl := range ts.FinalLogs {
		st := e.nodes[i].Status()
		want := FinalLog{ID: uint64(i + 1), LastIndex: st.LastIndex, Commit: st.Commit, Terms: statsLogTerms(t, e, i)}
		if !reflect.DeepEqual(fl, want) {
			t.Errorf("FinalLogs[%d] = %+v, want %+v", i, fl, want)
		}
		if !reflect.DeepEqual(fl.Terms, wantTerms) {
			t.Errorf("FinalLogs[%d].Terms = %v, want %v", i, fl.Terms, wantTerms)
		}
	}
	if f := e.StoryFacts(); f == nil || len(f) != 0 {
		t.Errorf("StoryFacts() = %#v, want empty non-nil", f)
	}
}

// statsOpeningTerms は testConfig() の開会式の後の、各島のログの帯（Log() で確かめた値）。
var statsOpeningTerms = [][3]uint64{{1, 1, 3}, {2, 4, 4}}

func TestStatsBeforeStart(t *testing.T) { // ST-1
	t.Parallel()
	e := newEngine(t, testConfig(), Options{CheckInvariants: true})
	for i := range e.nodes {
		t.Logf("island %d: status %+v, terms %v", i+1, e.nodes[i].Status(), statsLogTerms(t, e, i))
	}
	statsCheckEmpty(t, e, statsOpeningTerms)
	mustApply(t, e, Command{Type: CmdGameStart})
	statsCheckEmpty(t, e, statsOpeningTerms)
}

// statsE1 は ST-2 の Engine（E-1 の運航で 120000 まで）。
func statsE1(t *testing.T, score bool) (*Engine, []DomainEvent) {
	t.Helper()
	cfg := scoreTestConfig(stallReq(0, 1))
	cfg.Score.SilenceAfterMs = 30000
	if !score {
		cfg.Score = game.ScoreConfig{}
	}
	return e1Events(t, cfg, 120000)
}

func TestStatsE1(t *testing.T) { // ST-2
	t.Parallel()
	e, _ := statsE1(t, true)
	ts := e.TechStats()
	if want := map[string]int64{"append": 1, "append_resp": 1}; !reflect.DeepEqual(ts.LettersDelivered, want) {
		t.Errorf("LettersDelivered = %v, want %v", ts.LettersDelivered, want)
	}
	if ts.LeaderChanges != 0 || ts.AvgCommitLatencyMs != 9000 || ts.LongestSilenceMs != 120000 {
		t.Errorf("TechStats = %+v, want leaderChanges 0, avgCommitLatencyMs 9000, longestSilenceMs 120000", ts)
	}
	// ST-1 の帯（statsOpeningTerms）に index 5 の Term 2 を足したもの。
	wantTerms := [][3]uint64{{1, 1, 3}, {2, 4, 5}}
	if want := (FinalLog{ID: 1, LastIndex: 5, Commit: 5, Terms: wantTerms}); len(ts.FinalLogs) != 3 || !reflect.DeepEqual(ts.FinalLogs[0], want) {
		t.Errorf("FinalLogs = %+v, want [0] = %+v", ts.FinalLogs, want)
	}
	wantFacts := []game.StoryFact{
		{T: 0, Kind: game.StorySilence, Island: 3, DurationMs: 120000},
		{T: 9000, Kind: game.StoryFirstCommit, Island: 1, Resource: "stall:main"},
	}
	if got := e.StoryFacts(); !reflect.DeepEqual(got, wantFacts) {
		t.Errorf("StoryFacts() = %+v, want %+v", got, wantFacts)
	}

	e, _ = statsE1(t, false)
	if got, want := e.StoryFacts(), wantFacts[1:]; !reflect.DeepEqual(got, want) {
		t.Errorf("StoryFacts() without score = %+v, want %+v", got, want)
	}
}

// statsLeaderChangeConfig は ST-3 の設定（testConfig に船 f2 を足し、ラウンドを 240000 にする）。
func statsLeaderChangeConfig() Config {
	cfg := testConfig()
	cfg.Ferries = append(cfg.Ferries, ferry.FerryConfig{ID: "f2", Home: 2, Capacity: 4, SpeedPermille: 1000})
	cfg.DurationMs = 240000
	return cfg
}

// statsLeaderChange は ST-3 の台本。前半は f2 だけが 2-3 を巡回し（島 1 は孤立する）、後半は f1 が 1-2 を巡回する。
// 終わった Engine と、New からの全イベントを返す。
func statsLeaderChange(t *testing.T, seed uint64) (*Engine, []DomainEvent) {
	t.Helper()
	cfg := statsLeaderChangeConfig()
	e := scnStart(t, cfg, seed)
	r := &scnRec{}
	tours := []*scnTour{{ferry: "f2", stops: []uint64{2, 3}}}
	scnRun(t, e, r, 120000, tours, nil)
	tours = append(tours, &scnTour{ferry: "f1", stops: []uint64{1, 2}})
	scnRun(t, e, r, 239999, tours, nil)
	e.RunUntil(240000)
	r.drain(e)
	if e.Phase() != PhaseEnded {
		t.Fatalf("phase = %s, want ended", e.Phase())
	}
	return e, r.evs
}

// statsRecount はイベント列から、手紙の区分ごとの配達の数・航行中の Leader の誕生の数・最長の音信不通を数え直す。
func statsRecount(evs []DomainEvent, islands int, end SimTime) (map[string]int64, int64, int64) {
	labels := scnLetters(evs)
	letters := map[string]int64{}
	var leaders int64
	last := make([]SimTime, islands+1)
	var longest SimTime
	sailing := false
	for _, ev := range evs {
		switch d := ev.Data.(type) {
		case PhaseChanged:
			sailing = d.Phase == PhaseSailing
		case LeaderElected:
			if sailing {
				leaders++
			}
		case LetterDelivered:
			letters[scnLetter(labels, d.LetterID).Label]++
			longest = max(longest, ev.T-last[d.IslandID])
			last[d.IslandID] = ev.T
		}
	}
	for i := 1; i <= islands; i++ {
		longest = max(longest, end-last[i])
	}
	return letters, leaders, int64(longest)
}

// statsFactsOf は facts のうち kind のものを返す。
func statsFactsOf(facts []game.StoryFact, kind game.StoryKind) []game.StoryFact {
	var out []game.StoryFact
	for _, f := range facts {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

func TestStatsLeaderChange(t *testing.T) { // ST-3
	t.Parallel()
	for _, seed := range []uint64{1, 2, 3} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			t.Parallel()
			e, evs := statsLeaderChange(t, seed)
			facts := e.StoryFacts()
			for _, f := range facts {
				t.Logf("seed %d fact: %+v", seed, f)
			}

			iDown := slices.IndexFunc(facts, func(f game.StoryFact) bool {
				return f.Kind == game.StorySteppedDown && f.Island == 1 && f.Term == 2 && f.Reason == "check_quorum"
			})
			if iDown < 0 {
				t.Fatalf("no stepped_down of island 1 in %+v", facts)
			}
			iElected := slices.IndexFunc(facts[iDown+1:], func(f game.StoryFact) bool {
				return f.Kind == game.StoryLeaderElected && (f.Island == 2 || f.Island == 3) && f.Term >= 3
			})
			if iElected < 0 {
				t.Errorf("no leader_elected of island 2 or 3 after stepped_down in %+v", facts)
			}

			ts := e.TechStats()
			letters, leaders, longest := statsRecount(evs, 3, 240000)
			if ts.LeaderChanges != leaders || leaders < 1 {
				t.Errorf("LeaderChanges = %d, recount %d (want >= 1)", ts.LeaderChanges, leaders)
			}
			if !reflect.DeepEqual(ts.LettersDelivered, letters) {
				t.Errorf("LettersDelivered = %v, recount %v", ts.LettersDelivered, letters)
			}
			if ts.LongestSilenceMs != longest {
				t.Errorf("LongestSilenceMs = %d, recount %d", ts.LongestSilenceMs, longest)
			}

			stale := statsFactsOf(facts, game.StoryStaleIgnored)
			var wantStale []game.StoryFact
			if ds := scnStaleDeliveries(evs, 0, 3); len(ds) > 0 {
				wantStale = []game.StoryFact{{Kind: game.StoryStaleIgnored, Island: ds[0].From, Term: ds[0].Raft.Term}}
			}
			if len(stale) != len(wantStale) || (len(stale) == 1 && (stale[0].Island != wantStale[0].Island || stale[0].Term != wantStale[0].Term)) {
				t.Errorf("stale_ignored = %+v, want %+v", stale, wantStale)
			}

			pre := statsFactsOf(facts, game.StoryPreVoteRejected)
			var wantPre []uint64
			ls := scnLetters(evs)
			for _, d := range ofType[LetterDelivered](evs) {
				if l := scnLetter(ls, d.LetterID); l.Label == "prevote_resp" && l.Raft != nil && l.Raft.Reject {
					wantPre = []uint64{d.IslandID}
					break
				}
			}
			if len(pre) != len(wantPre) || (len(pre) == 1 && pre[0].Island != wantPre[0]) {
				t.Errorf("prevote_rejected = %+v, want island %v", pre, wantPre)
			}
			if len(stale)+len(pre) == 0 {
				t.Errorf("neither stale_ignored nor prevote_rejected in %+v", facts)
			}
		})
	}
}

func TestStatsStorm(t *testing.T) { // ST-4
	t.Parallel()
	e := newStarted(t, stormTestConfig(
		stormAt(10000, 30000, 0, [2]uint64{3, 2}),
		stormAt(20000, 30000, 0, [2]uint64{2, 3}),
		stormAt(70000, 10000, 0, [2]uint64{1, 2}),
	))
	e.RunUntil(120000)
	want := []game.StoryFact{
		{T: 10000, Kind: game.StoryStorm, Routes: [][2]uint64{{2, 3}}, Isolated: []uint64{3}},
		{T: 70000, Kind: game.StoryStorm, Routes: [][2]uint64{{1, 2}}, Isolated: []uint64{1}},
	}
	if got := statsFactsOf(e.StoryFacts(), game.StoryStorm); !reflect.DeepEqual(got, want) {
		t.Errorf("storm facts = %+v, want %+v", got, want)
	}
}

// statsAppliedAt は evs のうち、result が result の最初の ledger.applied の時刻（無ければ -1）。
func statsAppliedAt(evs []DomainEvent, result string) SimTime {
	for _, ev := range evs {
		if d, ok := ev.Data.(LedgerApplied); ok && d.Result == result {
			return ev.T
		}
	}
	return -1
}

func TestStatsRejectedAndDuplicate(t *testing.T) { // ST-5
	t.Parallel()
	t.Run("rejected", func(t *testing.T) {
		t.Parallel()
		// #31 の S-4 と同じ運航（花火の依頼 2 件）。
		e := newStarted(t, scoreTestConfig(fireworkReq(0, 1), fireworkReq(100, 1)))
		e.RunUntil(100)
		all := e.DrainEvents()
		mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: raftLetters(e, 1, 2, pb.MsgApp)})
		mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
		e.RunUntil(3850)
		mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: raftLetters(e, 2, 1, pb.MsgAppResp)})
		e.RunUntil(5350)
		mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 1})
		e.RunUntil(9100)
		all = append(all, e.DrainEvents()...)
		ts := statsAppliedAt(all, "rejected")
		if ts < 0 {
			t.Fatal("no rejected ledger.applied")
		}
		want := []game.StoryFact{{T: int64(ts), Kind: game.StoryRejected, Island: 1, Resource: "firework:a", Reason: "already_reserved"}}
		if got := statsFactsOf(e.StoryFacts(), game.StoryRejected); !reflect.DeepEqual(got, want) {
			t.Errorf("rejected facts = %+v, want %+v", got, want)
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		t.Parallel()
		// #30 の E-4 と同じ運航（依頼元が Leader のまま再送）。
		e := reqStarted(t, reqTestConfig(stallReq(0, 1)))
		e.RunUntil(25000)
		all := e.DrainEvents()
		all = append(all, load(t, e, raftLetters(e, 1, 2, pb.MsgApp))...)
		all = append(all, sail(t, e, 2)...)
		all = append(all, load(t, e, raftLetters(e, 2, 1, pb.MsgAppResp))...)
		all = append(all, sail(t, e, 1)...)
		ts := statsAppliedAt(all, "duplicate")
		if ts < 0 {
			t.Fatal("no duplicate ledger.applied")
		}
		want := []game.StoryFact{{T: int64(ts), Kind: game.StoryDuplicate, Island: 1, Resource: "stall:main"}}
		if got := statsFactsOf(e.StoryFacts(), game.StoryDuplicate); !reflect.DeepEqual(got, want) {
			t.Errorf("duplicate facts = %+v, want %+v", got, want)
		}
	})
}

func TestStatsDoesNotChangeEngine(t *testing.T) { // ST-6
	t.Parallel()
	e, _ := statsE1(t, true)
	hash, input := e.TraceHash(), e.InputLog()
	res, ok := e.Result()
	seq := e.seq

	ts1, f1 := e.TechStats(), e.StoryFacts()
	ts1.LettersDelivered["append"] = 99
	ts1.LettersDelivered["x"] = 1
	ts1.FinalLogs[0].Terms[0][0] = 99
	ts1.FinalLogs[0] = FinalLog{}
	f1[0].Island = 99
	f1[1].Resource = "x"

	ts2, f2 := e.TechStats(), e.StoryFacts()
	ts3, f3 := e.TechStats(), e.StoryFacts()
	if !reflect.DeepEqual(ts2, ts3) || !reflect.DeepEqual(f2, f3) {
		t.Errorf("second and third calls differ: %+v / %+v, %+v / %+v", ts2, ts3, f2, f3)
	}
	if ts2.LettersDelivered["append"] != 1 || len(ts2.LettersDelivered) != 2 || ts2.FinalLogs[0].ID != 1 || ts2.FinalLogs[0].Terms[0][0] != 1 {
		t.Errorf("TechStats() affected by the previous result: %+v", ts2)
	}
	if f2[0].Island != 3 || f2[1].Resource != "stall:main" {
		t.Errorf("StoryFacts() affected by the previous result: %+v", f2)
	}

	if e.TraceHash() != hash || !reflect.DeepEqual(e.InputLog(), input) || e.seq != seq {
		t.Error("TechStats/StoryFacts changed the trace, the input log or the events")
	}
	if n := len(e.DrainEvents()); n != 0 {
		t.Errorf("DrainEvents() = %d events after TechStats/StoryFacts, want 0", n)
	}
	if res2, ok2 := e.Result(); ok2 != ok || !reflect.DeepEqual(res2, res) {
		t.Errorf("Result() = %+v %v, want %+v %v", res2, ok2, res, ok)
	}
}

func TestStatsDeterministicAndReplay(t *testing.T) { // ST-7
	t.Parallel()
	a, _ := statsLeaderChange(t, 1)
	b, _ := statsLeaderChange(t, 1)
	if a.TraceHash() != b.TraceHash() {
		t.Fatal("TraceHash differs between two runs")
	}
	if !reflect.DeepEqual(a.TechStats(), b.TechStats()) || !reflect.DeepEqual(a.StoryFacts(), b.StoryFacts()) {
		t.Errorf("two runs differ:\n %+v\n %+v\n %+v\n %+v", a.TechStats(), b.TechStats(), a.StoryFacts(), b.StoryFacts())
	}

	cfg := statsLeaderChangeConfig()
	r := mustReplay(t, cfg, rpLog(1, a.InputLog(), cfg.DurationMs), Options{})
	if r.TraceHash() != a.TraceHash() {
		t.Fatal("replayed TraceHash differs")
	}
	if !reflect.DeepEqual(r.TechStats(), a.TechStats()) || !reflect.DeepEqual(r.StoryFacts(), a.StoryFacts()) {
		t.Errorf("replay differs:\n %+v\n %+v\n %+v\n %+v", r.TechStats(), a.TechStats(), r.StoryFacts(), a.StoryFacts())
	}
}
