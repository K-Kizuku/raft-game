package sim

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
)

// lineConfig は 6 島が一直線（1-2-3-4-5-6）の設定。船 f1 は 3 にいる。defs は欠航の定義。
func lineConfig(defs ...game.CancellationDef) Config {
	c := testConfig()
	c.Islands = 6
	c.DurationMs = 240000
	c.Routes = []ferry.RouteConfig{
		{A: 1, B: 2, TravelMs: 3000},
		{A: 2, B: 3, TravelMs: 3000},
		{A: 3, B: 4, TravelMs: 3000},
		{A: 4, B: 5, TravelMs: 3000},
		{A: 5, B: 6, TravelMs: 3000},
	}
	c.Ferries = []ferry.FerryConfig{{ID: "f1", Home: 3, Capacity: 4, SpeedPermille: 1000}}
	c.Cancellations = defs
	return c
}

// cancelDef は欠航の定義。
func cancelDef(island uint64, permille, slot, dur int64) game.CancellationDef {
	return game.CancellationDef{Island: island, ProbabilityPermille: permille, SlotMs: slot, DurationMs: dur}
}

func cancelBlocked(t, until SimTime, island uint64, ids ...string) stormEv {
	return stormEv{T: t, Data: RouteBlocked{RouteIDs: ids, Until: until, Cause: BlockCauseCancellation, IslandID: island}}
}

// SC-1 御蔵島（4）に確率 1000 → 30000 に 3-4・4-5 が 40000 まで止まり、40000 に route.unblocked。
// その間の 3→4 の出航は route_blocked、40000 ちょうどなら成功する。
func TestCancellationBasic(t *testing.T) {
	t.Parallel()
	cfg := lineConfig(cancelDef(4, 1000, 30000, 10000))
	cfg.DurationMs = 60000 // 区間の境目は 30000 だけ
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	checkNotable(t, e, 30000, true)

	evs := runAll(e, 30000)
	checkStormEvents(t, evs, []stormEv{cancelBlocked(30000, 40000, 4, "3-4", "4-5")})
	mustFailCode(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 4}, CodeRouteBlocked)
	checkNotable(t, e, 40000, true)

	evs = runAll(e, 39999)
	if got := stormOnly(t, evs); len(got) != 0 {
		t.Errorf("39999 までに嵐のイベント: %+v", got)
	}
	mustFailCode(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 4}, CodeRouteBlocked)

	evs = runAll(e, 40000)
	checkStormEvents(t, evs, []stormEv{unblocked(40000, "3-4", "4-5")})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 4})
}

// SC-2 確率 0 → route.blocked が出ない。欠航の定義が無い設定とトレースが同じ（嵐の window の時刻も変わらない）。
func TestCancellationZeroSameTrace(t *testing.T) {
	t.Parallel()
	storm := stormWin(10000, 50000, 5000, 0, [2]uint64{1, 2})
	for seed := uint64(1); seed <= 5; seed++ {
		base := lineConfig()
		base.Storms = []game.StormDef{storm}
		zero := lineConfig(cancelDef(4, 0, 30000, 10000))
		zero.Storms = []game.StormDef{storm}
		a, ha := seededRun(t, base, seed)
		b, hb := seededRun(t, zero, seed)
		if !reflect.DeepEqual(a, b) || ha != hb {
			t.Errorf("seed %d: 確率 0 の欠航でトレースが変わる", seed)
		}
		for _, rb := range ofType[RouteBlocked](b) {
			if rb.Cause != "" {
				t.Errorf("seed %d: 確率 0 で欠航の route.blocked: %+v", seed, rb)
			}
		}
		// 確率 1000 でも、嵐の時刻（events の乱数列）は変わらない。
		all := lineConfig(cancelDef(6, 1000, 30000, 10000))
		all.Storms = []game.StormDef{storm}
		c, _ := seededRun(t, all, seed)
		if sa, sc := stormBlocks(a), stormBlocks(c); !reflect.DeepEqual(sa, sc) {
			t.Errorf("seed %d: 欠航を足すと嵐が変わる: %+v / %+v", seed, sa, sc)
		}
	}
	e := newEngine(t, lineConfig(), Options{})
	if e.storms.list != nil {
		t.Errorf("欠航も嵐もないのに帳簿が空でない: %+v", e.storms.list)
	}
}

// stormBlocks は嵐の（cause の無い）route.blocked を時刻つきで返す。
func stormBlocks(evs []DomainEvent) []timed[RouteBlocked] {
	var out []timed[RouteBlocked]
	for _, rb := range ofTypeAt[RouteBlocked](evs) {
		if rb.D.Cause == "" {
			out = append(out, rb)
		}
	}
	return out
}

// SC-3 durationMs 40000・slotMs 30000・確率 1000 → 60000 の route.blocked は延びた分（until 100000）。
// 前の終わり 70000 では route.unblocked を出さない。
func TestCancellationExtend(t *testing.T) {
	t.Parallel()
	cfg := lineConfig(cancelDef(6, 1000, 30000, 40000))
	cfg.DurationMs = 100000 // 境目は 30000・60000・90000
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	evs := runAll(e, cfg.DurationMs)
	checkStormEvents(t, evs, []stormEv{
		cancelBlocked(30000, 70000, 6, "5-6"),
		cancelBlocked(60000, 100000, 6, "5-6"),
		cancelBlocked(90000, 130000, 6, "5-6"),
	})
}

