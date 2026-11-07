package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// loadStage は dataDir のステージをすべて読んで検証し、id のステージを返す。
// dataDir/islands.json と dataDir/stages/*.yaml を読む。読み込み・検証の誤りは stage のエラーをそのまま返す。
func loadStage(dataDir, id string) (*stage.Stage, error) {
	stages, err := stage.LoadDir(os.DirFS(dataDir), "islands.json", "stages")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(stages))
	for _, st := range stages {
		if st.ID == id {
			return st, nil
		}
		ids = append(ids, st.ID)
	}
	have := "なし"
	if len(ids) > 0 {
		have = strings.Join(ids, ", ")
	}
	return nil, fmt.Errorf("ステージ %s がない（あるもの: %s）", id, have)
}

// loadStageReport は loadStage を呼び、誤りなら stderr に出して nil を返す。
// ステージの警告は 1 件ずつ stderr に出す（失敗にしない）。
func loadStageReport(dataDir, id string, stderr io.Writer) *stage.Stage {
	st, err := loadStage(dataDir, id)
	if err != nil {
		printErr(stderr, err)
		return nil
	}
	for _, w := range st.Warnings {
		_, _ = fmt.Fprintf(stderr, "simcli: 警告: %s: %s\n", w.Path, w.Message)
	}
	return st
}

// newEngine はステージとシードから Engine を作る。stage.Stage → sim.Config の変換は T-202 の関数
// （stage.SimConfig）だけを使う。simcli の中で Engine を作るのはこの関数だけ。
func newEngine(st *stage.Stage, seed uint64, invariants bool) (*sim.Engine, error) {
	cfg, err := stage.SimConfig(st)
	if err != nil {
		return nil, fmt.Errorf("simcli: %s: %w", st.ID, err)
	}
	e, err := sim.New(cfg, seed, sim.Options{CheckInvariants: invariants})
	if err != nil {
		return nil, fmt.Errorf("simcli: %s: %w", st.ID, err)
	}
	return e, nil
}
