package stage_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/bot"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// リポジトリのステージを greedy ボットで遊ばせる（T-207。M2 の完了条件「greedy で Stage 1 の★1 が 90% 以上のシードで取れる」）。
// シードの数は repo_greedy_race_test.go・repo_greedy_norace_test.go にある（-race で変える）。

// greedyChunk は 1 つのサブテストで回すシードの数。
const greedyChunk = 50

// playGreedy は st を seed で greedy に 1 ラウンド遊ばせ、結果とイベントの列を返す。
func playGreedy(t *testing.T, st *stage.Stage, seed uint64) (sim.RoundResult, []sim.DomainEvent) {
	t.Helper()
	cfg, err := stage.SimConfig(st)
	if err != nil {
		t.Fatalf("%s: seed %d: SimConfig: %v", st.ID, seed, err)
	}
	b, err := bot.New("greedy", seed)
	if err != nil {
		t.Fatalf("%s: seed %d: bot.New: %v", st.ID, seed, err)
	}
	e, err := sim.New(cfg, seed, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("%s: seed %d: sim.New: %v", st.ID, seed, err)
	}
	if err := bot.Play(e, b); err != nil {
		t.Fatalf("%s: seed %d: bot.Play: %v", st.ID, seed, err)
	}
	res, ok := e.Result()
	if !ok {
		t.Fatalf("%s: seed %d: Result が ok == false（phase %s）", st.ID, seed, e.Phase())
	}
	return res, e.DrainEvents()
}

// greedyRun は 1 シードの結果のうち、集計に使う値。
type greedyRun struct {
	res          sim.RoundResult
	steppedDown  int         // node.stepped_down の数
	elected      int         // node.leader_elected の数（T > 0 のものだけ。開会式の 1 回を除く）
	firstStepped sim.SimTime // 最初の node.stepped_down の時刻
	hasStepped   bool
}

func summarizeRun(res sim.RoundResult, events []sim.DomainEvent) greedyRun {
	r := greedyRun{res: res}
	for _, ev := range events {
		switch ev.Type {
		case sim.EvSteppedDown:
			if !r.hasStepped {
				r.firstStepped, r.hasStepped = ev.T, true
			}
			r.steppedDown++
		case sim.EvLeaderElected:
			if ev.T > 0 {
				r.elected++
			}
		}
	}
	return r
}

// playGreedySeeds は st をシード 1..n で遊ばせ、seed-1 の添字に結果を入れて返す。
// シードを greedyChunk 個ずつに分けて並列に回す。Engine はシードごとに作るので、並列にしても結果は変わらない。
func playGreedySeeds(t *testing.T, st *stage.Stage, n int) []greedyRun {
	t.Helper()
	runs := make([]greedyRun, n)
	t.Run("play", func(t *testing.T) {
		for lo := 1; lo <= n; lo += greedyChunk {
			hi := min(lo+greedyChunk-1, n)
			t.Run(fmt.Sprintf("seeds-%d-%d", lo, hi), func(t *testing.T) {
				t.Parallel()
				for seed := lo; seed <= hi; seed++ {
					res, events := playGreedy(t, st, uint64(seed))
					runs[seed-1] = summarizeRun(res, events) // 塊ごとに書く添字が違うので、ロックは要らない
				}
			})
		}
	})
	if t.Failed() {
		t.FailNow()
	}
	return runs
}

