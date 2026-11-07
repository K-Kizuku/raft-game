package sim

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/game"
)

// ★2 でラウンドを終える（T-305b。demo_end.go）のテスト。E-1 の運航で、花火 A の依頼が 9000 に確定する（S-3 と同じ）。

// star2Config は DE-1 の設定（花火 A の依頼 1 件、TargetSuccesses 1、EndOnStar2）。
func star2Config() Config {
	cfg := scoreTestConfig(fireworkReq(0, 1))
	cfg.Score.EndOnStar2 = true
	return cfg
}

// phaseEndedCount は phase.changed{ended} の数を返す。
func phaseEndedCount(evs []DomainEvent) int {
	n := 0
	for _, ev := range evs {
		if ev.Data == (PhaseChanged{Phase: PhaseEnded}) {
			n++
		}
	}
	return n
}

func TestStar2EndsRound(t *testing.T) { // DE-1
	t.Parallel()
	e, all := e1Events(t, star2Config(), 120000)

	if e.Phase() != PhaseEnded {
		t.Fatalf("Phase() = %s, want %s", e.Phase(), PhaseEnded)
	}
	if e.Now() != 9000 {
		t.Errorf("Now() = %d, want 9000", e.Now())
	}
	if n := phaseEndedCount(all); n != 1 {
		t.Errorf("phase.changed{ended} = %d, want 1", n)
	}
	if last := all[len(all)-1]; last.Data != (PhaseChanged{Phase: PhaseEnded}) || last.T != 9000 {
		t.Errorf("last event = %+v, want phase.changed{ended} at 9000", last)
	}

	at9000 := at(all, 9000)
	iSecured := indexOf(at9000, func(d EventData) bool {
		s, ok := d.(ScoreUpdated)
		return ok && slices.Equal(s.CriticalSecured, []string{"firework:a"}) && s.Successes == 1
	})
	scores := ofTypeAt[ScoreUpdated](at9000)
	iEnd := indexOf(at9000, func(d EventData) bool { return d == PhaseChanged{Phase: PhaseEnded} })
	if iSecured < 0 || len(scores) != 2 || iEnd != len(at9000)-1 {
		t.Fatalf("events at 9000 = %+v", datas(at9000))
	}
	// 公平ボーナスの score.updated は phase.changed{ended} の直前。
	if bonus, ok := at9000[iEnd-1].Data.(ScoreUpdated); !ok || iEnd-1 <= iSecured || !reflect.DeepEqual(bonus, scores[1].D) {
		t.Errorf("event before phase.changed = %+v, want the fairness score.updated %+v", at9000[iEnd-1].Data, scores[1].D)
	}
	r := mustResult(t, e)
	if r.Stars < 2 {
		t.Errorf("Stars = %d, want >= 2", r.Stars)
	}
	if r.Score.Breakdown.Fairness == 0 {
		t.Errorf("Fairness = 0, want the fairness bonus")
	}
	checkBreakdown(t, r)
}

func TestStar2AfterEnd(t *testing.T) { // DE-2
	t.Parallel()
	e, _ := e1Events(t, star2Config(), 120000)
	e.RunUntil(120000)
	if evs := e.DrainEvents(); len(evs) != 0 {
		t.Errorf("RunUntil after the end emitted %+v", datas(evs))
	}
	if e.Now() != 9000 {
		t.Errorf("Now() = %d, want 9000", e.Now())
	}
	n := len(e.InputLog())
	if ack := e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: 2}); ack.OK || ack.Code != CodePhaseInvalid {
		t.Errorf("Apply(depart) = %+v, want %s", ack, CodePhaseInvalid)
	}
	if got := len(e.InputLog()); got != n {
		t.Errorf("len(InputLog()) = %d, want %d", got, n)
	}
	if at, ok := e.NextNotableAt(); ok || at != 0 {
		t.Errorf("NextNotableAt() = (%d, %v), want (0, false)", at, ok)
	}
}

// endsLikeWithout は cfg（EndOnStar2 が真）と、同じ設定で EndOnStar2 を偽にしたものが、
// 同じイベントの列と TraceHash で DurationMs に終わることを確かめる。
func endsLikeWithout(t *testing.T, cfg Config) {
	t.Helper()
	off := cfg
	off.Score.EndOnStar2 = false
	e, all := e1Events(t, cfg, cfg.DurationMs)
	eOff, allOff := e1Events(t, off, cfg.DurationMs)
	if e.Phase() != PhaseEnded || e.Now() != cfg.DurationMs {
		t.Errorf("phase %s at %d, want ended at %d", e.Phase(), e.Now(), cfg.DurationMs)
	}
	if !reflect.DeepEqual(all, allOff) {
		t.Errorf("events differ from EndOnStar2 == false")
	}
	if e.TraceHash() != eOff.TraceHash() {
		t.Errorf("TraceHash differs from EndOnStar2 == false")
	}
}

