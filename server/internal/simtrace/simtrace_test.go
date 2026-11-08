package simtrace

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/bot"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// ---- 参照モデルを動かす補助 ----

var (
	stagesOnce sync.Once
	stagesErr  error
	stagesByID map[string]*stage.Stage
)

// repoStage はリポジトリの data/ のステージを読む。data/islands.json がなければ試験を飛ばす。
func repoStage(t *testing.T, id string) *stage.Stage {
	t.Helper()
	if _, err := os.Stat("../../../data/islands.json"); errors.Is(err, fs.ErrNotExist) {
		t.Skip("data/islands.json がない")
	}
	stagesOnce.Do(func() {
		var list []*stage.Stage
		list, stagesErr = stage.LoadDir(os.DirFS("../../.."), "data/islands.json", "data/stages")
		stagesByID = map[string]*stage.Stage{}
		for _, st := range list {
			stagesByID[st.ID] = st
		}
	})
	if stagesErr != nil {
		t.Fatal(stagesErr)
	}
	st, ok := stagesByID[id]
	if !ok {
		t.Fatalf("ステージ %s がない", id)
	}
	return st
}

// play は参照モデル（sim.Engine）でボットに 1 ラウンド遊ばせ、イベントの列とラウンドの結果を返す。
func play(t *testing.T, stageID, botName string, seed uint64) ([]sim.DomainEvent, sim.RoundResult) {
	t.Helper()
	st := repoStage(t, stageID)
	cfg, err := stage.SimConfig(st)
	if err != nil {
		t.Fatal(err)
	}
	e, err := sim.New(cfg, seed, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := bot.New(botName, seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := bot.Play(e, b); err != nil {
		t.Fatal(err)
	}
	r, ok := e.Result()
	if !ok {
		t.Fatalf("%s %s seed=%d: ラウンドの結果がない", stageID, botName, seed)
	}
	return e.DrainEvents(), r
}

// labels は各イベントの島・船の列の名前を返す（global・score なら ""）。
func labels(events []sim.DomainEvent) []string {
	n := newNormalizer()
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = n.add(ev)
	}
	return out
}

func normalize(events []sim.DomainEvent, r sim.RoundResult) Trace {
	return Normalize(events).WithResult(r)
}

// ---- 参照モデルどうし ----

// 参照モデルどうし（同じ seed を 2 回）の Compare は一致する。JSON を通しても一致する。
func TestCompareReferenceWithItself(t *testing.T) {
	t.Parallel()
	for _, stageID := range []string{"stage1", "stage2", "demo-goto", "demo-izu"} {
		for _, botName := range []string{"greedy", "novice"} {
			for seed := uint64(1); seed <= 2; seed++ {
				ev1, r1 := play(t, stageID, botName, seed)
				ev2, r2 := play(t, stageID, botName, seed)
				ref, got := normalize(ev1, r1), normalize(ev2, r2)
				if d := Compare(ref, got); d != nil {
					t.Fatalf("%s %s seed=%d: %s", stageID, botName, seed, d)
				}
				b, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				var back Trace
				if err := json.Unmarshal(b, &back); err != nil {
					t.Fatal(err)
				}
				if d := Compare(ref, back); d != nil {
					t.Fatalf("%s %s seed=%d: JSON を通すと食い違う: %s", stageID, botName, seed, d)
				}
				if s := streamByName(ref, UnknownStream); s != nil {
					t.Fatalf("%s %s seed=%d: unknown の列に %d 件ある（最初: %s）", stageID, botName, seed, len(s.Events), s.Events[0].Data)
				}
			}
		}
	}
}

// 別の seed のトレースは食い違う（比べ方が何でも一致にしてしまわないこと）。
func TestCompareDifferentSeedsDiffer(t *testing.T) {
	t.Parallel()
	ev1, r1 := play(t, "stage2", "greedy", 1)
	ev2, r2 := play(t, "stage2", "greedy", 2)
	if d := Compare(normalize(ev1, r1), normalize(ev2, r2)); d == nil {
		t.Fatal("seed 1 と 2 が一致した")
	}
}

// 手紙の ID の振り方を変えても（全体の連番 → 島ごとの番号のような付け替え）、列の振り分けは変わらない。
func TestNormalizeIndependentOfLetterIDs(t *testing.T) {
	t.Parallel()
	events, _ := play(t, "stage2", "greedy", 7)
	remapped := remapLetterIDs(t, events, func(id uint64) uint64 { return id*1_000_000_007 + 3 })
	a, b := labels(events), labels(remapped)
	if !slices.Equal(a, b) {
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("イベント %d（%s）の列が %q → %q に変わった", i, events[i].Type, a[i], b[i])
			}
		}
	}
	// 中身の letterId が変わるので、Compare は食い違いを返す（ID も比べる）。
	if d := Compare(Normalize(events), Normalize(remapped)); d == nil {
		t.Fatal("手紙の ID を付け替えたのに一致した")
	}
}

