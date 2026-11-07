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
	for _, name := range repoBots {
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
