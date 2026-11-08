package sim

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
)

// breakdownTestConfig は故障のテストの基本の設定（testConfig に Breakdowns を入れたもの）。
func breakdownTestConfig(defs ...game.BreakdownDef) Config {
	c := testConfig()
	c.Breakdowns = defs
	return c
}

// breakAt は at で書いた故障。
func breakAt(at, dur int64, ferryID string) game.BreakdownDef {
	return game.BreakdownDef{WindowStart: at, WindowEnd: at, DurationMs: dur, Ferry: ferryID}
}

// bdEv は故障のイベント（ferry.broken・ferry.repaired）の時刻と中身。
type bdEv struct {
	T    SimTime
	Data EventData
}

// breakdownOnly は evs のうち ferry.broken・ferry.repaired だけを取り出す（Type と Data の型の一致も確かめる）。
func breakdownOnly(t *testing.T, evs []DomainEvent) []bdEv {
	t.Helper()
	var out []bdEv
	for _, ev := range evs {
		switch ev.Data.(type) {
		case FerryBroken, FerryRepaired:
			if ev.Type != ev.Data.EventType() {
				t.Errorf("event %d: Type %s but Data is %T", ev.Seq, ev.Type, ev.Data)
			}
			out = append(out, bdEv{T: ev.T, Data: ev.Data})
		}
	}
	return out
}

func checkBreakdownEvents(t *testing.T, evs []DomainEvent, want []bdEv) {
	t.Helper()
	if got := breakdownOnly(t, evs); !reflect.DeepEqual(got, want) {
		t.Errorf("故障のイベント:\n got  %+v\n want %+v", got, want)
	}
}

func brokenEv(t, until SimTime, id string) bdEv {
	return bdEv{T: t, Data: FerryBroken{FerryID: id, Until: until}}
}

func repairedEv(t SimTime, id string) bdEv {
	return bdEv{T: t, Data: FerryRepaired{FerryID: id, Until: t}}
}

func checkBrokenUntil(t *testing.T, e *Engine, i int, want *SimTime) {
	t.Helper()
	got := e.Snapshot().Ferries[i].BrokenUntil
	if (got == nil) != (want == nil) || (got != nil && *got != *want) {
		t.Errorf("t=%d: Snapshot().Ferries[%d].BrokenUntil = %v, want %v", e.Now(), i, ptrStr(got), ptrStr(want))
	}
}

func ptrStr(p *SimTime) any {
	if p == nil {
		return nil
	}
	return *p
}

// SB-1・SB-6 停泊中の船が壊れ、直るまで出航できない（積み下ろしはできる）。
func TestBreakdownDocked(t *testing.T) {
	t.Parallel()
	e := startedEngineWith(t, breakdownTestConfig(breakAt(10000, 20000, "f1")), Options{CheckInvariants: true})
	checkNotable(t, e, 10000, true)
	checkBrokenUntil(t, e, 0, nil)

	checkBreakdownEvents(t, runAll(e, 10000), []bdEv{brokenEv(10000, 30000, "f1")})
	until := SimTime(30000)
	checkBrokenUntil(t, e, 0, &until)
	checkNotable(t, e, 30000, true)

	e.RunUntil(15000)
	mustFailCode(t, e, departCmd(2), CodeFerryBroken)
	// 積み下ろしはできる。
	mustApply(t, e, Command{Type: CmdFerryAutoload, FerryID: "f1", To: 2})
	f, _ := e.tr.Ferry("f1")
	if len(f.Cargo) == 0 {
		t.Fatal("autoload で何も積んでいない")
	}
	mustApply(t, e, Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: []uint64{uint64(f.Cargo[0])}})
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{uint64(f.Cargo[0])}})
	e.DrainEvents()

	e.RunUntil(29999)
	mustFailCode(t, e, departCmd(2), CodeFerryBroken)
	checkBreakdownEvents(t, runAll(e, 30000), []bdEv{repairedEv(30000, "f1")})
	checkBrokenUntil(t, e, 0, nil)
	// 修理の時刻ちょうどから出航できる。
	mustApply(t, e, departCmd(2))
	checkBreakdownEvents(t, runAll(e, 120000), nil)
}