// ---- わざと壊した入力（Q-TRACE） ----

// 島をまたぐイベントの前後（全体の seq の順）を入れ替えても一致する（分散版で順序が変わってよいところ）。
func TestCompareToleratesCrossStreamInterleaving(t *testing.T) {
	t.Parallel()
	events, r := play(t, "stage2", "greedy", 3)
	ref := normalize(events, r)
	lab := labels(events)
	// 別の列（または列と global）のイベントが同じ時刻に隣り合う組。入れ替えても各列の並びは変わらない。
	// 同じ手紙の letter.created と letter.dropped のように、振り分けが前後に依るものは除く。
	var cand []int
	for i := 0; i+1 < len(events); i++ {
		if events[i].T == events[i+1].T && lab[i] != lab[i+1] && !sameLetter(events[i], events[i+1]) {
			cand = append(cand, i)
		}
	}
	if len(cand) == 0 {
		t.Fatal("入れ替えられる組がなかった")
	}
	// 全部を試すと遅い（-race で 40 秒）ので、最大 maxSwaps 組を全体から間引いて試す。
	const maxSwaps = 60
	stride := max(1, len(cand)/maxSwaps)
	swaps := 0
	for k := 0; k < len(cand); k += stride {
		i := cand[k]
		got := slices.Clone(events)
		got[i], got[i+1] = got[i+1], got[i]
		if d := Compare(ref, normalize(got, r)); d != nil {
			t.Fatalf("イベント %d と %d（%s / %s）を入れ替えたら食い違った: %s", i, i+1, lab[i], lab[i+1], d)
		}
		swaps++
	}
	t.Logf("入れ替えた組: %d", swaps)
}

// 1 つの島の列の中で、同じ時刻の 2 つのイベント（別の島に宛てた手紙など）の順を入れ替えると検出する。
func TestCompareDetectsSwapWithinIslandStream(t *testing.T) {
	t.Parallel()
	events, r := play(t, "stage2", "greedy", 3)
	ref := normalize(events, r)
	lab := labels(events)
	i, j := findPair(events, lab, func(a, b sim.DomainEvent) bool {
		ca, okA := a.Data.(sim.LetterCreated)
		cb, okB := b.Data.(sim.LetterCreated)
		return okA && okB && ca.Letter.To != cb.Letter.To // 送信元の島から、別の島に宛てた手紙
	})
	if i < 0 {
		t.Fatal("同じ島・同じ時刻に別の島へ宛てた letter.created の組がない")
	}
	got := slices.Clone(events)
	got[i], got[j] = got[j], got[i]
	d := Compare(ref, normalize(got, r))
	if d == nil {
		t.Fatalf("イベント %d と %d（%s）の入れ替えを見逃した", i, j, lab[i])
	}
	if d.Section != SectionStream || d.Where != lab[i] || d.T != events[i].T {
		t.Fatalf("食い違いの場所が違う: %s（want stream %s t=%d）", d, lab[i], events[i].T)
	}
}

// 1 つの手紙の時刻をずらすと検出する（letter.created・letter.delivered・letter.moved）。
func TestCompareDetectsShiftedLetter(t *testing.T) {
	t.Parallel()
	events, r := play(t, "stage2", "greedy", 3)
	ref := normalize(events, r)
	lab := labels(events)
	for _, typ := range []sim.EventType{sim.EvLetterCreated, sim.EvLetterDelivered, sim.EvLetterMoved, sim.EvLetterDropped} {
		i := slices.IndexFunc(events, func(ev sim.DomainEvent) bool { return ev.Type == typ && ev.T > 0 })
		if i < 0 {
			t.Fatalf("%s がない", typ)
		}
		got := slices.Clone(events)
		got[i].T++ // 並びは変えず、時刻だけ 1 ms ずらす
		d := Compare(ref, normalize(got, r))
		if d == nil {
			t.Fatalf("%s（イベント %d）の時刻のずれを見逃した", typ, i)
		}
		if d.Section != SectionStream || d.Where != lab[i] || d.T != events[i].T {
			t.Fatalf("%s: 食い違いの場所が違う: %s（want stream %s t=%d）", typ, d, lab[i], events[i].T)
		}
	}
}

