package sim

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
)

// stormTestConfig は嵐のテストの基本の設定（testConfig に Storms を入れたもの）。
func stormTestConfig(storms ...game.StormDef) Config {
	c := testConfig()
	c.Storms = storms
	return c
}

// stormAt は at で書いた嵐。
func stormAt(start, dur, forecast int64, routes ...[2]uint64) game.StormDef {
	return game.StormDef{WindowStart: start, WindowEnd: start, DurationMs: dur, Routes: routes, ForecastMs: forecast}
}

// stormWin は window で書いた嵐。
func stormWin(start, end, dur, forecast int64, routes ...[2]uint64) game.StormDef {
	return game.StormDef{WindowStart: start, WindowEnd: end, DurationMs: dur, Routes: routes, ForecastMs: forecast}
}

// stormEv は嵐のイベント（forecast・route.*）の時刻と中身。
type stormEv struct {
	T    SimTime
	Data EventData
}

// stormOnly は evs のうち forecast・route.* だけを取り出す（Type と Data の型の一致も確かめる）。
func stormOnly(t *testing.T, evs []DomainEvent) []stormEv {
	t.Helper()
	var out []stormEv
	for _, ev := range evs {
		switch ev.Data.(type) {
		case Forecast, RouteBlocked, RouteUnblocked:
			if ev.Type != ev.Data.EventType() {
				t.Errorf("event %d: Type %s but Data is %T", ev.Seq, ev.Type, ev.Data)
			}
			out = append(out, stormEv{T: ev.T, Data: ev.Data})
		}
	}
	return out
}

func checkStormEvents(t *testing.T, evs []DomainEvent, want []stormEv) {
	t.Helper()
	if got := stormOnly(t, evs); !reflect.DeepEqual(got, want) {
		t.Errorf("嵐のイベント:\n got  %+v\n want %+v", got, want)
	}
}

func blocked(t SimTime, until SimTime, ids ...string) stormEv {
	return stormEv{T: t, Data: RouteBlocked{RouteIDs: ids, Until: until}}
}

func unblocked(t SimTime, ids ...string) stormEv {
	return stormEv{T: t, Data: RouteUnblocked{RouteIDs: ids, Until: t}}
}

func forecastEv(t, at SimTime, ids ...string) stormEv {
	return stormEv{T: t, Data: Forecast{Event: "storm", At: at, RouteIDs: ids}}
}

func departCmd(to uint64) Command { return Command{Type: CmdFerryDepart, FerryID: "f1", To: to} }

// mustFailCode は cmd が code で失敗することを確かめる。
func mustFailCode(t *testing.T, e *Engine, cmd Command, code string) {
	t.Helper()
	if ack := e.Apply(cmd); ack.OK || ack.Code != code {
		t.Errorf("t=%d: Apply(%+v) = %+v, want %s", e.Now(), cmd, ack, code)
	}
}

// runAll は until まで進めて、その間のイベントを返す。
func runAll(e *Engine, until SimTime) []DomainEvent {
	e.RunUntil(until)
	return e.DrainEvents()
}

