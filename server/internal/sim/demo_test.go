package sim

import (
	"errors"
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// demoTestConfig は testConfig（3 島・航路 1-2・2-3）にデモの嵐（航路 1-2 を 20 秒）を入れた設定。
func demoTestConfig() Config {
	c := testConfig()
	c.DemoStorm = &DemoStorm{Routes: [][2]uint64{{1, 2}}, DurationMs: 20000}
	return c
}

var demoStormCmd = Command{Type: CmdDemoTrigger, Event: DemoEventStorm}

// demoEntries は入力ログのうち demo.trigger のエントリだけを返す。
func demoEntries(log []InputEntry) []InputEntry {
	var out []InputEntry
	for _, en := range log {
		if en.Cmd.Type == CmdDemoTrigger {
			out = append(out, en)
		}
	}
	return out
}

// DS-1 New の検査。
func TestDemoStormConfig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ds   *DemoStorm
		want error // nil なら文だけ確かめる
	}{
		{"durationMs=0", &DemoStorm{Routes: [][2]uint64{{1, 2}}, DurationMs: 0}, nil},
		{"routes が空", &DemoStorm{DurationMs: 20000}, nil},
		{"航路がない", &DemoStorm{Routes: [][2]uint64{{1, 3}}, DurationMs: 20000}, ferry.ErrNoRoute},
		{"航路がない（2 つ目）", &DemoStorm{Routes: [][2]uint64{{2, 1}, {3, 1}}, DurationMs: 20000}, ferry.ErrNoRoute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig()
			cfg.DemoStorm = tc.ds
			e, err := New(cfg, testSeed, Options{})
			if err == nil || e != nil {
				t.Fatalf("New = (%v, %v), want error", e, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("New の err = %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		if _, err := New(testConfig(), testSeed, Options{}); err != nil {
			t.Fatalf("New: %v", err)
		}
	})

	t.Run("設定の複製", func(t *testing.T) {
		t.Parallel()
		cfg := demoTestConfig()
		e := newEngine(t, cfg, Options{CheckInvariants: true})
		cfg.DemoStorm.Routes[0] = [2]uint64{2, 3}
		cfg.DemoStorm.DurationMs = 1
		mustApply(t, e, Command{Type: CmdGameStart})
		e.DrainEvents()
		mustApply(t, e, demoStormCmd)
		checkStormEvents(t, e.DrainEvents(), []stormEv{blocked(0, 20000, "1-2")})
	})
}

// DS-2 成功。
func TestDemoStormTrigger(t *testing.T) {
	t.Parallel()
	e := startedEngineWith(t, demoTestConfig(), Options{CheckInvariants: true})
	e.RunUntil(5000)
	e.DrainEvents()
	ack := mustApply(t, e, demoStormCmd)
	if want := (Ack{OK: true, T: 5000}); ack != want {
		t.Errorf("Apply = %+v, want %+v", ack, want)
	}
	evs := e.DrainEvents()
	checkStormEvents(t, evs, []stormEv{blocked(5000, 25000, "1-2")})
	if n := len(ofType[Forecast](evs)); n != 0 {
		t.Errorf("forecast %d 件, want 0", n)
	}
	log := e.InputLog()
	if got, want := log[len(log)-1], (InputEntry{T: 5000, Cmd: Command{Type: "demo.trigger", Event: "storm"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("InputLog の最後 = %+v, want %+v", got, want)
	}
	until := SimTime(25000)
	if got, want := e.Snapshot().Routes, []RouteView{{ID: "1-2", BlockedUntil: &until}, {ID: "2-3"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot().Routes = %+v, want %+v", got, want)
	}
	mustFailCode(t, e, departCmd(2), CodeRouteBlocked)
}

// DS-3 終わり。
func TestDemoStormEnd(t *testing.T) {
	t.Parallel()
	e := startedEngineWith(t, demoTestConfig(), Options{CheckInvariants: true})
	e.RunUntil(5000)
	mustApply(t, e, demoStormCmd)
	e.DrainEvents()
	checkNotable(t, e, 25000, true)
	checkStormEvents(t, runAll(e, 25000), []stormEv{unblocked(25000, "1-2")})
	mustApply(t, e, departCmd(2))
}

// DS-4 失敗しても何も変わらない。
func TestDemoStormFail(t *testing.T) {
	t.Parallel()
	check := func(t *testing.T, e *Engine, cmd Command, code string) {
		t.Helper()
		e.DrainEvents()
		now, n, hash := e.Now(), len(e.InputLog()), e.TraceHash()
		ack := e.Apply(cmd)
		if ack.OK || ack.Code != code || ack.T != now {
			t.Errorf("Apply(%+v) = %+v, want %s at %d", cmd, ack, code, now)
		}
		if e.Now() != now || len(e.InputLog()) != n || e.TraceHash() != hash {
			t.Errorf("失敗で状態が変わった: now %d→%d, input %d→%d", now, e.Now(), n, len(e.InputLog()))
		}
		if evs := e.DrainEvents(); len(evs) != 0 {
			t.Errorf("イベントが出た: %+v", evs)
		}
	}

	t.Run("Intro", func(t *testing.T) {
		t.Parallel()
		e := newEngine(t, demoTestConfig(), Options{CheckInvariants: true})
		check(t, e, demoStormCmd, CodePhaseInvalid)
	})
	t.Run("終了後", func(t *testing.T) {
		t.Parallel()
		e := startedEngineWith(t, demoTestConfig(), Options{CheckInvariants: true})
		e.RunUntil(120000)
		if e.Phase() != PhaseEnded {
			t.Fatalf("Phase = %s, want ended", e.Phase())
		}
		check(t, e, demoStormCmd, CodePhaseInvalid)
	})
	t.Run("event", func(t *testing.T) {
		t.Parallel()
		e := startedEngineWith(t, demoTestConfig(), Options{CheckInvariants: true})
		e.RunUntil(5000)
		check(t, e, Command{Type: CmdDemoTrigger}, CodeBadRequest)
		check(t, e, Command{Type: CmdDemoTrigger, Event: "rush"}, CodeBadRequest)
	})
	t.Run("demo.storm がない", func(t *testing.T) {
		t.Parallel()
		e := startedEngineWith(t, testConfig(), Options{CheckInvariants: true})
		e.RunUntil(5000)
		check(t, e, demoStormCmd, CodeBadRequest)
	})
}

// DS-5 2 回起こす。
func TestDemoStormTwice(t *testing.T) {
	t.Parallel()

	t.Run("嵐の途中", func(t *testing.T) {
		t.Parallel()
		e := startedEngineWith(t, demoTestConfig(), Options{CheckInvariants: true})
		e.RunUntil(5000)
		mustApply(t, e, demoStormCmd)
		checkStormEvents(t, runAll(e, 10000), []stormEv{blocked(5000, 25000, "1-2")})
		mustApply(t, e, demoStormCmd)
		checkStormEvents(t, e.DrainEvents(), []stormEv{blocked(10000, 30000, "1-2")})
		checkStormEvents(t, runAll(e, 25000), nil)
		checkStormEvents(t, runAll(e, 120000), []stormEv{unblocked(30000, "1-2")})
	})

	t.Run("同じ時刻", func(t *testing.T) {
		t.Parallel()
		e := startedEngineWith(t, demoTestConfig(), Options{CheckInvariants: true})
		e.RunUntil(5000)
		mustApply(t, e, demoStormCmd)
		mustApply(t, e, demoStormCmd)
		checkStormEvents(t, e.DrainEvents(), []stormEv{blocked(5000, 25000, "1-2")})
		want := []InputEntry{{T: 5000, Cmd: demoStormCmd}, {T: 5000, Cmd: demoStormCmd}}
		if got := demoEntries(e.InputLog()); !reflect.DeepEqual(got, want) {
			t.Errorf("入力ログの demo.trigger = %+v, want %+v", got, want)
		}
		checkStormEvents(t, runAll(e, 120000), []stormEv{unblocked(25000, "1-2")})
	})
}

// DS-6 リプレイ。
func TestDemoStormReplay(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		run  func(t *testing.T, e *Engine)
	}{
		{"1 回", func(t *testing.T, e *Engine) {
			e.RunUntil(3000)
			mustApply(t, e, departCmd(2))
			e.RunUntil(5000)
			mustApply(t, e, demoStormCmd)
			e.RunUntil(25000)
			mustApply(t, e, departCmd(1))
			e.RunUntil(40000)
		}},
		{"途中でもう一度", func(t *testing.T, e *Engine) {
			e.RunUntil(5000)
			mustApply(t, e, demoStormCmd)
			e.RunUntil(10000)
			mustApply(t, e, demoStormCmd)
			e.RunUntil(28000)
		}},
		{"同じ時刻に 2 回", func(t *testing.T, e *Engine) {
			e.RunUntil(5000)
			mustApply(t, e, demoStormCmd)
			mustApply(t, e, demoStormCmd)
			e.RunUntil(26000)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := demoTestConfig()
			e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
			tc.run(t, e)
			r, err := Replay(cfg, rpStageHash, rpLog(testSeed, e.InputLog(), e.Now()), Options{CheckInvariants: true})
			if err != nil {
				t.Fatalf("Replay: %v", err)
			}
			if r.TraceHash() != e.TraceHash() {
				t.Error("TraceHash が元と違う")
			}
			if got, want := r.Snapshot(), e.Snapshot(); !reflect.DeepEqual(got, want) {
				t.Errorf("Snapshot が元と違う:\n got  %+v\n want %+v", got, want)
			}
			rpCheckState(t, tc.name, rpCapture(r), rpCapture(e))
		})
	}

	t.Run("demo.storm のないステージ", func(t *testing.T) {
		t.Parallel()
		e := startedEngineWith(t, demoTestConfig(), Options{CheckInvariants: true})
		e.RunUntil(5000)
		mustApply(t, e, demoStormCmd)
		_, err := Replay(testConfig(), rpStageHash, rpLog(testSeed, e.InputLog(), e.Now()), Options{})
		if !errors.Is(err, ErrReplayDiverged) {
			t.Errorf("Replay の err = %v, want ErrReplayDiverged", err)
		}
	})
}

// DS-7 設定だけでは結果が変わらない（乱数を引かない）。
func TestDemoStormConfigOnly(t *testing.T) {
	t.Parallel()
	run := func(cfg Config) ([]DomainEvent, [32]byte) {
		e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
		e.RunUntil(9000)
		mustApply(t, e, departCmd(2))
		e.RunUntil(cfg.DurationMs)
		return e.DrainEvents(), e.TraceHash()
	}
	a, ha := run(testConfig())
	b, hb := run(demoTestConfig())
	if ha != hb || !reflect.DeepEqual(a, b) {
		t.Error("DemoStorm の有無で結果が違う")
	}
	_, hs := seededRun(t, testConfig(), testSeed)
	_, hd := seededRun(t, demoTestConfig(), testSeed)
	if hs != hd {
		t.Error("DemoStorm の有無で TraceHash が違う（コマンドなし）")
	}
}

// DS-8 終わりがラウンドの後。
func TestDemoStormPastRoundEnd(t *testing.T) {
	t.Parallel()
	cfg := demoTestConfig()
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	e.RunUntil(cfg.DurationMs - 1000)
	e.DrainEvents()
	if ack := mustApply(t, e, demoStormCmd); ack.T != 119000 {
		t.Errorf("Apply の T = %d, want 119000", ack.T)
	}
	checkStormEvents(t, e.DrainEvents(), []stormEv{blocked(119000, 139000, "1-2")})
	evs := runAll(e, 200000)
	if n := len(ofType[RouteUnblocked](evs)); n != 0 {
		t.Errorf("route.unblocked %d 件, want 0", n)
	}
	if e.Phase() != PhaseEnded {
		t.Errorf("Phase = %s, want ended", e.Phase())
	}
	until := SimTime(139000)
	if got := e.Snapshot().Routes[0].BlockedUntil; got == nil || *got != until {
		t.Errorf("Snapshot().Routes[0].BlockedUntil = %v, want %d", got, until)
	}
}