func TestStar2NotReached(t *testing.T) { // DE-3
	t.Parallel()
	t.Run("target", func(t *testing.T) {
		t.Parallel()
		cfg := star2Config()
		cfg.Score.TargetSuccesses = 2
		endsLikeWithout(t, cfg)
	})
	t.Run("critical", func(t *testing.T) {
		t.Parallel()
		cfg := scoreTestConfig(stallReq(0, 1))
		cfg.Score.EndOnStar2 = true
		endsLikeWithout(t, cfg)
	})
}

func TestStar2NoCritical(t *testing.T) { // DE-4
	t.Parallel()
	cfg := scoreTestConfig(stallReq(0, 1))
	cfg.Requests.Critical = nil
	cfg.Score.EndOnStar2 = true
	e, all := e1Events(t, cfg, cfg.DurationMs)
	if e.Phase() != PhaseEnded || e.Now() != 9000 {
		t.Errorf("phase %s at %d, want ended at 9000", e.Phase(), e.Now())
	}
	reqs := ofTypeAt[RequestUpdated](all)
	i := slices.IndexFunc(reqs, func(u timed[RequestUpdated]) bool { return u.D.Status == "succeeded" })
	if i < 0 || reqs[i].T != 9000 {
		t.Errorf("request.updated = %+v, want succeeded at 9000", reqs)
	}
	if n := phaseEndedCount(all); n != 1 {
		t.Errorf("phase.changed{ended} = %d, want 1", n)
	}
	checkBreakdown(t, mustResult(t, e))
}

func TestStar2OffUnchanged(t *testing.T) { // DE-5
	t.Parallel()
	_, on := e1Events(t, star2Config(), 120000)
	cfg := star2Config()
	cfg.Score.EndOnStar2 = false
	e, off := e1Events(t, cfg, cfg.DurationMs)

	if got := at(off, 9000); slices.ContainsFunc(got, func(ev DomainEvent) bool { return ev.Type == EvPhaseChanged }) {
		t.Errorf("phase.changed at 9000 with EndOnStar2 == false: %+v", datas(got))
	}
	if e.Phase() != PhaseEnded || e.Now() != cfg.DurationMs {
		t.Errorf("phase %s at %d, want ended at %d", e.Phase(), e.Now(), cfg.DurationMs)
	}
	// 9000 の score.updated（確定）までが同じ。
	i := slices.IndexFunc(on, func(ev DomainEvent) bool {
		s, ok := ev.Data.(ScoreUpdated)
		return ok && ev.T == 9000 && s.Successes == 1
	})
	if i < 0 || len(off) <= i {
		t.Fatalf("score.updated at 9000 not found (%d)", i)
	}
	if !reflect.DeepEqual(on[:i+1], off[:i+1]) {
		t.Errorf("events up to the score.updated at 9000 differ")
	}
}

func TestStar2Replay(t *testing.T) { // DE-6
	t.Parallel()
	cfg := star2Config()
	e, _ := e1Events(t, cfg, 120000)
	wantRes := mustResult(t, e)
	for _, until := range []SimTime{120000, 9000} {
		r, err := Replay(cfg, "h", ReplayLog{Format: ReplayFormat, StageHash: "h", Seed: testSeed, Entries: e.InputLog(), Until: until}, Options{CheckInvariants: true})
		if err != nil {
			t.Fatalf("Replay(until %d): %v", until, err)
		}
		if r.Phase() != PhaseEnded || r.Now() != 9000 {
			t.Errorf("until %d: phase %s at %d, want ended at 9000", until, r.Phase(), r.Now())
		}
		if r.TraceHash() != e.TraceHash() {
			t.Errorf("until %d: TraceHash differs", until)
		}
		if !reflect.DeepEqual(r.Snapshot(), e.Snapshot()) {
			t.Errorf("until %d: Snapshot differs", until)
		}
		if got := mustResult(t, r); !reflect.DeepEqual(got, wantRes) {
			t.Errorf("until %d: Result() = %+v, want %+v", until, got, wantRes)
		}
	}
	r, err := Replay(cfg, "h", ReplayLog{Format: ReplayFormat, StageHash: "h", Seed: testSeed, Entries: e.InputLog(), Until: 8999}, Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("Replay(until 8999): %v", err)
	}
	if r.Phase() != PhaseSailing {
		t.Errorf("until 8999: phase %s, want %s", r.Phase(), PhaseSailing)
	}
}

func TestStar2NotQueuedAfterApply(t *testing.T) { // DE-7
	t.Parallel()
	e := newStarted(t, star2Config())
	if e.score.endQueued {
		t.Errorf("endQueued after game.start")
	}
	runE1(t, e, func(name string) {
		want := name == "9000"
		if e.score.endQueued != want {
			t.Errorf("after %s: endQueued = %v, want %v", name, e.score.endQueued, want)
		}
	})
}

func TestStar2InvalidConfig(t *testing.T) { // DE-8
	t.Parallel()
	cfg := star2Config()
	cfg.Score = game.ScoreConfig{EndOnStar2: true}
	if _, err := New(cfg, testSeed, Options{}); !errors.Is(err, game.ErrInvalidScoreConfig) {
		t.Errorf("New = %v, want ErrInvalidScoreConfig", err)
	}
}
