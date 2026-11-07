package stage_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/bot"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// リポジトリのデモのステージが ★2 でラウンドを終える（T-305b。データ仕様 §5.4・§5.6）。

// RD-1 demo-* だけが EndOnStar2。
func TestRepoDemoEndOnStar2(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]bool{"demo-izu": true, "demo-goto": true, "stage1": false, "stage2": false} {
		cfg, err := stage.SimConfig(repoStage(t, id))
		if err != nil {
			t.Fatalf("%s: SimConfig: %v", id, err)
		}
		if cfg.Score.EndOnStar2 != want {
			t.Errorf("%s: Score.EndOnStar2 = %v, want %v", id, cfg.Score.EndOnStar2, want)
		}
	}
}

// RD-2 demo-goto を greedy で遊ばせ、★2 以上のラウンドは早く終わり、再生でも同じに終わる。
func TestRepoDemoGotoStar2End(t *testing.T) {
	t.Parallel()
	st := repoStage(t, "demo-goto")
	cfg, err := stage.SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	reached := 0
	for seed := uint64(1); seed <= 20; seed++ {
		b, err := bot.New("greedy", seed)
		if err != nil {
			t.Fatalf("seed %d: bot.New: %v", seed, err)
		}
		e, err := sim.New(cfg, seed, sim.Options{CheckInvariants: true})
		if err != nil {
			t.Fatalf("seed %d: sim.New: %v", seed, err)
		}
		if err := bot.Play(e, b); err != nil {
			t.Fatalf("seed %d: bot.Play: %v", seed, err)
		}
		res, ok := e.Result()
		if !ok {
			t.Fatalf("seed %d: Result が ok == false（phase %s）", seed, e.Phase())
		}
		if res.Stars < 2 {
			continue
		}
		reached++
		if e.Now() >= sim.SimTime(st.DurationMs) {
			t.Errorf("seed %d: Now() = %d, want < %d", seed, e.Now(), st.DurationMs)
		}
		evs := e.DrainEvents()
		if len(evs) == 0 || evs[len(evs)-1].Data != (sim.PhaseChanged{Phase: sim.PhaseEnded}) {
			t.Errorf("seed %d: 最後のイベントが phase.changed{ended} でない", seed)
		}
		var last *sim.ScoreUpdated
		for i := len(evs) - 1; i >= 0; i-- {
			if s, ok := evs[i].Data.(sim.ScoreUpdated); ok {
				last = &s
				break
			}
		}
		if last == nil || !slices.Equal(last.CriticalSecured, cfg.Requests.Critical) {
			t.Errorf("seed %d: 最後の score.updated = %+v, criticalSecured が %v でない", seed, last, cfg.Requests.Critical)
		}

		r, err := sim.Replay(cfg, st.Hash, sim.ReplayLog{
			Format: sim.ReplayFormat, StageHash: st.Hash, Seed: seed, Entries: e.InputLog(), Until: sim.SimTime(st.DurationMs),
		}, sim.Options{CheckInvariants: true})
		if err != nil {
			t.Fatalf("seed %d: Replay: %v", seed, err)
		}
		if r.Phase() != e.Phase() || r.Now() != e.Now() || r.TraceHash() != e.TraceHash() {
			t.Errorf("seed %d: Replay は phase %s・t %d（元は %s・%d）、または TraceHash が違う", seed, r.Phase(), r.Now(), e.Phase(), e.Now())
		}
		if rr, _ := r.Result(); !reflect.DeepEqual(rr, res) {
			t.Errorf("seed %d: Replay の Result() = %+v, want %+v", seed, rr, res)
		}
	}
	if reached == 0 {
		t.Log("demo-goto: シード 1..20 の greedy で ★2 以上のラウンドがない（T-501 の調整で変わる）")
	}
	t.Logf("demo-goto: ★2 以上のラウンド %d / 20", reached)
}
