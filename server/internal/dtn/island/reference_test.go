package island_test

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/island"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/islandcore"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/simtrace"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// referenceStages は分散モードで遊べるステージ（simulation-time.md §6。stage3 は欠航を定義しているので対象外）。
var referenceStages = []string{"stage1", "stage2", "demo-goto", "demo-izu"}

// streamOf は正規化トレースの列 name（なければ空）。
func streamOf(tr simtrace.Trace, name string) []simtrace.Entry {
	for _, s := range tr.Streams {
		if s.Name == name {
			return s.Events
		}
	}
	return nil
}

// compareStream は 2 つの列の (t, type, 中身) の並びを比べ、最初の食い違いを返す（一致すれば ""）。
func compareStream(ref, got []simtrace.Entry) string {
	for i := range min(len(ref), len(got)) {
		r, g := ref[i], got[i]
		if r.T != g.T || r.Type != g.Type || string(r.Data) != string(g.Data) {
			return fmt.Sprintf("event %d differs:\nref t=%d %s %s\ngot t=%d %s %s", i, r.T, r.Type, r.Data, g.T, g.Type, g.Data)
		}
	}
	if len(ref) != len(got) {
		return fmt.Sprintf("%d events vs %d", len(ref), len(got))
	}
	return ""
}

// endMark は参照モデルのラウンドの終わり（durationMs の ClassGameTimer）。島のイベントはこれより前まで処理される。
func endMark(cfg sim.Config) islandcore.Mark {
	return islandcore.Mark{T: int64(cfg.DurationMs), Class: 7}
}

// runReferenceIdle は参照モデルを cmd.game.start だけで最後まで動かし、正規化トレースを返す。
func runReferenceIdle(t testing.TB, st *stage.Stage, seed uint64) simtrace.Trace {
	t.Helper()
	cfg := simConfig(t, st)
	e, err := sim.New(cfg, seed, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if ack := e.Apply(sim.Command{Type: sim.CmdGameStart}); !ack.OK {
		t.Fatalf("start: %+v", ack)
	}
	e.RunUntil(cfg.DurationMs)
	return simtrace.Normalize(e.DrainEvents())
}

// advanceRandomly は、ばらばらの大きさの上限で end まで advance し、出力をつなげて返す。
func advanceRandomly(t testing.TB, a *island.Actor, r *rand.Rand, end islandcore.Mark) []dtnproto.Output {
	t.Helper()
	var outs []dtnproto.Output
	h := islandcore.Mark{}
	for h.Less(end) {
		next := islandcore.Mark{T: h.T + int64(r.IntN(3000)), Class: islandcore.Class(r.IntN(9))}
		if next.Less(h) {
			next = h // 同じ上限の 2 回目（出力なし）も混ぜる
		}
		if end.Less(next) {
			next = end
		}
		outs = append(outs, advance(t, a, next)...)
		h = next
	}
	return outs
}

// TestIslandMatchesReferenceWithoutCommands は、コマンドなし（船が動かないので島は手紙を受け取らない）の
// 4 ステージ × seed 1..50 で、島の Actor を init とばらばらの advance だけで動かした列が、参照モデルの
// island:<id> の列と一致することを確かめる（AC-2）。
func TestIslandMatchesReferenceWithoutCommands(t *testing.T) {
	seeds := uint64(50)
	if testing.Short() {
		seeds = 5
	}
	for _, sid := range referenceStages {
		t.Run(sid, func(t *testing.T) {
			t.Parallel()
			st := repoStage(t, sid)
			cfg := simConfig(t, st)
			for seed := uint64(1); seed <= seeds; seed++ {
				ref := runReferenceIdle(t, st, seed)
				for id := uint64(1); id <= uint64(cfg.Islands); id++ {
					a, outs := newIsland(t, st, seed, id)
					outs = append(outs, advanceRandomly(t, a, rand.New(rand.NewPCG(seed, id)), endMark(cfg))...)
					got := simtrace.Normalize(domainEvents(t, outs))
					name := fmt.Sprintf("island:%d", id)
					refStream := streamOf(ref, name)
					if len(refStream) == 0 {
						t.Fatalf("seed %d: reference has no %s stream", seed, name)
					}
					if d := compareStream(refStream, streamOf(got, name)); d != "" {
						t.Fatalf("seed %d %s: %s", seed, name, d)
					}
				}
			}
		})
	}
}
