package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// CF-3: OpenData。
func TestOpenData(t *testing.T) {
	t.Parallel()
	fsys, desc, err := OpenData("testdata")
	if err != nil {
		t.Fatalf("OpenData(testdata): %v", err)
	}
	abs, _ := filepath.Abs("testdata")
	if desc != "dir:"+abs {
		t.Errorf("desc = %q, want dir:%s", desc, abs)
	}
	if _, err := LoadCatalog(fsys); err != nil {
		t.Errorf("LoadCatalog: %v", err)
	}

	for _, p := range []string{filepath.Join(t.TempDir(), "nope"), "testdata/islands.json"} {
		if _, _, err := OpenData(p); err == nil {
			t.Errorf("OpenData(%s) succeeded", p)
		}
	}

	if _, ok := stage.Embedded(); ok {
		t.Skip("-tags embeddata のビルド")
	}
	// E-2: タグなしのビルドでは埋め込みがない。
	_, _, err = OpenData("")
	if err == nil {
		t.Fatal(`OpenData("") succeeded without embeddata`)
	}
	for _, s := range []string{"make sync-data", "--data", "DATA_DIR"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("err %q does not mention %q", err, s)
		}
	}
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// CF-4: LoadCatalog。
func TestLoadCatalog(t *testing.T) {
	t.Parallel()
	cat := testCatalog(t)
	stages := cat.Stages()
	if len(stages) != 2 || stages[0].ID != "alpha" || stages[1].ID != "omega" {
		t.Fatalf("Stages() = %v", stages)
	}
	stages[0] = nil // 新しいスライス
	if cat.Stages()[0] == nil {
		t.Error("Stages() shares the slice")
	}
	if st, ok := cat.Lookup("alpha"); !ok || st.ID != "alpha" {
		t.Errorf("Lookup(alpha) = %v, %v", st, ok)
	}
	if _, ok := cat.Lookup("x"); ok {
		t.Error("Lookup(x) found")
	}
	if w := cat.Warnings(); len(w) != 0 {
		t.Errorf("Warnings() = %v", w)
	}

	islands := readTestdata(t, "islands.json")
	alpha := readTestdata(t, "stages/alpha.yaml")

	// SimConfig が誤りになるステージ（preVote: false）は起動を止める。
	bad := strings.Replace(string(alpha), "preVote: true", "preVote: false", 1)
	_, err := LoadCatalog(fstest.MapFS{
		"islands.json":      {Data: islands},
		"stages/alpha.yaml": {Data: []byte(bad)},
	})
	if err == nil || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "preVote") {
		t.Errorf("LoadCatalog(preVote: false) err = %v", err)
	}

	// stages/ が空でもエラーにしない。
	empty, err := LoadCatalog(fstest.MapFS{
		"islands.json":    {Data: islands},
		"stages/.gitkeep": {Data: nil},
	})
	if err != nil || len(empty.Stages()) != 0 {
		t.Errorf("LoadCatalog(empty) = %v, %v", empty, err)
	}

	// islands.json がない・ステージの検証の誤り。
	if _, err := LoadCatalog(fstest.MapFS{"stages/alpha.yaml": {Data: alpha}}); err == nil {
		t.Error("LoadCatalog without islands.json succeeded")
	}
	if _, err := LoadCatalog(fstest.MapFS{
		"islands.json":      {Data: islands},
		"stages/alpha.yaml": {Data: []byte("format: 1\nid: alpha\n")},
	}); err == nil {
		t.Error("LoadCatalog with a broken stage succeeded")
	}
}

// CF-5: リポジトリの data/ を読める。
func TestLoadCatalogRepoData(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "..", "data")
	if _, err := os.Stat(filepath.Join(dir, "islands.json")); err != nil {
		t.Skipf("data/islands.json がない: %v", err)
	}
	if _, err := LoadCatalog(os.DirFS(dir)); err != nil {
		t.Fatalf("LoadCatalog(data): %v", err)
	}
}