// SE-1 基本。
func TestStormBasic(t *testing.T) {
	t.Parallel()
	e := startedEngineWith(t, stormTestConfig(stormAt(10000, 20000, 5000, [2]uint64{2, 1})), Options{CheckInvariants: true})
	var all []DomainEvent
	checkNotable(t, e, 5000, true)

	evs := runAll(e, 5000)
	all = append(all, evs...)
	checkStormEvents(t, evs, []stormEv{forecastEv(5000, 10000, "1-2")})
	checkNotable(t, e, 10000, true)

	evs = runAll(e, 10000)
	all = append(all, evs...)
	checkStormEvents(t, evs, []stormEv{blocked(10000, 30000, "1-2")})
	until := SimTime(30000)
	if got, want := e.Snapshot().Routes, []RouteView{{ID: "1-2", BlockedUntil: &until}, {ID: "2-3"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot().Routes = %+v, want %+v", got, want)
	}
	mustFailCode(t, e, departCmd(2), CodeRouteBlocked)
	checkNotable(t, e, 30000, true)

	evs = runAll(e, 30000)
	all = append(all, evs...)
	checkStormEvents(t, evs, []stormEv{unblocked(30000, "1-2")})
	if got := e.Snapshot().Routes[0].BlockedUntil; got != nil {
		t.Errorf("Snapshot().Routes[0].BlockedUntil = %d, want nil", *got)
	}
	mustApply(t, e, departCmd(2))
	checkNotable(t, e, 33750, true)

	all = append(all, e.DrainEvents()...)
	all = append(all, runAll(e, 120000)...)
	if n := len(ofType[Forecast](all)); n != 1 {
		t.Errorf("forecast %d 件, want 1", n)
	}
	if n := len(ofType[RouteBlocked](all)); n != 1 {
		t.Errorf("route.blocked %d 件, want 1", n)
	}
	if n := len(ofType[RouteUnblocked](all)); n != 1 {
		t.Errorf("route.unblocked %d 件, want 1", n)
	}
}

// SE-2 予報なし。
func TestStormNoForecast(t *testing.T) {
	t.Parallel()
	e := startedEngineWith(t, stormTestConfig(stormAt(10000, 20000, 0, [2]uint64{1, 2})), Options{CheckInvariants: true})
	checkNotable(t, e, 10000, true)
	evs := runAll(e, 120000)
	if n := len(ofType[Forecast](evs)); n != 0 {
		t.Errorf("forecast %d 件, want 0", n)
	}
	checkStormEvents(t, evs, []stormEv{blocked(10000, 30000, "1-2"), unblocked(30000, "1-2")})
}

// SE-3 航行中の船はそのまま進む。
func TestStormSailingFerry(t *testing.T) {
	t.Parallel()
	e := startedEngineWith(t, stormTestConfig(stormAt(10000, 20000, 5000, [2]uint64{2, 1})), Options{CheckInvariants: true})
	e.RunUntil(9000)
	mustApply(t, e, departCmd(2))
	e.DrainEvents()
	evs := runAll(e, 12750)
	arrived := ofType[FerryArrived](evs)
	if len(arrived) != 1 || arrived[0].IslandID != 2 {
		t.Fatalf("ferry.arrived = %+v, want 島 2 に 1 件", arrived)
	}
	if i := slices.IndexFunc(evs, func(ev DomainEvent) bool { return ev.Type == EvFerryArrived }); evs[i].T != 12750 {
		t.Errorf("ferry.arrived の t = %d, want 12750", evs[i].T)
	}
	e.RunUntil(14250)
	mustFailCode(t, e, departCmd(1), CodeRouteBlocked)
	mustApply(t, e, departCmd(3))
}

// SE-4 重なり。
func TestStormOverlap(t *testing.T) {
	t.Parallel()
	cfg := stormTestConfig(
		stormAt(10000, 30000, 0, [2]uint64{1, 2}),
		stormAt(20000, 5000, 0, [2]uint64{1, 2}, [2]uint64{2, 3}),
		stormAt(30000, 20000, 0, [2]uint64{2, 1}),
	)
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	checkStormEvents(t, runAll(e, 120000), []stormEv{
		blocked(10000, 40000, "1-2"),
		blocked(20000, 25000, "2-3"),
		unblocked(25000, "2-3"),
		blocked(30000, 50000, "1-2"),
		unblocked(50000, "1-2"),
	})
}

// SE-5 終わりと開始が同じ時刻なら途切れない。定義の順を逆にしても同じ。
func TestStormContinuous(t *testing.T) {
	t.Parallel()
	a := stormAt(10000, 10000, 0, [2]uint64{1, 2})
	b := stormAt(20000, 10000, 0, [2]uint64{1, 2})
	want := []stormEv{blocked(10000, 20000, "1-2"), blocked(20000, 30000, "1-2"), unblocked(30000, "1-2")}
	for _, defs := range [][]game.StormDef{{a, b}, {b, a}} {
		e := startedEngineWith(t, stormTestConfig(defs...), Options{CheckInvariants: true})
		checkStormEvents(t, runAll(e, 120000), want)
	}
}

// SE-6 同じ時刻に終わる 2 つの嵐。
func TestStormSameEnd(t *testing.T) {
	t.Parallel()
	cfg := stormTestConfig(stormAt(10000, 10000, 0, [2]uint64{1, 2}), stormAt(15000, 5000, 0, [2]uint64{1, 2}))
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	checkStormEvents(t, runAll(e, 120000), []stormEv{blocked(10000, 20000, "1-2"), unblocked(20000, "1-2")})
}

// seededRun は cfg と seed で開始してラウンドの終わりまで進め、イベントの列とハッシュを返す。
func seededRun(t *testing.T, cfg Config, seed uint64) ([]DomainEvent, [32]byte) {
	t.Helper()
	e, err := New(cfg, seed, Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustApply(t, e, Command{Type: CmdGameStart})
	e.RunUntil(cfg.DurationMs)
	return e.DrainEvents(), e.TraceHash()
}

// SE-7 幅とシード。
func TestStormWindowSeed(t *testing.T) {
	t.Parallel()
	cfg := stormTestConfig(stormWin(10000, 20000, 5000, 0, [2]uint64{1, 2}))
	var starts []SimTime
	for seed := uint64(1); seed <= 30; seed++ {
		plan, err := game.PlanStorms(cfg.Storms, 3, 120000, Sub(seed, StreamEvents))
		if err != nil {
			t.Fatalf("PlanStorms: %v", err)
		}
		evs, hash := seededRun(t, cfg, seed)
		var bt []SimTime
		for _, ev := range evs {
			if ev.Type == EvRouteBlocked {
				bt = append(bt, ev.T)
			}
		}
		if len(bt) != 1 || bt[0] != SimTime(plan[0].Start) || bt[0] < 10000 || bt[0] > 20000 {
			t.Errorf("seed %d: route.blocked の t = %v, want [%d]（10000..20000）", seed, bt, plan[0].Start)
		}
		if !slices.Contains(starts, SimTime(plan[0].Start)) {
			starts = append(starts, SimTime(plan[0].Start))
		}
		if seed <= 3 {
			evs2, hash2 := seededRun(t, cfg, seed)
			if !reflect.DeepEqual(evs, evs2) || hash != hash2 {
				t.Errorf("seed %d: 同じシードでイベントの列・TraceHash が違う", seed)
			}
		}
	}
	if len(starts) < 2 {
		t.Errorf("開始の時刻が 1 種類だけ: %v", starts)
	}
}

// SE-8 設定の誤り。
func TestStormConfigErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		storm game.StormDef
		want  error
	}{
		{"航路がない", stormAt(10000, 1000, 0, [2]uint64{1, 3}), ferry.ErrNoRoute},
		{"durationMs=0", stormAt(10000, 0, 0, [2]uint64{1, 2}), game.ErrInvalidStorm},
		{"windowEnd超過", stormWin(10000, 120001, 1000, 0, [2]uint64{1, 2}), game.ErrInvalidStorm},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, err := New(stormTestConfig(tc.storm), testSeed, Options{})
			if !errors.Is(err, tc.want) {
				t.Errorf("New の err = %v, want %v", err, tc.want)
			}
			if e != nil {
				t.Error("誤りのときに Engine を返した")
			}
		})
	}
}