// スコアを変えると検出する。round.result を変えても検出する。
func TestCompareDetectsScoreChange(t *testing.T) {
	t.Parallel()
	events, r := play(t, "stage2", "greedy", 3)
	ref := normalize(events, r)
	// ある時刻の最後の score.updated の合計を変える。
	i := lastAtT(events, sim.EvScoreUpdated, len(events)/2)
	if i < 0 {
		t.Fatal("score.updated がない")
	}
	got := slices.Clone(events)
	s := got[i].Data.(sim.ScoreUpdated)
	s.Total++
	got[i].Data = s
	d := Compare(ref, normalize(got, r))
	if d == nil || d.Section != SectionScore || d.T != events[i].T {
		t.Fatalf("score.updated の変更: %s（want score t=%d）", d, events[i].T)
	}

	r2 := r
	r2.Score.Total++
	d = Compare(ref, normalize(events, r2))
	if d == nil || d.Section != SectionFinal || d.Where != "result" {
		t.Fatalf("round.result の変更: %s（want final result）", d)
	}
	// round.result を渡していない側とは食い違う。
	if d := Compare(ref, Normalize(events)); d == nil || d.Where != "result" {
		t.Fatalf("round.result がない側: %s", d)
	}
}

// Q-TRACE: global（累積しない）は、時刻ごとの多重集合で比べる。
// 同じ時刻の中の順は比べない（設計どおり）。時刻のずれ・増減・重複・中身の違いは検出する。
func TestCompareGlobalQTrace(t *testing.T) {
	t.Parallel()
	events, r := play(t, "stage2", "greedy", 3)
	ref := normalize(events, r)
	lab := labels(events)
	isGlobal := func(k int) bool { return lab[k] == "" && events[k].Type != sim.EvScoreUpdated }

	gi := slices.IndexFunc(events, func(ev sim.DomainEvent) bool { return ev.Type == sim.EvRequestUpdated })
	if gi < 0 {
		t.Fatal("request.updated がない")
	}

	t.Run("時刻をずらす", func(t *testing.T) {
		got := slices.Clone(events)
		got[gi].T++
		if d := Compare(ref, normalize(got, r)); d == nil || d.Section != SectionGlobal || d.T != events[gi].T {
			t.Fatalf("got %s（want global t=%d）", d, events[gi].T)
		}
	})
	t.Run("消す", func(t *testing.T) {
		got := slices.Delete(slices.Clone(events), gi, gi+1)
		if d := Compare(ref, normalize(got, r)); d == nil || d.Section != SectionGlobal {
			t.Fatalf("got %s（want global）", d)
		}
	})
	t.Run("重ねる（集合でなく多重集合で比べる）", func(t *testing.T) {
		got := slices.Insert(slices.Clone(events), gi, events[gi])
		if d := Compare(ref, normalize(got, r)); d == nil || d.Section != SectionGlobal || d.T != events[gi].T {
			t.Fatalf("got %s（want global t=%d）", d, events[gi].T)
		}
	})
	t.Run("中身を変える", func(t *testing.T) {
		got := slices.Clone(events)
		u := got[gi].Data.(sim.RequestUpdated)
		u.Expired = !u.Expired
		got[gi].Data = u
		if d := Compare(ref, normalize(got, r)); d == nil || d.Section != SectionGlobal || d.T != events[gi].T {
			t.Fatalf("got %s（want global t=%d）", d, events[gi].T)
		}
	})
	t.Run("同じ時刻の中の順を変えても一致する", func(t *testing.T) {
		var pairs [][2]int
		for i := range events {
			if !isGlobal(i) {
				continue
			}
			for j := i + 1; j < len(events) && events[j].T == events[i].T; j++ {
				if isGlobal(j) && !sameRequest(events[i], events[j]) {
					pairs = append(pairs, [2]int{i, j})
				}
			}
		}
		const maxSwaps = 40 // 全部を試すと遅いので間引く
		swapped := 0
		for k := 0; k < len(pairs); k += max(1, len(pairs)/maxSwaps) {
			i, j := pairs[k][0], pairs[k][1]
			got := slices.Clone(events)
			got[i], got[j] = got[j], got[i]
			if d := Compare(ref, normalize(got, r)); d != nil {
				t.Fatalf("イベント %d と %d を入れ替えたら食い違った: %s", i, j, d)
			}
			swapped++
		}
		if swapped == 0 {
			t.Fatal("同じ時刻の global の組がない")
		}
	})
	t.Run("最後の request.updated を変えると最終の状態でも検出する", func(t *testing.T) {
		// 最後の request.updated を探し、status を変える。global の集合は t ごとに検出するが、
		// 最終の状態も違うことを compareFinal で確かめる。
		last := -1
		for k, ev := range events {
			if ev.Type == sim.EvRequestUpdated {
				last = k
			}
		}
		got := slices.Clone(events)
		u := got[last].Data.(sim.RequestUpdated)
		u.Status += "-x"
		got[last].Data = u
		gt := normalize(got, r)
		if d := compareFinal(ref.Final, gt.Final); d == nil || !strings.HasPrefix(d.Where, "request:") {
			t.Fatalf("got %s（want final request:…）", d)
		}
	})
}