// 故障は停泊時間の前でも ferry_not_ready より先。
func TestBreakdownBeforeNotReady(t *testing.T) {
	t.Parallel()
	// 3750 に島 2 に着き、5250 まで停泊。4000 に壊れて 4500 に直る。
	e := startedEngineWith(t, breakdownTestConfig(breakAt(4000, 500, "f1")), Options{CheckInvariants: true})
	mustApply(t, e, departCmd(2))
	e.RunUntil(4200)
	mustFailCode(t, e, departCmd(1), CodeFerryBroken)
	e.RunUntil(4500)
	mustFailCode(t, e, departCmd(1), CodeFerryNotReady)
	e.RunUntil(5250)
	mustApply(t, e, departCmd(1))
}

// SB-2 航行中の船は、着いたときに壊れる（長さは着いた時刻から）。
func TestBreakdownSailing(t *testing.T) {
	t.Parallel()
	e := startedEngineWith(t, breakdownTestConfig(breakAt(10000, 5000, "f1")), Options{CheckInvariants: true})
	e.RunUntil(9000)
	mustApply(t, e, departCmd(2))
	e.DrainEvents()
	evs := runAll(e, 12749)
	checkBreakdownEvents(t, evs, nil)
	checkBrokenUntil(t, e, 0, nil)
	// 開始は処理済み。着くのが次の出来事。
	checkNotable(t, e, 12750, true)

	evs = runAll(e, 12750)
	checkBreakdownEvents(t, evs, []bdEv{brokenEv(12750, 17750, "f1")})
	ai := slices.IndexFunc(evs, func(ev DomainEvent) bool { return ev.Type == EvFerryArrived })
	bi := slices.IndexFunc(evs, func(ev DomainEvent) bool { return ev.Type == EvFerryBroken })
	if ai < 0 || bi != ai+1 {
		t.Errorf("ferry.broken（%d 番目）が ferry.arrived（%d 番目）の直後でない", bi, ai)
	}
	until := SimTime(17750)
	checkBrokenUntil(t, e, 0, &until)
	checkNotable(t, e, 17750, true)
	mustFailCode(t, e, departCmd(1), CodeFerryBroken)
	checkBreakdownEvents(t, runAll(e, 17750), []bdEv{repairedEv(17750, "f1")})
	mustApply(t, e, departCmd(1))
}

// 航行中にラウンドが終われば壊れない。
func TestBreakdownSailingUntilEnd(t *testing.T) {
	t.Parallel()
	e := startedEngineWith(t, breakdownTestConfig(breakAt(118000, 5000, "f1")), Options{CheckInvariants: true})
	e.RunUntil(117000)
	mustApply(t, e, departCmd(2)) // 120750 に着く（ラウンドの後）
	checkBreakdownEvents(t, runAll(e, 120000), nil)
	if e.Phase() != PhaseEnded {
		t.Fatalf("phase = %s", e.Phase())
	}
}

// SB-3 重なる故障。
func TestBreakdownOverlap(t *testing.T) {
	t.Parallel()
	t.Run("延びる", func(t *testing.T) {
		t.Parallel()
		e := startedEngineWith(t, breakdownTestConfig(breakAt(10000, 20000, "f1"), breakAt(20000, 20000, "f1")), Options{CheckInvariants: true})
		checkBreakdownEvents(t, runAll(e, 20000), []bdEv{brokenEv(10000, 30000, "f1"), brokenEv(20000, 40000, "f1")})
		// 30000 の修理は出ない。次の出来事は延びた方の修理。
		checkNotable(t, e, 40000, true)
		checkBreakdownEvents(t, runAll(e, 120000), []bdEv{repairedEv(40000, "f1")})
	})
	t.Run("延びない", func(t *testing.T) {
		t.Parallel()
		e := startedEngineWith(t, breakdownTestConfig(breakAt(10000, 20000, "f1"), breakAt(15000, 5000, "f1")), Options{CheckInvariants: true})
		checkBreakdownEvents(t, runAll(e, 120000), []bdEv{
			brokenEv(10000, 30000, "f1"),
			brokenEv(15000, 30000, "f1"),
			repairedEv(30000, "f1"),
		})
	})
	t.Run("航行中に2つ", func(t *testing.T) {
		t.Parallel()
		e := startedEngineWith(t, breakdownTestConfig(breakAt(1000, 3000, "f1"), breakAt(2000, 1000, "f1")), Options{CheckInvariants: true})
		mustApply(t, e, departCmd(2)) // 3750 に着く
		checkBreakdownEvents(t, runAll(e, 120000), []bdEv{brokenEv(3750, 6750, "f1"), repairedEv(6750, "f1")})
	})
}

