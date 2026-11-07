//go:build race

package stage_test

// -race では 1 ラウンドが約 10 倍遅いので、シードの数を減らす（CI。データ仕様 §5.6）。
const (
	stage1GreedySeeds = 100
	otherGreedySeeds  = 10
)
