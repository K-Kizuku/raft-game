//go:build !race

package stage_test

// -race なしでは、M2 の完了条件どおり Stage 1 を 1000 シードで確かめる（データ仕様 §5.6）。
const (
	stage1GreedySeeds = 1000
	otherGreedySeeds  = 100
)
