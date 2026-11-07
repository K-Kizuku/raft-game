package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"testing"
)

// repoData はリポジトリの data/（提供データとステージ定義）。
const repoData = "../../../data"

// relayHash は st の relayCap を relayCap にして、ボット botName・seed で 1 ラウンドを回したトレースのハッシュを返す。
func relayHash(t *testing.T, stageID string, relayCap int, botName string, seed uint64) string {
	t.Helper()
	st, err := loadStage(repoData, stageID)
	if err != nil {
		t.Fatal(err)
	}
	c := *st
	c.Transport.RelayCap = relayCap
	res, _, _, err := playSeed(&c, seed, botName, false)
	if err != nil {
		t.Fatalf("%s/%s/seed %d/relayCap %d: %v", stageID, botName, seed, relayCap, err)
	}
	return res.TraceHash
}

// CL-R2・SR-9: リポジトリのステージ × ボットで、relayCap を 0 にした設定とステージのまま（12）の設定の
// トレースのハッシュが同じ（ボットは積み替えを使わないので、relayCap を渡しても何も変わらない）。
func TestRelayCapDoesNotChangeBotTraces(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(repoData + "/islands.json"); errors.Is(err, fs.ErrNotExist) {
		t.Skip("リポジトリの data/ がない")
	}
	type tc struct {
		stage, bot string
		seed       uint64
	}
	var cases []tc
	for _, s := range []string{"stage1", "stage2", "demo-izu", "demo-goto"} {
		for _, b := range []string{"greedy", "steady"} {
			for _, seed := range []uint64{1, 7} {
				cases = append(cases, tc{s, b, seed})
			}
		}
	}
	// SR-9: ボット greedy・steady・random、シード 1..3（bot は sim を import するので、sim のテストではなくここで確かめる）。
	for _, b := range []string{"greedy", "steady", "random"} {
		for _, seed := range []uint64{1, 2, 3} {
			cases = append(cases, tc{"stage1", b, seed})
		}
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%s/%d", c.stage, c.bot, c.seed), func(t *testing.T) {
			t.Parallel()
			st, err := loadStage(repoData, c.stage)
			if err != nil {
				t.Fatal(err)
			}
			if st.Transport.RelayCap == 0 {
				t.Fatalf("%s: relayCap is 0 (precondition)", c.stage)
			}
			withCap := relayHash(t, c.stage, st.Transport.RelayCap, c.bot, c.seed)
			without := relayHash(t, c.stage, 0, c.bot, c.seed)
			if withCap == "" || withCap != without {
				t.Errorf("seed %d: relayCap %d → %s, relayCap 0 → %s", c.seed, st.Transport.RelayCap, withCap, without)
			}
		})
	}
}
