package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// リポジトリのデータと生成物（読むだけ）。
var (
	repoData    = filepath.Join("..", "..", "..", "data")
	repoCatalog = filepath.Join("..", "..", "..", "web", "worker", "routes", "stage-catalog.gen.json")
)

// repoRegen は、リポジトリのルートで生成物を作り直すコマンド。
const repoRegen = "go -C server run ./cmd/stagecatalog --data ../data --out ../web/worker/routes/stage-catalog.gen.json"

// REPO-1: リポジトリの data/ から作った出力が、コミットした生成物とバイト列で一致する。
func TestRepoCatalog(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "stage-catalog.gen.json")
	if code := run([]string{"--data", repoData, "--out", out}, io.Discard); code != 0 {
		t.Fatalf("data/ から一覧を作れない（code = %d）", code)
	}
	got := readFile(t, out)
	committed, err := os.ReadFile(repoCatalog)
	if err != nil {
		t.Fatalf("%s を読めない（%v）。%s で作り直す", repoCatalog, err, repoRegen)
	}
	if !bytes.Equal(got, committed) {
		t.Fatalf("web/worker/routes/stage-catalog.gen.json が data/ から作ったものと違う。%s で作り直す", repoRegen)
	}

	var c struct {
		Format int
		Stages []protocol.StageSummary
	}
	dec := json.NewDecoder(bytes.NewReader(committed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("生成物を読めない: %v", err)
	}
	stages, err := stage.LoadDir(os.DirFS(repoData), "islands.json", "stages")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Stages) != len(stages) {
		t.Fatalf("ステージの数 = %d, want %d", len(c.Stages), len(stages))
	}
	for i, s := range c.Stages {
		if s.ID != stages[i].ID {
			t.Errorf("stages[%d].id = %q, want %q（LoadDir の順）", i, s.ID, stages[i].ID)
		}
		if want := strings.HasPrefix(s.ID, "demo-"); s.Demo != want {
			t.Errorf("stages[%d]（%s）.demo = %v, want %v", i, s.ID, s.Demo, want)
		}
		if s.Intro.Text == "" {
			t.Errorf("stages[%d]（%s）.intro.text が空", i, s.ID)
		}
	}
}