// Q-TRACE: score.updated は時刻ごとの最後の値だけを比べる。同じ時刻の途中の値の違いは見逃す（設計どおり）。
func TestCompareScoreOnlyLastPerT(t *testing.T) {
	t.Parallel()
	events, r := play(t, "stage2", "greedy", 3)
	ref := normalize(events, r)
	for i := 0; i+1 < len(events); i++ {
		if events[i].Type != sim.EvScoreUpdated || lastAtT(events, sim.EvScoreUpdated, i) == i {
			continue
		}
		got := slices.Clone(events)
		s := got[i].Data.(sim.ScoreUpdated)
		s.Total += 1000
		got[i].Data = s
		if d := Compare(ref, normalize(got, r)); d != nil {
			t.Fatalf("途中の score.updated の違いで食い違った: %s", d)
		}
		return
	}
	t.Skip("同じ時刻に 2 つ以上の score.updated がない")
}

// ---- 振り分けの表（§5.2） ----

func TestStreamAssignment(t *testing.T) {
	t.Parallel()
	ev := func(tm sim.SimTime, d sim.EventData) sim.DomainEvent {
		return sim.DomainEvent{T: tm, Type: d.EventType(), Data: d}
	}
	events := []sim.DomainEvent{
		ev(0, sim.PhaseChanged{Phase: sim.PhaseSailing}),
		ev(0, sim.NodeState{ID: 2, Role: "leader"}),
		ev(0, sim.LeaderElected{ID: 2, Term: 1}),
		ev(0, sim.SteppedDown{ID: 3, Term: 1}),
		ev(1, sim.LedgerApplied{IslandID: 4, Index: 1, ReqID: "r1", Result: "ok"}),
		ev(2, sim.LetterCreated{Letter: sim.LetterView{ID: 7001, Kind: "raft", From: 1, To: 3, Loc: sim.LocView{Type: "port", IslandID: 1}}}),
		ev(2, sim.LetterCreated{Letter: sim.LetterView{ID: 7002, Kind: "raft", From: 1, To: 2, Loc: sim.LocView{Type: "port", IslandID: 1}}}),
		ev(2, sim.LetterDropped{LetterID: 7002, Reason: "superseded"}),
		ev(3, sim.LetterMoved{LetterID: 7001, Loc: sim.LocView{Type: "ferry", FerryID: "f1"}}),
		ev(4, sim.FerryDeparted{FerryID: "f1", From: 1, To: 2}),
		ev(5, sim.FerryArrived{FerryID: "f1", IslandID: 2}),
		ev(5, sim.LetterMoved{LetterID: 7001, Loc: sim.LocView{Type: "relay", IslandID: 2}}),   // 下ろす（経由）
		ev(6, sim.LetterMoved{LetterID: 7001, Loc: sim.LocView{Type: "ferry", FerryID: "f9"}}), // 倉庫から別の船へ
		ev(7, sim.FerryBroken{FerryID: "f9", Until: 9}),
		ev(9, sim.FerryRepaired{FerryID: "f9", Until: 9}),
		ev(10, sim.LetterDelivered{LetterID: 7001, IslandID: 3, Accepted: true}),
		ev(10, sim.LetterStale{LetterID: 7001}),
		ev(10, sim.Forecast{Event: "storm", At: 20, RouteIDs: []string{"1-2"}}),
		ev(20, sim.RouteBlocked{RouteIDs: []string{"1-2"}, Until: 30}),
		ev(30, sim.RouteUnblocked{RouteIDs: []string{"1-2"}, Until: 30}),
		ev(30, sim.RequestCreated{Request: sim.RequestState{ID: "r1", Origin: 4, Status: "received"}}),
		ev(31, sim.RequestUpdated{ID: "r1", Status: "committed", Result: &sim.RequestResult{Status: "ok"}}),
		ev(31, sim.IslandUpdated{ID: 4, Satisfaction: 900}),
		ev(31, sim.ScoreUpdated{Total: 10, CriticalSecured: []string{}}),
		ev(32, sim.LetterDropped{LetterID: 999, Reason: "overflow"}),                        // letter.created のない手紙
		ev(32, sim.LetterMoved{LetterID: 998, Loc: sim.LocView{Type: "port", IslandID: 1}}), // 同上
	}
	want := []string{
		"", "island:2", "island:2", "island:3", "island:4",
		"island:1", "island:1", "island:1",
		"ferry:f1", "ferry:f1", "ferry:f1", "ferry:f1", "ferry:f9", "ferry:f9", "ferry:f9",
		"island:3", "", "", "", "", "", "", "", "",
		UnknownStream, UnknownStream,
	}
	if got := labels(events); !slices.Equal(got, want) {
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("イベント %d（%s）: got %q, want %q", i, events[i].Type, got[i], want[i])
			}
		}
	}

	tr := Normalize(events)
	var names []string
	for _, s := range tr.Streams {
		names = append(names, s.Name)
	}
	if want := []string{"island:1", "island:2", "island:3", "island:4", "ferry:f1", "ferry:f9", UnknownStream}; !slices.Equal(names, want) {
		t.Errorf("列の順: got %v, want %v", names, want)
	}
	if len(tr.Score) != 1 || tr.Score[0].T != 31 {
		t.Errorf("score: %+v", tr.Score)
	}
	if len(tr.Final.Requests) != 1 || string(tr.Final.Requests[0].State) !=
		`{"createdAt":0,"critical":false,"deadline":0,"expired":false,"id":"r1","kind":"","origin":4,"resource":"","result":{"status":"ok"},"status":"committed"}` {
		t.Errorf("依頼の最終の状態: %+v", tr.Final.Requests)
	}
	if len(tr.Final.Ledgers) != 1 || tr.Final.Ledgers[0].IslandID != 4 || len(tr.Final.Ledgers[0].Applied) != 1 {
		t.Errorf("台帳: %+v", tr.Final.Ledgers)
	}
	if len(tr.Final.Islands) != 1 || tr.Final.Islands[0].ID != "4" {
		t.Errorf("島: %+v", tr.Final.Islands)
	}
	if string(tr.Final.Result) != "" {
		t.Errorf("round.result を渡していないのに入っている: %s", tr.Final.Result)
	}
	// global の時刻の数（0・10・20・30・31）。
	if len(tr.Global) != 5 {
		t.Errorf("global の時刻: %d", len(tr.Global))
	}
}