// 長さがラウンドの終わりを超える故障は、修理が出ない。
func TestBreakdownPastEnd(t *testing.T) {
	t.Parallel()
	e := startedEngineWith(t, breakdownTestConfig(breakAt(110000, 20000, "f1")), Options{CheckInvariants: true})
	checkBreakdownEvents(t, runAll(e, 110000), []bdEv{brokenEv(110000, 130000, "f1")})
	// 修理は DurationMs より後なので次の出来事にならない。
	checkNotable(t, e, 0, false)
	checkBreakdownEvents(t, runAll(e, 120000), nil)
}

// twoFerryConfig は船を 2 隻にした設定（any の船の選び方を確かめる）。
func twoFerryConfig(defs ...game.BreakdownDef) Config {
	c := breakdownTestConfig(defs...)
	c.Ferries = append(c.Ferries, ferry.FerryConfig{ID: "f2", Home: 3, Capacity: 2, SpeedPermille: 1200})
	return c
}

// SB-4 決定性。同じシード・同じ入力で 2 回 → 同じトレース。Replay でも同じ時刻に同じ船が壊れる。
func TestBreakdownDeterminism(t *testing.T) {
	t.Parallel()
	cfg := twoFerryConfig(
		game.BreakdownDef{WindowStart: 20000, WindowEnd: 60000, DurationMs: 15000},
		game.BreakdownDef{WindowStart: 40000, WindowEnd: 90000, DurationMs: 10000},
	)
	run := func() (*Engine, []DomainEvent) {
		e := newEngine(t, cfg, Options{CheckInvariants: true})
		mustApply(t, e, Command{Type: CmdGameStart})
		for at := SimTime(1000); at <= 120000; at += 1000 {
			e.RunUntil(at)
			// 失敗しても入力ログに入らないので、そのまま次へ進む。
			e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
			e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: 1})
			e.Apply(Command{Type: CmdFerryDepart, FerryID: "f2", To: 2})
			e.Apply(Command{Type: CmdFerryDepart, FerryID: "f2", To: 3})
		}
		return e, e.DrainEvents()
	}
	a, aEvs := run()
	b, bEvs := run()
	if a.TraceHash() != b.TraceHash() || !reflect.DeepEqual(aEvs, bEvs) {
		t.Fatal("同じシード・同じ入力でトレースが違う")
	}
	bd := breakdownOnly(t, aEvs)
	if len(ofType[FerryBroken](aEvs)) < 2 {
		t.Fatalf("ferry.broken が 2 件未満: %+v", bd)
	}
	r := mustReplay(t, cfg, rpLog(testSeed, a.InputLog(), 120000), Options{CheckInvariants: true})
	rEvs := r.DrainEvents()
	if r.TraceHash() != a.TraceHash() {
		t.Error("Replay の TraceHash が違う")
	}
	if got := breakdownOnly(t, rEvs); !reflect.DeepEqual(got, bd) {
		t.Errorf("Replay の故障のイベント:\n got  %+v\n want %+v", got, bd)
	}
}

// any の船と window の時刻はシードで決まり、PlanBreakdowns（events/breakdown の乱数列）と一致する。
func TestBreakdownWindowSeed(t *testing.T) {
	t.Parallel()
	cfg := twoFerryConfig(game.BreakdownDef{WindowStart: 10000, WindowEnd: 20000, DurationMs: 5000})
	ferriesSeen := map[string]bool{}
	for seed := uint64(1); seed <= 20; seed++ {
		plan, err := game.PlanBreakdowns(cfg.Breakdowns, []string{"f1", "f2"}, 120000, Sub(seed, StreamBreakdown))
		if err != nil {
			t.Fatal(err)
		}
		evs, _ := seededRun(t, cfg, seed)
		br := ofTypeAt[FerryBroken](evs)
		if len(br) != 1 || br[0].T != SimTime(plan[0].At) || br[0].D.FerryID != plan[0].Ferry {
			t.Errorf("seed %d: ferry.broken = %+v, want t=%d ferry=%s", seed, br, plan[0].At, plan[0].Ferry)
		}
		ferriesSeen[plan[0].Ferry] = true
	}
	if len(ferriesSeen) != 2 {
		t.Errorf("any で選ばれた船が 1 種類だけ: %v", ferriesSeen)
	}
}