// SE-9 嵐なし。
func TestStormNone(t *testing.T) {
	t.Parallel()
	for _, cfg := range []Config{testConfig(), stormTestConfig()} {
		e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
		e.RunUntil(9000)
		mustApply(t, e, departCmd(2))
		evs := runAll(e, 120000)
		if got := stormOnly(t, evs); len(got) != 0 {
			t.Errorf("嵐のイベントが出た: %+v", got)
		}
		if len(e.storms.list) != 0 || e.storms.unblockedAt != nil {
			t.Errorf("帳簿が空でない: %+v", e.storms)
		}
	}
	// 嵐なしなら、Storms が nil でも空でも同じ結果（乱数を引かない）。
	a, ha := seededRun(t, testConfig(), testSeed)
	b, hb := seededRun(t, stormTestConfig(), testSeed)
	if !reflect.DeepEqual(a, b) || ha != hb {
		t.Error("Storms が nil と空で結果が違う")
	}
}

// SE-10 ラウンドの終わりちょうど。
func TestStormAtRoundEnd(t *testing.T) {
	t.Parallel()
	e := startedEngineWith(t, stormTestConfig(stormAt(120000, 5000, 10000, [2]uint64{1, 2})), Options{CheckInvariants: true})
	checkNotable(t, e, 110000, true)
	checkStormEvents(t, runAll(e, 110000), []stormEv{forecastEv(110000, 120000, "1-2")})
	checkNotable(t, e, 120000, true)
	evs := runAll(e, 120000)
	checkStormEvents(t, evs, []stormEv{blocked(120000, 125000, "1-2")})
	bi := slices.IndexFunc(evs, func(ev DomainEvent) bool { return ev.Type == EvRouteBlocked })
	ei := slices.IndexFunc(evs, func(ev DomainEvent) bool {
		pc, ok := ev.Data.(PhaseChanged)
		return ok && pc.Phase == PhaseEnded
	})
	if bi < 0 || ei < 0 || bi > ei {
		t.Errorf("route.blocked（%d 番目）が phase.changed{ended}（%d 番目）より先でない", bi, ei)
	}
}

