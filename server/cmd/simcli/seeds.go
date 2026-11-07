package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// maxSeeds は --seeds で指定できるシードの数の上限。打ち間違いで終わらなくなるのを防ぐ simcli の安全装置で、
// ゲームの数値ではない。
const maxSeeds = 100_000

// parseSeeds は "1..1000"・"7"・"1..3,10,20..22" を、書いた順のシードの並びにする。
// 各要素は N（1 つ）か A..B（両端を含む）。数は 10 進の uint64。空の要素・A > B・片側がない範囲・
// 同じシードの重複・maxSeeds を超える数は誤り。
func parseSeeds(s string) ([]uint64, error) {
	if s == "" {
		return nil, errors.New("--seeds が空")
	}
	var out []uint64
	seen := map[uint64]bool{} // 重複の検出だけに使う（走査しない）
	for i, elem := range strings.Split(s, ",") {
		if elem == "" {
			return nil, fmt.Errorf("--seeds の %d 番目の要素が空", i+1)
		}
		lo, hi, err := parseSeedElem(elem)
		if err != nil {
			return nil, err
		}
		// hi-lo+1 は uint64 で溢れることがあるので、hi-lo と比べる。
		if hi-lo >= uint64(maxSeeds-len(out)) {
			return nil, fmt.Errorf("--seeds のシードの数が上限 %d を超える", maxSeeds)
		}
		for n := lo; ; n++ {
			if seen[n] {
				return nil, fmt.Errorf("--seeds でシード %d が 2 回出る", n)
			}
			seen[n] = true
			out = append(out, n)
			if n == hi {
				break
			}
		}
	}
	return out, nil
}

// parseSeedElem は要素 N か A..B を解釈し、両端を返す（N なら N, N）。
func parseSeedElem(elem string) (lo, hi uint64, err error) {
	a, b, isRange := strings.Cut(elem, "..")
	if !isRange {
		n, err := parseSeedNum(elem)
		return n, n, err
	}
	if a == "" || b == "" {
		return 0, 0, fmt.Errorf("--seeds の範囲 %q の片側がない", elem)
	}
	if lo, err = parseSeedNum(a); err != nil {
		return 0, 0, err
	}
	if hi, err = parseSeedNum(b); err != nil {
		return 0, 0, err
	}
	if lo > hi {
		return 0, 0, fmt.Errorf("--seeds の範囲 %q の始まりが終わりより大きい", elem)
	}
	return lo, hi, nil
}

// parseSeedNum は 10 進の uint64 を解釈する。空白・符号・0x は誤り。
func parseSeedNum(s string) (uint64, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("--seeds の %q は 10 進の uint64 でない", s)
	}
	return n, nil
}