// 最後の結果（台帳・依頼・島）の食い違いを返す。
func TestCompareFinal(t *testing.T) {
	t.Parallel()
	base := Final{
		Result:   json.RawMessage(`{"stars":1}`),
		Ledgers:  []LedgerFinal{{IslandID: 1, Applied: []json.RawMessage{json.RawMessage(`{"index":1}`)}}},
		Requests: []KeyedState{{ID: "r1", State: json.RawMessage(`{"status":"committed"}`)}},
		Islands:  []KeyedState{{ID: "1", State: json.RawMessage(`{"satisfaction":1}`)}},
	}
	if d := compareFinal(base, base); d != nil {
		t.Fatalf("同じもの: %s", d)
	}
	cases := []struct {
		name  string
		edit  func(f *Final)
		where string
	}{
		{"台帳の中身", func(f *Final) {
			f.Ledgers = []LedgerFinal{{IslandID: 1, Applied: []json.RawMessage{json.RawMessage(`{"index":2}`)}}}
		}, "ledger:1"},
		{"台帳の島", func(f *Final) { f.Ledgers = []LedgerFinal{{IslandID: 2, Applied: base.Ledgers[0].Applied}} }, "ledger:1"},
		{"台帳がない", func(f *Final) { f.Ledgers = nil }, "ledger:1"},
		{"依頼の状態", func(f *Final) { f.Requests = []KeyedState{{ID: "r1", State: json.RawMessage(`{"status":"expired"}`)}} }, "request:r1"},
		{"島の状態", func(f *Final) { f.Islands = []KeyedState{{ID: "1", State: json.RawMessage(`{"satisfaction":2}`)}} }, "island:1"},
	}
	for _, c := range cases {
		got := base
		c.edit(&got)
		d := compareFinal(base, got)
		if d == nil || d.Section != SectionFinal || d.Where != c.where || d.T != FinalT {
			t.Errorf("%s: got %s, want final %s", c.name, d, c.where)
		}
	}
}