// SE-12 設定の複製。
func TestStormConfigCopied(t *testing.T) {
	t.Parallel()
	cfg := stormTestConfig(stormAt(10000, 1000, 0, [2]uint64{1, 2}))
	e := newEngine(t, cfg, Options{CheckInvariants: true})
	cfg.Storms[0].Routes[0] = [2]uint64{2, 3}
	mustApply(t, e, Command{Type: CmdGameStart})
	e.DrainEvents()
	checkStormEvents(t, runAll(e, 10000), []stormEv{blocked(10000, 11000, "1-2")})
}

// SE-13 raiseStorm。
func TestRaiseStorm(t *testing.T) {
	t.Parallel()
	r12 := [][2]uint64{{1, 2}}

	t.Run("今の時刻", func(t *testing.T) {
		t.Parallel()
		e := startedEngine(t)
		e.RunUntil(7000)
		e.DrainEvents()
		if _, err := e.raiseStorm(game.Storm{Start: 7000, End: 12000, Routes: r12}); err != nil {
			t.Fatalf("raiseStorm: %v", err)
		}
		e.afterStep()
		evs := e.DrainEvents()
		checkStormEvents(t, evs, []stormEv{blocked(7000, 12000, "1-2")})
		checkNotable(t, e, 12000, true)
		checkStormEvents(t, runAll(e, 12000), []stormEv{unblocked(12000, "1-2")})
	})

	t.Run("先の時刻", func(t *testing.T) {
		t.Parallel()
		e := startedEngine(t)
		e.RunUntil(7000)
		e.DrainEvents()
		if _, err := e.raiseStorm(game.Storm{Start: 20000, End: 25000, HasForecast: true, ForecastAt: 15000, Routes: r12}); err != nil {
			t.Fatalf("raiseStorm: %v", err)
		}
		e.afterStep()
		checkStormEvents(t, e.DrainEvents(), nil)
		checkNotable(t, e, 15000, true)
		checkStormEvents(t, runAll(e, 20000), []stormEv{forecastEv(15000, 20000, "1-2"), blocked(20000, 25000, "1-2")})
		checkStormEvents(t, runAll(e, 30000), []stormEv{unblocked(25000, "1-2")})
	})

	cases := []struct {
		name string
		s    game.Storm
		want error
	}{
		{"過去", game.Storm{Start: 6000, End: 12000, Routes: r12}, game.ErrInvalidStorm},
		{"End==Start", game.Storm{Start: 7000, End: 7000, Routes: r12}, game.ErrInvalidStorm},
		{"航路が空", game.Storm{Start: 7000, End: 8000}, game.ErrInvalidStorm},
		{"航路がない", game.Storm{Start: 7000, End: 8000, Routes: [][2]uint64{{1, 3}}}, ferry.ErrNoRoute},
		{"航路がない（2 つ目）", game.Storm{Start: 9000, End: 10000, Routes: [][2]uint64{{1, 2}, {3, 1}}}, ferry.ErrNoRoute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := startedEngineWith(t, stormTestConfig(stormAt(50000, 1000, 1000, [2]uint64{2, 3})), Options{CheckInvariants: true})
			e.RunUntil(7000)
			e.DrainEvents()
			list, unb, n, routes := slices.Clone(e.storms.list), slices.Clone(e.storms.unblockedAt), e.sched.Len(), e.tr.Routes()
			if _, err := e.raiseStorm(tc.s); !errors.Is(err, tc.want) {
				t.Fatalf("raiseStorm の err = %v, want %v", err, tc.want)
			}
			if evs := e.DrainEvents(); len(evs) != 0 {
				t.Errorf("イベントが出た: %+v", evs)
			}
			if !reflect.DeepEqual(e.storms.list, list) || !reflect.DeepEqual(e.storms.unblockedAt, unb) {
				t.Errorf("帳簿が変わった: %+v", e.storms)
			}
			if e.sched.Len() != n {
				t.Errorf("スケジューラの長さ = %d, want %d", e.sched.Len(), n)
			}
			if !reflect.DeepEqual(e.tr.Routes(), routes) {
				t.Errorf("航路が変わった: %+v", e.tr.Routes())
			}
		})
	}
}

