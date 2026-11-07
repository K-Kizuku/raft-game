//go:build balance

package stage_test

import (
	"fmt"
	"testing"
)

// M2 の完了条件「greedy で Stage 1 の★1 が 90% 以上のシードで取れる」を確かめる（T-207 の GR-1 の割合の確認）。
// steady（T-501c #257）も同じ条件で確かめる（ボットの変更で Stage 1 の★1 の割合を下げないため）。
// ビルドタグ balance を付けて必須のテスト（CI）から外している（T-207 の時点では 0/1000 だったため）。
// 0/1000 の原因は greedy の積み込み（直行の手紙を先に選び、遠い島宛ての MsgApp が港で溢れた）で、T-207b（#203）で直した。値の調整は T-501 で行う。
//
//	go -C server test -tags balance -count=1 -run Greedy -v ./internal/stage/...
func TestRepoGreedyStage1Balance(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"greedy", "steady"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st := repoStage(t, "stage1")
			runs := playGreedySeeds(t, name, st, stage1GreedySeeds)
			logGreedySummary(t, name+"/"+st.ID, runs)

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
				t.Fatalf("%s: ★1 以上のシード = %d / %d（90%% 未満）", name, n1, stage1GreedySeeds)
			}
		})
	}
}

// stage1NoviceSeeds は、Stage 1 の難しさを確かめるシードの数（シード 1〜200。T-501f #311）。
const stage1NoviceSeeds = 200

// stage1Stars は、Stage 1 をシード 1..stage1NoviceSeeds でボット name に遊ばせ、★が minStars 以上のシードの数を返す。
func stage1Stars(t *testing.T, name string, minStars int) int {
	t.Helper()
	st := repoStage(t, "stage1")
	runs := playGreedySeeds(t, name, st, stage1NoviceSeeds)
	logGreedySummary(t, name+"/"+st.ID, runs)
	n := 0
	for _, r := range runs {
		if r.res.Stars >= minStars {
			n++
		}
	}
	return n
}

// Stage 1 は、初めての人がクリアできる難しさにする（T-501f #311）。初心者の代わりのボット novice の★1 の割合が 70% 以上。
//
//	go -C server test -tags balance -count=1 -run Novice -v ./internal/stage/...
func TestRepoNoviceStage1Balance(t *testing.T) {
	t.Parallel()
	if n := stage1Stars(t, "novice", 1); n*10 < stage1NoviceSeeds*7 {
		t.Fatalf("novice: ★1 以上のシード = %d / %d（70%% 未満）", n, stage1NoviceSeeds)
	}
}

// Stage 1 の難しさの幅（T-501f #311）。ランダムに動く random の★1 の割合が 30% 以上で、
// 上手な人の greedy が★3 に届く割合が 90% 以下（上手な人に伸びしろを残す）。
//
//	go -C server test -tags balance -count=1 -run Stage1Spread -v ./internal/stage/...
func TestRepoStage1SpreadBalance(t *testing.T) {
	t.Parallel()
	t.Run("random", func(t *testing.T) {
		t.Parallel()
		if n := stage1Stars(t, "random", 1); n*10 < stage1NoviceSeeds*3 {
			t.Fatalf("random: ★1 以上のシード = %d / %d（30%% 未満）", n, stage1NoviceSeeds)
		}
	})
	t.Run("greedy", func(t *testing.T) {
		t.Parallel()
		if n := stage1Stars(t, "greedy", 3); n*10 > stage1NoviceSeeds*9 {
			t.Fatalf("greedy: ★3 のシード = %d / %d（90%% より多い）", n, stage1NoviceSeeds)
		}
	})
}