// 片方にしかない列も食い違いにし、最も早い t の食い違いを返す。
func TestCompareStreamsEarliest(t *testing.T) {
	t.Parallel()
	e := func(tm sim.SimTime, s string) Entry { return Entry{T: tm, Type: "x", Data: json.RawMessage(s)} }
	ref := Trace{Streams: []Stream{
		{Name: "island:1", Events: []Entry{e(1, `1`), e(5, `2`)}},
		{Name: "island:2", Events: []Entry{e(3, `1`)}},
	}}
	got := Trace{Streams: []Stream{
		{Name: "island:1", Events: []Entry{e(1, `1`), e(5, `9`)}},
		{Name: "island:2", Events: []Entry{e(3, `9`)}},
		{Name: "ferry:f1", Events: []Entry{e(4, `1`)}},
	}}
	d := Compare(ref, got)
	if d == nil || d.Where != "island:2" || d.T != 3 || d.Index != 0 {
		t.Fatalf("got %s, want island:2 t=3", d)
	}
	got.Streams[1].Events[0] = e(3, `1`)
	if d := Compare(ref, got); d == nil || d.Where != "ferry:f1" || d.T != 4 || len(d.Ref) != 0 {
		t.Fatalf("got %s, want ferry:f1 t=4", d)
	}
}

// ---- 補助 ----

func streamByName(tr Trace, name string) *Stream {
	for i := range tr.Streams {
		if tr.Streams[i].Name == name {
			return &tr.Streams[i]
		}
	}
	return nil
}

// findPair は、同じ島の列・同じ時刻で、ok を満たす 2 つのイベントの添字を返す。なければ (-1, -1)。
func findPair(events []sim.DomainEvent, lab []string, ok func(a, b sim.DomainEvent) bool) (int, int) {
	for i := range events {
		if !strings.HasPrefix(lab[i], islandPrefix) {
			continue
		}
		for j := i + 1; j < len(events) && events[j].T == events[i].T; j++ {
			if lab[j] == lab[i] && ok(events[i], events[j]) {
				return i, j
			}
		}
	}
	return -1, -1
}

// lastAtT は、from 以降で最初に見つかる typ のイベントの、同じ時刻の最後の typ のイベントの添字を返す。
func lastAtT(events []sim.DomainEvent, typ sim.EventType, from int) int {
	i := -1
	for k := from; k < len(events); k++ {
		if events[k].Type == typ {
			i = k
			break
		}
	}
	if i < 0 {
		return -1
	}
	for k := i + 1; k < len(events) && events[k].T == events[i].T; k++ {
		if events[k].Type == typ {
			i = k
		}
	}
	return i
}

// letterOf は手紙のイベントの手紙の ID を返す（手紙のイベントでなければ 0, false）。
func letterOf(ev sim.DomainEvent) (uint64, bool) {
	switch d := ev.Data.(type) {
	case sim.LetterCreated:
		return d.Letter.ID, true
	case sim.LetterMoved:
		return d.LetterID, true
	case sim.LetterDelivered:
		return d.LetterID, true
	case sim.LetterDropped:
		return d.LetterID, true
	case sim.LetterStale:
		return d.LetterID, true
	}
	return 0, false
}