// stormModel は route.* のイベントから組み立てた、航路ごとの遮断の状態（航路の添字 = Snapshot().Routes の添字）。
type stormModel struct {
	ids       []string
	until     []SimTime // 最後の route.blocked の until。0 = まだない
	unblocked []SimTime // 最後の route.unblocked の until。-1 = まだない
}

func (m *stormModel) index(t *testing.T, id string) int {
	t.Helper()
	k := slices.Index(m.ids, id)
	if k < 0 {
		t.Fatalf("知らない航路 %q", id)
	}
	return k
}

func (m *stormModel) apply(t *testing.T, evs []DomainEvent) {
	t.Helper()
	for _, ev := range evs {
		switch d := ev.Data.(type) {
		case RouteBlocked:
			for _, id := range d.RouteIDs {
				k := m.index(t, id)
				if d.Until <= m.until[k] || d.Until <= ev.T {
					t.Errorf("t=%d: route.blocked %s の until %d が延びていない（前は %d）", ev.T, id, d.Until, m.until[k])
				}
				m.until[k] = d.Until
			}
		case RouteUnblocked:
			if d.Until != ev.T {
				t.Errorf("t=%d: route.unblocked の until %d が t と違う", ev.T, d.Until)
			}
			for _, id := range d.RouteIDs {
				k := m.index(t, id)
				if m.until[k] != ev.T || m.unblocked[k] == ev.T {
					t.Errorf("t=%d: route.unblocked %s（遮断の終わり %d、前の unblocked %d）", ev.T, id, m.until[k], m.unblocked[k])
				}
				m.unblocked[k] = ev.T
			}
		}
	}
}

