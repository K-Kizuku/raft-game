package stage

import (
	"math/rand/v2"
	"reflect"
	"testing"
)

func TestWeightsExamples(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		values []int64
		want   []int64
	}{
		// W-1 データ仕様 §4.1 の Stage 1〜4（populationSqrt の 0.01 単位の値）。
		{"stage1", []int64{16088, 2771, 5542}, []int64{659, 114, 227}},
		{"stage2", []int64{17873, 4390, 12693}, []int64{511, 126, 363}},
		{"stage3", []int64{8427, 1808, 4768, 1797, 8392, 1300}, []int64{318, 68, 180, 68, 317, 49}},
		{"stage4", []int64{22692, 6680, 11590, 8392, 5542, 16845, 17873}, []int64{253, 75, 129, 94, 62, 188, 199}},
		// W-2 同点と端。
		{"同点3", []int64{1, 1, 1}, []int64{334, 333, 333}},
		{"同点7", []int64{1, 1, 1, 1, 1, 1, 1}, []int64{143, 143, 143, 143, 143, 143, 142}},
		{"0を含む", []int64{5, 0, 5}, []int64{500, 0, 500}},
		{"0が2つ", []int64{0, 0, 1}, []int64{0, 0, 1000}},
		{"1つ", []int64{7}, []int64{1000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Weights(tc.values)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Weights(%v) = %v, want %v", tc.values, got, tc.want)
			}
		})
	}
}

// W-3 エラー。
func TestWeightsErrors(t *testing.T) {
	t.Parallel()
	for name, values := range map[string][]int64{
		"空":     nil,
		"負":     {1, -1, 3},
		"すべて0":  {0, 0, 0},
		"大きすぎる": {1, 1_000_000_000_001},
	} {
		if w, err := Weights(values); err == nil {
			t.Errorf("%s: Weights(%v) = %v, エラーにならない", name, values, w)
		}
	}
	if _, err := Weights([]int64{1_000_000_000_000}); err != nil {
		t.Errorf("10^12 ちょうどはよい: %v", err)
	}
}

// W-4 性質。
func TestWeightsProperties(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	for n := 0; n < 1000; {
		values := make([]int64, 1+rng.IntN(7))
		var sum int64
		for i := range values {
			values[i] = rng.Int64N(1_000_001)
			sum += values[i]
		}
		if sum == 0 {
			continue
		}
		n++
		w, err := Weights(values)
		if err != nil {
			t.Fatalf("Weights(%v): %v", values, err)
		}
		var total int64
		for i, v := range values {
			total += w[i]
			base := 1000 * v / sum
			if w[i] != base && w[i] != base+1 {
				t.Fatalf("Weights(%v)[%d] = %d, base %d", values, i, w[i], base)
			}
			for j, u := range values {
				if v > u && w[i] < w[j] {
					t.Fatalf("Weights(%v): v[%d] > v[%d] なのに w %d < %d", values, i, j, w[i], w[j])
				}
			}
		}
		if total != 1000 {
			t.Fatalf("Weights(%v) の合計 = %d", values, total)
		}
	}
}