func sameLetter(a, b sim.DomainEvent) bool {
	x, okA := letterOf(a)
	y, okB := letterOf(b)
	return okA && okB && x == y
}

// sameRequest は、2 つのイベントが同じ依頼の request.created・request.updated か（入れ替えると最終の状態が変わる）。
func sameRequest(a, b sim.DomainEvent) bool {
	id := func(ev sim.DomainEvent) string {
		switch d := ev.Data.(type) {
		case sim.RequestCreated:
			return d.Request.ID
		case sim.RequestUpdated:
			return d.ID
		case sim.IslandUpdated: // 島の最終の状態も同じ理由で除く
			return "island:" + strconv.FormatUint(d.ID, 10)
		}
		return ""
	}
	return id(a) != "" && id(a) == id(b)
}

// remapLetterIDs は、手紙のイベントの手紙の ID を f で付け替えた複製を返す。
func remapLetterIDs(t *testing.T, events []sim.DomainEvent, f func(uint64) uint64) []sim.DomainEvent {
	t.Helper()
	out := slices.Clone(events)
	for i, ev := range out {
		switch d := ev.Data.(type) {
		case sim.LetterCreated:
			d.Letter.ID = f(d.Letter.ID)
			out[i].Data = d
		case sim.LetterMoved:
			d.LetterID = f(d.LetterID)
			out[i].Data = d
		case sim.LetterDelivered:
			d.LetterID = f(d.LetterID)
			out[i].Data = d
		case sim.LetterDropped:
			d.LetterID = f(d.LetterID)
			out[i].Data = d
		case sim.LetterStale:
			d.LetterID = f(d.LetterID)
			out[i].Data = d
		}
	}
	return out
}

// Q-TRACE: 同じ時刻に同じ依頼（島）の更新が 2 つあるとき、global の集合は順を比べない。入れ替えて最後の状態が
// 変われば §5.3 の 4.（依頼・島の最終の状態）で検出する。後で上書きされる途中の順は見逃す（設計どおり）。
// 参照モデルの列では、最後の更新が同じ時刻に 2 つ並ぶ例が見つからなかったので、手で作った列で確かめる。
func TestCompareSameTUpdatesFinal(t *testing.T) {
	t.Parallel()
	ev := func(tm sim.SimTime, d sim.EventData) sim.DomainEvent {
		return sim.DomainEvent{T: tm, Type: d.EventType(), Data: d}
	}
	base := []sim.DomainEvent{
		ev(1, sim.RequestCreated{Request: sim.RequestState{ID: "r1", Origin: 1, Status: "received"}}),
		ev(5, sim.RequestUpdated{ID: "r1", Status: "proposed"}),
		ev(5, sim.RequestUpdated{ID: "r1", Status: "waiting"}),
		ev(5, sim.IslandUpdated{ID: 2, Satisfaction: 900}),
		ev(5, sim.IslandUpdated{ID: 2, Satisfaction: 800}),
	}
	swap := func(evs []sim.DomainEvent, i, j int) []sim.DomainEvent {
		out := slices.Clone(evs)
		out[i], out[j] = out[j], out[i]
		return out
	}
	ref := Normalize(base)
	if d := Compare(ref, Normalize(swap(base, 1, 2))); d == nil || d.Section != SectionFinal || d.Where != "request:r1" {
		t.Errorf("依頼の最後の更新の入れ替え: %s（want final request:r1）", d)
	}
	if d := Compare(ref, Normalize(swap(base, 3, 4))); d == nil || d.Section != SectionFinal || d.Where != "island:2" {
		t.Errorf("島の最後の更新の入れ替え: %s（want final island:2）", d)
	}
	// 後の時刻で上書きされる途中の順は見逃す。
	later := append(slices.Clone(base), ev(6, sim.RequestUpdated{ID: "r1", Status: "committed"}))
	if d := Compare(Normalize(later), Normalize(swap(later, 1, 2))); d != nil {
		t.Errorf("途中の順の入れ替えで食い違った（設計では見逃す）: %s", d)
	}
}
