//go:build embeddata

package stage

import (
	"os"
	"testing"
)

// E-1: make sync-data の後、埋め込んだステージ定義が data/ と同じに読める（-tags embeddata のときだけ）。
func TestEmbedded(t *testing.T) {
	t.Parallel()
	fsys, ok := Embedded()
	if !ok {
		t.Fatal("Embedded() = false（make sync-data を実行したか）")
	}
	got, err := LoadDir(fsys, "islands.json", "stages")
	if err != nil {
		t.Fatalf("LoadDir(embedded): %v", err)
	}
	want, err := LoadDir(os.DirFS("../../../data"), "islands.json", "stages")
	if err != nil {
		t.Fatalf("LoadDir(data): %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("ステージの数 = %d, data/ は %d（make sync-data をやり直す）", len(got), len(want))
	}
	for i := range got {
		if got[i].ID != want[i].ID || got[i].Hash != want[i].Hash {
			t.Errorf("stages[%d] = %s %s, data/ は %s %s", i, got[i].ID, got[i].Hash, want[i].ID, want[i].Hash)
		}
	}
}
