package main

import (
	"github.com/K-Kizuku/raft-game/server/internal/dtn/island"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/runtime"
)

// newIslandActor は ROLE=island の Actor（DTN-03 #160）を作る。ステージ定義をすべて読み、init の stage で引く。
func newIslandActor(env Env) (runtime.Actor, error) {
	stages, err := loadStages(env)
	if err != nil {
		return nil, err
	}
	return island.New(stages), nil
}