// logGreedySummary は、ステージの集計を 1 行ずつ出す（T-501 の目安）。平均・中央値は整数で計算する。
func logGreedySummary(t *testing.T, id string, runs []greedyRun) {
	t.Helper()
	n := int64(len(runs))
	if n == 0 {
		return
	}
	var stars [4]int
	succMin, succMax, succSum := runs[0].res.Stats.Succeeded, runs[0].res.Stats.Succeeded, int64(0)
	scoreMin, scoreMax, scoreSum := runs[0].res.Score.Total, runs[0].res.Score.Total, int64(0)
	var rejected, expired int64
	var stepped, elected, noStepped int
	var firsts []sim.SimTime
	for _, r := range runs {
		s := r.res.Stars
		if s >= 0 && s < len(stars) {
			stars[s]++
		}
		succ, score := r.res.Stats.Succeeded, r.res.Score.Total
		succMin, succMax, succSum = min(succMin, succ), max(succMax, succ), succSum+succ
		scoreMin, scoreMax, scoreSum = min(scoreMin, score), max(scoreMax, score), scoreSum+score
		rejected += r.res.Stats.Rejected
		expired += r.res.Stats.Expired
		stepped += r.steppedDown
		elected += r.elected
		if r.hasStepped {
			firsts = append(firsts, r.firstStepped)
		} else {
			noStepped++
		}
	}
	t.Logf("%s: シード %d 個", id, n)
	t.Logf("%s: ★の分布 0:%d 1:%d 2:%d 3:%d", id, stars[0], stars[1], stars[2], stars[3])
	t.Logf("%s: Stats.Succeeded 最小 %d・平均 %d・最大 %d", id, succMin, succSum/n, succMax)
	t.Logf("%s: Score.Total 最小 %d・平均 %d・最大 %d", id, scoreMin, scoreSum/n, scoreMax)
	t.Logf("%s: Stats.Rejected の合計 %d・Stats.Expired の合計 %d", id, rejected, expired)
	t.Logf("%s: node.stepped_down の合計 %d・node.leader_elected（T > 0）の合計 %d", id, stepped, elected)
	if len(firsts) > 0 {
		slices.Sort(firsts)
		t.Logf("%s: 最初の node.stepped_down の時刻 最小 %d ms・中央値 %d ms・一度もなかったシード %d 個",
			id, firsts[0], firsts[(len(firsts)-1)/2], noStepped)
	} else {
		t.Logf("%s: 最初の node.stepped_down の時刻 なし・一度もなかったシード %d 個", id, noStepped)
	}
}

// GR-1 Stage 1 の★1 が 90% 以上のシードで取れる。
func TestRepoGreedyStage1(t *testing.T) {
	t.Parallel()
	st := repoStage(t, "stage1")
	runs := playGreedySeeds(t, st, stage1GreedySeeds)
	logGreedySummary(t, st.ID, runs)

	n1 := 0
	var misses []string
	for i, r := range runs {
		if r.res.Stars >= 1 {
			n1++
		} else if len(misses) < 10 {
			misses = append(misses, fmt.Sprintf("seed %d: Stars %d・Stats %+v・Score %d", i+1, r.res.Stars, r.res.Stats, r.res.Score.Total))
		}
	}
	if n1*10 < stage1GreedySeeds*9 {
		for _, m := range misses {
			t.Log(m)
		}
		t.Fatalf("★1 以上のシード = %d / %d（90%% 未満）", n1, stage1GreedySeeds)
	}
}

// GR-2 stage2・demo-goto・demo-izu がエラー・不変条件の違反なしで終わる。★の割合は比べず、集計を出すだけ。
func TestRepoGreedyOthers(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"stage2", "demo-goto", "demo-izu"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			st := repoStage(t, id)
			runs := playGreedySeeds(t, st, otherGreedySeeds)
			logGreedySummary(t, st.ID, runs)
		})
	}
}

// GR-3 ステージ定義から作った設定でも、同じシードなら結果とイベントの列が一致する（全体は T-111）。
func TestRepoGreedyDeterministic(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"stage1", "stage2"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			st := repoStage(t, id)
			res1, ev1 := playGreedy(t, st, 1)
			res2, ev2 := playGreedy(t, st, 1)
			if !reflect.DeepEqual(res1, res2) {
				t.Errorf("RoundResult が一致しない: %+v / %+v", res1, res2)
			}
			if !reflect.DeepEqual(ev1, ev2) {
				t.Errorf("イベントの列が一致しない（%d 件 / %d 件）", len(ev1), len(ev2))
			}
		})
	}
}
