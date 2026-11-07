package stage

import (
	"errors"
	"fmt"
	"slices"
)

// maxWeightValue は Weights が受け付ける値の上限（1000 × v のあふれを防ぐ）。
const maxWeightValue = 1_000_000_000_000

// Weights は values の比で 1000 を分け、合計がちょうど 1000 の千分率の整数を返す（データ仕様 §4.1）。
// 整数の最大剰余法: base_i = ⌊1000 × v_i / S⌋、rem_i = 1000 × v_i mod S とし、
// 1000 − Σ base_i 個の島に 1 ずつ足す。足す島は rem_i の大きい順で、同じなら添字の小さい順。
func Weights(values []int64) ([]int64, error) {
	if len(values) == 0 {
		return nil, errors.New("stage: 重みの値が空")
	}
	var sum int64
	for i, v := range values {
		if v < 0 {
			return nil, fmt.Errorf("stage: 重みの値 [%d] が負（%d）", i, v)
		}
		if v > maxWeightValue {
			return nil, fmt.Errorf("stage: 重みの値 [%d] が 10^12 を超える（%d）", i, v)
		}
		if sum > (1<<63-1)-v {
			return nil, errors.New("stage: 重みの値の合計が大きすぎる")
		}
		sum += v
	}
	if sum == 0 {
		return nil, errors.New("stage: 重みの値の合計が 0")
	}
	w := make([]int64, len(values))
	rem := make([]int64, len(values))
	var total int64
	for i, v := range values {
		w[i] = permille * v / sum
		rem[i] = permille * v % sum
		total += w[i]
	}
	order := make([]int, len(values))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		switch {
		case rem[a] > rem[b]:
			return -1
		case rem[a] < rem[b]:
			return 1
		}
		return a - b
	})
	for k := int64(0); k < permille-total; k++ {
		w[order[k]]++
	}
	return w, nil
}