// SC-3 続き: 当たり外れが混ざると、続けて当たった欠航の route.unblocked は最後の終わりで 1 回だけ出る。
func TestCancellationExtendUnblockOnce(t *testing.T) {
	t.Parallel()
	cfg := lineConfig(cancelDef(6, 500, 30000, 40000))
	extended := false
	for seed := uint64(1); seed <= 30; seed++ {
		plan, err := game.PlanCancellations(cfg.Cancellations, 6, int64(cfg.DurationMs), Sub(seed, StreamCancellation))
		if err != nil {
			t.Fatal(err)
		}
		// 期待値: 前の遮断が続いている間に始まった欠航は延ばし、途切れたところで route.unblocked を 1 回出す。
		var want []stormEv
		var end int64 = -1
		for _, c := range plan {
			if end >= 0 && end < c.Start {
				want = append(want, unblocked(SimTime(end), "5-6"))
			} else if end >= c.Start {
				extended = true
			}
			want = append(want, cancelBlocked(SimTime(c.Start), SimTime(c.End), 6, "5-6"))
			end = c.End
		}
		if end >= 0 && end <= int64(cfg.DurationMs) {
			want = append(want, unblocked(SimTime(end), "5-6"))
		}
		evs, _ := seededRun(t, cfg, seed)
		if got := stormOnly(t, evs); !reflect.DeepEqual(got, want) {
			t.Errorf("seed %d:\n got  %+v\n want %+v", seed, got, want)
		}
	}
	if !extended {
		t.Error("続けて当たる欠航が 1 つも無い（シードを増やす）")
	}
}

// SC-4 嵐（30000〜50000）と欠航（30000〜40000）が同じ航路 → 欠航の route.blocked はその航路を含まない。
// route.unblocked は 50000 に 1 回。
func TestCancellationWithStorm(t *testing.T) {
	t.Parallel()
	cfg := lineConfig(cancelDef(4, 1000, 30000, 10000))
	cfg.DurationMs = 60000
	cfg.Storms = []game.StormDef{stormAt(30000, 20000, 0, [2]uint64{3, 4})}
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	evs := runAll(e, cfg.DurationMs)
	checkStormEvents(t, evs, []stormEv{
		blocked(30000, 50000, "3-4"),
		cancelBlocked(30000, 40000, 4, "4-5"),
		unblocked(40000, "4-5"),
		unblocked(50000, "3-4"),
	})
	// 嵐が先に止めていて、欠航の航路が全部延びなければ route.blocked は出ない。
	cfg.Storms = []game.StormDef{stormAt(30000, 20000, 0, [2]uint64{3, 4}, [2]uint64{4, 5})}
	e = startedEngineWith(t, cfg, Options{CheckInvariants: true})
	evs = runAll(e, cfg.DurationMs)
	checkStormEvents(t, evs, []stormEv{
		blocked(30000, 50000, "3-4", "4-5"),
		unblocked(50000, "3-4", "4-5"),
	})
}

// SC-5 決定性。同じシード・同じ入力で 2 回 → トレースのハッシュが同じ。Replay でも同じ時刻に同じ航路が止まる。
func TestCancellationDeterminism(t *testing.T) {
	t.Parallel()
	cfg := lineConfig(cancelDef(2, 170, 30000, 10000), cancelDef(4, 410, 20000, 10000), cancelDef(6, 410, 30000, 10000))
	run := func() (*Engine, []DomainEvent) {
		e := newEngine(t, cfg, Options{CheckInvariants: true})
		mustApply(t, e, Command{Type: CmdGameStart})
		for at := SimTime(1000); at <= cfg.DurationMs; at += 1000 {
			e.RunUntil(at)
			// 失敗しても入力ログに入らないので、そのまま次へ進む。
			for _, to := range []uint64{2, 3, 4} {
				e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: to})
			}
		}
		return e, e.DrainEvents()
	}
	a, aEvs := run()
	b, bEvs := run()
	if a.TraceHash() != b.TraceHash() || !reflect.DeepEqual(aEvs, bEvs) {
		t.Fatal("同じシード・同じ入力でトレースが違う")
	}
	want := stormOnly(t, aEvs)
	if len(ofType[RouteBlocked](aEvs)) == 0 {
		t.Fatal("欠航が 1 件も起きない（シードを変える）")
	}
	r := mustReplay(t, cfg, rpLog(testSeed, a.InputLog(), cfg.DurationMs), Options{CheckInvariants: true})
	rEvs := r.DrainEvents()
	if r.TraceHash() != a.TraceHash() {
		t.Error("Replay の TraceHash が違う")
	}
	if got := stormOnly(t, rEvs); !reflect.DeepEqual(got, want) {
		t.Errorf("Replay の遮断のイベント:\n got  %+v\n want %+v", got, want)
	}
}

