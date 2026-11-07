package game

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

const rushDuration = 180000

var rushKinds = []RandomKind{
	{Kind: "firework.reserve", Resource: "firework:a", Weight: 1},
	{Kind: "stall.reserve", Resource: "stall:main", Weight: 3},
}

func newRushRNG(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, 13)) }

// rushIslands は依頼の島を決めた順に返す。
func rushIslands(r Rush) []uint64 {
	out := make([]uint64, len(r.Requests))
	for i, q := range r.Requests {
		out[i] = q.Island
	}
	return out
}

// GR-1 at・requests: 3・島 4（重みすべて 1）→ 島が重複なし、Islands が ID の昇順、同じシードで同じ結果。
func TestPlanRushDistinct(t *testing.T) {
	t.Parallel()
	defs := []RushDef{{WindowStart: 60000, WindowEnd: 60000, Requests: 3, ForecastMs: 5000}}
	weights := []int64{1, 1, 1, 1}
	for seed := range uint64(50) {
		got, err := PlanRush(defs, weights, rushKinds, rushDuration, newRushRNG(seed))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		r := got[0]
		if r.At != 60000 || !r.HasForecast || r.ForecastAt != 55000 {
			t.Errorf("seed %d: at %d forecast %v %d", seed, r.At, r.HasForecast, r.ForecastAt)
		}
		if len(r.Requests) != 3 {
			t.Fatalf("seed %d: requests = %d, want 3", seed, len(r.Requests))
		}
		ids := rushIslands(r)
		sorted := slices.Clone(ids)
		slices.Sort(sorted)
		if len(slices.Compact(sorted)) != 3 {
			t.Errorf("seed %d: islands %v are not distinct", seed, ids)
		}
		if !slices.Equal(r.Islands, sorted) {
			t.Errorf("seed %d: Islands = %v, want %v", seed, r.Islands, sorted)
		}
		for _, q := range r.Requests {
			if q.At != 60000 || q.Island < 1 || q.Island > 4 {
				t.Errorf("seed %d: request %+v", seed, q)
			}
			if !slices.ContainsFunc(rushKinds, func(k RandomKind) bool { return k.Kind == q.Kind && k.Resource == q.Resource }) {
				t.Errorf("seed %d: unknown kind %+v", seed, q)
			}
		}
		again, err := PlanRush(defs, weights, rushKinds, rushDuration, newRushRNG(seed))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, again) {
			t.Errorf("seed %d: not deterministic: %+v vs %+v", seed, got, again)
		}
	}
}

// GR-2 requests: 5・島 3 → 3 件で全部の島を使い、残り 2 件は 2 巡目（重複なし）。
func TestPlanRushSecondRound(t *testing.T) {
	t.Parallel()
	defs := []RushDef{{WindowStart: 1000, WindowEnd: 9000, Requests: 5}}
	weights := []int64{500, 300, 200}
	for seed := range uint64(50) {
		got, err := PlanRush(defs, weights, rushKinds, rushDuration, newRushRNG(seed))
		if err != nil {
			t.Fatal(err)
		}
		r := got[0]
		if r.HasForecast || r.At < 1000 || r.At > 9000 {
			t.Errorf("seed %d: rush %+v", seed, r)
		}
		ids := rushIslands(r)
		first := slices.Clone(ids[:3])
		slices.Sort(first)
		if !slices.Equal(first, []uint64{1, 2, 3}) {
			t.Errorf("seed %d: first round %v, want all islands", seed, ids[:3])
		}
		if ids[3] == ids[4] {
			t.Errorf("seed %d: second round %v has a duplicate", seed, ids[3:])
		}
		if !slices.Equal(r.Islands, []uint64{1, 2, 3}) {
			t.Errorf("seed %d: Islands = %v", seed, r.Islands)
		}
	}
}

