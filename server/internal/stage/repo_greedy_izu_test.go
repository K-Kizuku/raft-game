package stage_test

import (
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// 伊豆版のデモ（demo-izu）を greedy・steady で遊ばせ、進行表 §8 の 3 つの条件を確かめる（T-501e #308）。
// デモの嵐（D キーのコマンド）はボットからは起こせないので、ステージの写しの events に、demo.storm と同じ航路・長さの
// kind: storm（at 30000、予報なし）を足して再現する（進行表 §8 の計測と同じ方法）。

// izuStormAt はデモの嵐を起こす時刻（進行表 §8 の台本の 0:30）。
const izuStormAt = 30000

// izuBots は条件を確かめるボット（進行表 §8・T-501e の計測と同じ 2 つ）。novice（初心者の想定）は対象にしない。
var izuBots = []string{"greedy", "steady"}

// izuRun は 1 シードの結果のうち、条件に使う値。
type izuRun struct {
	star2      bool        // ラウンドの終わりに★2 以上
	down       sim.SimTime // 嵐のあと、最初の代表（島 1）が最初におりた時刻。おりていなければ -1
	newLeader  sim.SimTime // 嵐のあと、ほかの島が最初に代表になった時刻。なければ -1
	steppedAny bool        // どこかの島が代表をおりた
}

func summarizeIzu(res sim.RoundResult, events []sim.DomainEvent, from sim.SimTime) izuRun {
	r := izuRun{star2: res.Stars >= 2, down: -1, newLeader: -1}
	for _, ev := range events {
		switch d := ev.Data.(type) {
		case sim.SteppedDown:
			r.steppedAny = true
			if d.ID == 1 && ev.T >= from && r.down < 0 {
				r.down = ev.T
			}
		case sim.LeaderElected:
			if d.ID != 1 && ev.T >= from && r.newLeader < 0 {
				r.newLeader = ev.T
			}
		}
	}
	return r
}

// izuWithStorm は st の写しに、demo.storm と同じ航路・長さの嵐（at、予報なし）を足したものを返す。
func izuWithStorm(t *testing.T, st *stage.Stage, at int64) *stage.Stage {
	t.Helper()
	if st.Demo == nil || st.Demo.Storm == nil {
		t.Fatalf("%s: demo.storm がない", st.ID)
	}
	cp := *st
	cp.Events = append(append([]stage.Event{}, st.Events...), stage.Event{
		Kind:       stage.EventStorm,
		When:       stage.Window{Start: at, End: at},
		DurationMs: st.Demo.Storm.DurationMs,
		Routes:     append([][2]uint64{}, st.Demo.Storm.Routes...),
	})
	return &cp
}

// playIzuSeeds は st をシード 1..n でボット name に遊ばせる（Engine はシードごとに作る）。
func playIzuSeeds(t *testing.T, name string, st *stage.Stage, n int, from sim.SimTime) []izuRun {
	t.Helper()
	runs := make([]izuRun, n)
	for seed := 1; seed <= n; seed++ {
		res, events := playGreedy(t, name, st, uint64(seed))
		runs[seed-1] = summarizeIzu(res, events, from)
	}
	return runs
}

// GR-4 demo-izu: (a) 嵐で代表（大島）が孤立したとき、代表がおりてから 20 秒以内（おりる前を含む）に新しい代表が決まる
// シードが 8 割以上、(b) 嵐があっても★2 に届くシードが 8 割以上、(c) 嵐がないとき代表がおりるシードが 1 割以下（進行表 §8）。
func TestRepoGreedyDemoIzu(t *testing.T) {
	t.Parallel()
	base := repoStage(t, "demo-izu")
	if !base.Raft.CheckQuorum {
		t.Fatal("demo-izu: raft.checkQuorum = false, want true")
	}
	storm := izuWithStorm(t, base, izuStormAt)
	n := otherGreedySeeds
	for _, name := range izuBots {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			calm := playIzuSeeds(t, name, base, n, 0)
			stormy := playIzuSeeds(t, name, storm, n, izuStormAt)

			var a, b, c, during int
			for _, r := range stormy {
				if r.newLeader >= 0 && (r.down < 0 || r.newLeader-r.down <= 20000) {
					a++
				}
				if r.newLeader >= 0 && r.newLeader < sim.SimTime(izuStormAt+storm.Demo.Storm.DurationMs) {
					during++
				}
				if r.star2 {
					b++
				}
			}
			for _, r := range calm {
				if r.steppedAny {
					c++
				}
			}
			t.Logf("%s/demo-izu: (a) %d/%d・嵐の間に新しい代表 %d/%d・(b) %d/%d・(c) %d/%d", name, a, n, during, n, b, n, c, n)
			if a*10 < n*8 {
				t.Errorf("%s: (a) 代表がおりてから 20 秒以内に新しい代表が決まったシード = %d / %d（8 割未満）", name, a, n)
			}
			if b*10 < n*8 {
				t.Errorf("%s: (b) 嵐があっても★2 に届いたシード = %d / %d（8 割未満）", name, b, n)
			}
			if c*10 > n {
				t.Errorf("%s: (c) 嵐がないのに代表がおりたシード = %d / %d（1 割より多い）", name, c, n)
			}
		})
	}
}