// 欠航の時刻と島は PlanCancellations（events/cancellation の乱数列）と一致する。
func TestCancellationSeed(t *testing.T) {
	t.Parallel()
	cfg := lineConfig(cancelDef(4, 410, 30000, 10000))
	for seed := uint64(1); seed <= 20; seed++ {
		plan, err := game.PlanCancellations(cfg.Cancellations, 6, 240000, Sub(seed, StreamCancellation))
		if err != nil {
			t.Fatal(err)
		}
		evs, _ := seededRun(t, cfg, seed)
		var got []SimTime
		for _, rb := range ofTypeAt[RouteBlocked](evs) {
			got = append(got, rb.T)
		}
		var want []SimTime
		for _, c := range plan {
			want = append(want, SimTime(c.Start))
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("seed %d: route.blocked の時刻 %v, want %v", seed, got, want)
		}
	}
}

// SC-6 嵐の route.blocked の JSON に cause・islandId が無い。欠航では入る。
func TestCancellationJSON(t *testing.T) {
	t.Parallel()
	storm, err := json.Marshal(RouteBlocked{RouteIDs: []string{"1-2"}, Until: 30000})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(storm); s != `{"routeIds":["1-2"],"until":30000}` {
		t.Errorf("嵐の JSON = %s", s)
	}
	cancel, err := json.Marshal(RouteBlocked{RouteIDs: []string{"3-4"}, Until: 40000, Cause: BlockCauseCancellation, IslandID: 4})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(cancel); s != `{"routeIds":["3-4"],"until":40000,"cause":"cancellation","islandId":4}` {
		t.Errorf("欠航の JSON = %s", s)
	}
	e := startedEngineWith(t, stormTestConfig(stormAt(10000, 20000, 0, [2]uint64{1, 2})), Options{})
	for _, rb := range ofType[RouteBlocked](runAll(e, 120000)) {
		b, _ := json.Marshal(rb)
		if strings.Contains(string(b), "cause") || strings.Contains(string(b), "islandId") {
			t.Errorf("嵐の route.blocked に欠航の欄: %s", b)
		}
	}
}

// SC-7 NextNotableAt が欠航の開始・終わりを返す。スナップショットの routesState に欠航中の blockedUntil が入る。
func TestCancellationNotableSnapshot(t *testing.T) {
	t.Parallel()
	cfg := lineConfig(cancelDef(1, 1000, 30000, 10000))
	cfg.DurationMs = 60000
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	checkNotable(t, e, 30000, true)
	runAll(e, 30000)
	checkNotable(t, e, 40000, true)
	until := SimTime(40000)
	want := []RouteView{{ID: "1-2", BlockedUntil: &until}, {ID: "2-3"}, {ID: "3-4"}, {ID: "4-5"}, {ID: "5-6"}}
	if got := e.Snapshot().Routes; !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot().Routes = %+v, want %+v", got, want)
	}
	runAll(e, 40000)
	checkNotable(t, e, 0, false)
	if got := e.Snapshot().Routes[0].BlockedUntil; got != nil {
		t.Errorf("Snapshot().Routes[0].BlockedUntil = %d, want nil", *got)
	}
}

// SC-8 テストモードで、欠航を含むラウンドを最後まで走らせる（船を動かし続ける）。
func TestCancellationInvariantsFullRound(t *testing.T) {
	t.Parallel()
	cfg := lineConfig(cancelDef(2, 410, 20000, 30000), cancelDef(4, 410, 30000, 10000), cancelDef(3, 500, 15000, 10000))
	cfg.Storms = []game.StormDef{stormWin(10000, 100000, 20000, 5000, [2]uint64{3, 4})}
	for seed := uint64(1); seed <= 5; seed++ {
		e, err := New(cfg, seed, Options{CheckInvariants: true})
		if err != nil {
			t.Fatal(err)
		}
		mustApply(t, e, Command{Type: CmdGameStart})
		for at := SimTime(500); at <= cfg.DurationMs; at += 500 {
			e.RunUntil(at)
			for _, to := range []uint64{2, 3, 4} {
				e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: to})
			}
		}
		if e.Phase() != PhaseEnded {
			t.Errorf("seed %d: phase = %s", seed, e.Phase())
		}
	}
}

// 設定の誤り（ErrInvalidCancellation）と複製。
func TestCancellationConfig(t *testing.T) {
	t.Parallel()
	for _, d := range []game.CancellationDef{
		cancelDef(7, 100, 30000, 10000),
		cancelDef(1, 1001, 30000, 10000),
		cancelDef(1, 100, 0, 10000),
		cancelDef(1, 100, 30000, 0),
	} {
		e, err := New(lineConfig(d), testSeed, Options{})
		if !errors.Is(err, game.ErrInvalidCancellation) || e != nil {
			t.Errorf("%+v: New = %v, %v; want nil, ErrInvalidCancellation", d, e, err)
		}
	}
	cfg := lineConfig(cancelDef(1, 100, 30000, 10000))
	e := newEngine(t, cfg, Options{})
	cfg.Cancellations[0].DurationMs = 5000
	if e.cfg.Cancellations[0].DurationMs != 10000 {
		t.Error("Config.Cancellations を複製していない")
	}
}
