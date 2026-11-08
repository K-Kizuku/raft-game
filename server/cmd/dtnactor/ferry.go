package main

import (
	"errors"
	"fmt"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/ferryactor"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/runtime"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// newFerryActor は ROLE=ferry の Actor（DTN-04 #161）を作る。ステージ定義をすべて読み、init の stage で引く。
func newFerryActor(env Env) (runtime.Actor, error) {
	stages, err := loadStages(env)
	if err != nil {
		return nil, err
	}
	return ferryactor.New(stages), nil
}

// loadStages は env.Data の islands.json と stages/*.yaml を読む。
func loadStages(env Env) ([]*stage.Stage, error) {
	if env.Data == nil {
		return nil, errors.New("no stage data (build with -tags embeddata after make sync-data, or set DATA_DIR)")
	}
	stages, err := stage.LoadDir(env.Data, "islands.json", "stages")
	if err != nil {
		return nil, fmt.Errorf("load stages (%s): %w", env.DataDesc, err)
	}
	if len(stages) == 0 {
		return nil, fmt.Errorf("load stages (%s): no stages", env.DataDesc)
	}
	return stages, nil
}