// check は、モデルと Snapshot().Routes の遮断が一致し、終わった遮断には route.unblocked が出ていることを確かめる。
func (m *stormModel) check(t *testing.T, e *Engine) {
	t.Helper()
	now := e.Now()
	for k, rv := range e.Snapshot().Routes {
		if want := m.until[k] > now; (rv.BlockedUntil != nil) != want {
			t.Fatalf("t=%d: %s の BlockedUntil = %v、最後の route.blocked の until = %d", now, rv.ID, rv.BlockedUntil, m.until[k])
		}
		if m.until[k] != 0 && m.until[k] <= now && m.unblocked[k] != m.until[k] {
			t.Fatalf("t=%d: %s の遮断（%d まで）の route.unblocked が出ていない", now, rv.ID, m.until[k])
		}
	}
}

// stormRandomRun は SE-14 の嵐の設定で、ランダムな操作をラウンドの終わりまで行う。
func stormRandomRun(t *testing.T, seed uint64) ([]DomainEvent, [32]byte) {
	t.Helper()
	cfg := stormTestConfig(
		stormAt(5000, 30000, 5000, [2]uint64{1, 2}),
		stormWin(20000, 60000, 40000, 10000, [2]uint64{2, 3}),
		stormAt(50000, 20000, 0, [2]uint64{1, 2}, [2]uint64{2, 3}),
	)
	e, err := New(cfg, seed, Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m := &stormModel{ids: []string{"1-2", "2-3"}, until: make([]SimTime, 2), unblocked: []SimTime{-1, -1}}
	var all []DomainEvent
	step := func() {
		evs := e.DrainEvents()
		all = append(all, evs...)
		m.apply(t, evs)
		m.check(t, e)
	}
	mustApply(t, e, Command{Type: CmdGameStart})
	step()
	r := rand.New(rand.NewPCG(seed, 1))
	for e.Phase() == PhaseSailing {
		f, _ := e.tr.Ferry("f1")
		switch r.IntN(4) {
		case 0: // load
			if f.State == ferry.Docked {
				if port := e.tr.Port(f.At); len(port) > 0 {
					n := 1 + r.IntN(min(len(port), 4))
					var ids []uint64
					for _, i := range r.Perm(len(port))[:n] {
						ids = append(ids, uint64(port[i]))
					}
					e.Apply(Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: ids})
				}
			}
		case 1: // unload
			if len(f.Cargo) > 0 {
				e.Apply(Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: []uint64{uint64(f.Cargo[r.IntN(len(f.Cargo))])}})
			}
		case 2: // depart（失敗してよい）
			if f.State == ferry.Docked {
				to := neighborOf(r, f.At)
				wasBlocked, err := e.tr.IsBlocked(f.At, to, int64(e.Now()))
				if err != nil {
					t.Fatal(err)
				}
				ack := e.Apply(departCmd(to))
				if ack.Code == CodeRouteBlocked && !wasBlocked {
					t.Fatalf("t=%d: 遮断されていない %d-%d への depart が route_blocked", e.Now(), f.At, to)
				}
				if wasBlocked && ack.OK {
					t.Fatalf("t=%d: 遮断中の %d-%d へ出航できた", e.Now(), f.At, to)
				}
			}
		default:
			e.RunUntil(e.Now() + SimTime(50+r.IntN(1951)))
		}
		step()
	}
	return all, e.TraceHash()
}

// SE-14 ランダムな操作と決定性。
func TestStormRandom(t *testing.T) {
	t.Parallel()
	for seed := uint64(1); seed <= 6; seed++ {
		evs, hash := stormRandomRun(t, seed)
		if len(ofType[RouteBlocked](evs)) == 0 {
			t.Errorf("seed %d: route.blocked が出ない", seed)
		}
		evs2, hash2 := stormRandomRun(t, seed)
		if !reflect.DeepEqual(evs, evs2) || hash != hash2 {
			t.Errorf("seed %d: 同じシードでイベントの列・TraceHash が違う", seed)
		}
	}
}
