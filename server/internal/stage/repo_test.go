package stage

import (
	"errors"
	"io/fs"
	"os"
	"testing"
)

// R-1 リポジトリの data/islands.json と data/stages/*.yaml を読む。
func TestRepoData(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("../../../data/islands.json"); errors.Is(err, fs.ErrNotExist) {
		t.Skip("data/islands.json がない。提供データを置いて islandsdata で作る（data/raw/README.md）")
	}
	stages, err := LoadDir(os.DirFS("../../.."), "data/islands.json", "data/stages")
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range stages {
		var sum int64
		for _, isl := range st.Islands {
			sum += isl.WeightPermille
		}
		if sum != 1000 {
			t.Errorf("%s: 重みの合計 = %d", st.ID, sum)
		}
		for _, w := range st.Warnings {
			t.Logf("%s: 警告: %s: %s", st.ID, w.Path, w.Message)
		}
	}
}