// SB-5 故障の定義が無ければ、乱数の引き方とトレースが変わらない。
// 故障を足しても、台本の嵐の時刻（events の乱数列）は変わらない。
func TestBreakdownNone(t *testing.T) {
	t.Parallel()
	storm := stormWin(10000, 50000, 5000, 0, [2]uint64{1, 2})
	base := stormTestConfig(storm)
	empty := stormTestConfig(storm)
	empty.Breakdowns = []game.BreakdownDef{}
	for seed := uint64(1); seed <= 5; seed++ {
		a, ha := seededRun(t, base, seed)
		b, hb := seededRun(t, empty, seed)
		if !reflect.DeepEqual(a, b) || ha != hb {
			t.Errorf("seed %d: Breakdowns が nil と空で結果が違う", seed)
		}
		if got := breakdownOnly(t, a); len(got) != 0 {
			t.Errorf("seed %d: 故障のイベントが出た: %+v", seed, got)
		}
		with := stormTestConfig(storm)
		with.Breakdowns = []game.BreakdownDef{{WindowStart: 1000, WindowEnd: 100000, DurationMs: 1000}}
		c, _ := seededRun(t, with, seed)
		if ga, gc := ofTypeAt[RouteBlocked](a), ofTypeAt[RouteBlocked](c); !reflect.DeepEqual(ga, gc) {
			t.Errorf("seed %d: 故障を足すと嵐が変わる: %+v / %+v", seed, ga, gc)
		}
	}
	e := newEngine(t, base, Options{})
	if e.breakdowns.list != nil || e.breakdowns.pending != nil || e.breakdowns.gen != nil {
		t.Errorf("故障なしで帳簿が空でない: %+v", e.breakdowns)
	}
}

// 設定の誤り。
func TestBreakdownConfigErrors(t *testing.T) {
	t.Parallel()
	for _, d := range []game.BreakdownDef{
		breakAt(10000, 1000, "f9"),
		breakAt(10000, 0, "f1"),
		{WindowStart: 10000, WindowEnd: 120001, DurationMs: 1000},
	} {
		e, err := New(breakdownTestConfig(d), testSeed, Options{})
		if !errors.Is(err, game.ErrInvalidBreakdown) || e != nil {
			t.Errorf("%+v: New = %v, %v; want nil, ErrInvalidBreakdown", d, e, err)
		}
	}
}

// 設定の複製。New の後に呼び出し側の Breakdowns を書き換えても変わらない。
func TestBreakdownConfigCopied(t *testing.T) {
	t.Parallel()
	cfg := breakdownTestConfig(breakAt(10000, 1000, "f1"))
	e := newEngine(t, cfg, Options{CheckInvariants: true})
	cfg.Breakdowns[0].DurationMs = 5000
	if e.cfg.Breakdowns[0].DurationMs != 1000 {
		t.Error("Config.Breakdowns を複製していない")
	}
}

// SB-7 テストモードで、故障を含むラウンドを最後まで走らせる（船を動かし続ける）。
func TestBreakdownInvariantsFullRound(t *testing.T) {
	t.Parallel()
	cfg := twoFerryConfig(
		breakAt(5000, 10000, "f1"),
		breakAt(8000, 30000, "f2"),
		game.BreakdownDef{WindowStart: 0, WindowEnd: 100000, DurationMs: 7000},
		game.BreakdownDef{WindowStart: 30000, WindowEnd: 110000, DurationMs: 20000},
	)
	for seed := uint64(1); seed <= 5; seed++ {
		e, err := New(cfg, seed, Options{CheckInvariants: true})
		if err != nil {
			t.Fatal(err)
		}
		mustApply(t, e, Command{Type: CmdGameStart})
		for at := SimTime(500); at <= 120000; at += 500 {
			e.RunUntil(at)
			for _, f := range []string{"f1", "f2"} {
				for _, to := range []uint64{1, 2, 3} {
					e.Apply(Command{Type: CmdFerryAutoload, FerryID: f, To: to})
					e.Apply(Command{Type: CmdFerryDepart, FerryID: f, To: to})
				}
			}
		}
		if e.Phase() != PhaseEnded {
			t.Fatalf("seed %d: phase = %s", seed, e.Phase())
		}
		evs := e.DrainEvents()
		if len(ofType[FerryBroken](evs)) == 0 {
			t.Errorf("seed %d: ferry.broken が出ていない", seed)
		}
	}
}