// GR-3 重み 0 の島は選ばれない（2 巡目でも）。
func TestPlanRushZeroWeight(t *testing.T) {
	t.Parallel()
	defs := []RushDef{{WindowStart: 0, WindowEnd: 0, Requests: 7}}
	weights := []int64{0, 400, 0, 600}
	for seed := range uint64(50) {
		got, err := PlanRush(defs, weights, rushKinds, rushDuration, newRushRNG(seed))
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range rushIslands(got[0]) {
			if id == 1 || id == 3 {
				t.Errorf("seed %d: island %d has weight 0", seed, id)
			}
		}
		if !slices.Equal(got[0].Islands, []uint64{2, 4}) {
			t.Errorf("seed %d: Islands = %v", seed, got[0].Islands)
		}
	}
}

// 島の選び方は重みに比例する（1 件だけなら重みの大きい島が多く選ばれる）。
func TestPlanRushProportional(t *testing.T) {
	t.Parallel()
	defs := []RushDef{{WindowStart: 0, WindowEnd: 0, Requests: 1}}
	weights := []int64{900, 100}
	var count [2]int
	for seed := range uint64(400) {
		got, err := PlanRush(defs, weights, rushKinds, rushDuration, newRushRNG(seed))
		if err != nil {
			t.Fatal(err)
		}
		count[got[0].Requests[0].Island-1]++
	}
	if count[0] < 300 || count[1] == 0 {
		t.Errorf("counts = %v, want about 360:40", count)
	}
}

// GR-4 検査: requests: 0・forecastMs > windowStart・kinds が空 ほか → ErrInvalidRush。defs が空 → 乱数を引かない。
func TestPlanRushInvalid(t *testing.T) {
	t.Parallel()
	ok := RushDef{WindowStart: 10000, WindowEnd: 20000, Requests: 2, ForecastMs: 5000}
	weights := []int64{1, 1}
	cases := []struct {
		name    string
		def     RushDef
		weights []int64
		kinds   []RandomKind
	}{
		{"requests 0", RushDef{WindowStart: 10000, WindowEnd: 10000, Requests: 0}, weights, rushKinds},
		{"forecastMs > windowStart", RushDef{WindowStart: 10000, WindowEnd: 10000, Requests: 1, ForecastMs: 10001}, weights, rushKinds},
		{"forecastMs < 0", RushDef{WindowStart: 10000, WindowEnd: 10000, Requests: 1, ForecastMs: -1}, weights, rushKinds},
		{"kinds empty", ok, weights, nil},
		{"kinds weight 0", ok, weights, []RandomKind{{Kind: "stall.reserve", Resource: "stall:main", Weight: 0}}},
		{"weights 0", ok, []int64{0, 0}, rushKinds},
		{"weights empty", ok, nil, rushKinds},
		{"windowStart < 0", RushDef{WindowStart: -1, WindowEnd: 0, Requests: 1}, weights, rushKinds},
		{"windowEnd < windowStart", RushDef{WindowStart: 10, WindowEnd: 9, Requests: 1}, weights, rushKinds},
		{"windowEnd > duration", RushDef{WindowStart: 10, WindowEnd: rushDuration + 1, Requests: 1}, weights, rushKinds},
	}
	for _, c := range cases {
		rng := newRushRNG(3)
		got, err := PlanRush([]RushDef{ok, c.def}, c.weights, c.kinds, rushDuration, rng)
		if !errors.Is(err, ErrInvalidRush) || got != nil {
			t.Errorf("%s: got %+v, %v, want ErrInvalidRush", c.name, got, err)
		}
		if !sameNext(rng, newRushRNG(3)) {
			t.Errorf("%s: drew random numbers before validation", c.name)
		}
	}

	rng := newRushRNG(4)
	got, err := PlanRush(nil, nil, nil, rushDuration, rng)
	if got != nil || err != nil {
		t.Errorf("empty: got %+v, %v", got, err)
	}
	if !sameNext(rng, newRushRNG(4)) {
		t.Error("empty: drew random numbers")
	}
}

// ラッシュの時刻が durationMs ちょうどでも決まる。
func TestPlanRushAtDuration(t *testing.T) {
	t.Parallel()
	got, err := PlanRush([]RushDef{{WindowStart: rushDuration, WindowEnd: rushDuration, Requests: 1}}, []int64{1}, rushKinds, rushDuration, newRushRNG(5))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].At != rushDuration || got[0].Requests[0].At != rushDuration {
		t.Errorf("got %+v", got)
	}
}
