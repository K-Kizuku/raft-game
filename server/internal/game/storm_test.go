package game

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"testing"
)

const (
	stormIslands  = 3
	stormDuration = 120000
)

func newStormRNG(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, 7)) }

// sameNext は、a と b の次の値が同じか（a を何回引いたかを、新しく作った b と比べて確かめる）。
func sameNext(a, b *rand.Rand) bool { return a.Uint64() == b.Uint64() }

// validStorm は誤りのない嵐（各ケースで 1 か所だけ変える）。
func validStorm() StormDef {
	return StormDef{WindowStart: 10000, WindowEnd: 20000, DurationMs: 5000, Routes: [][2]uint64{{1, 2}}, ForecastMs: 5000}
}

// GS-1 検査。
func TestPlanStormsInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mod  func(*StormDef)
	}{
		{"windowStart<0", func(d *StormDef) { d.WindowStart = -1 }},
		{"windowEnd<windowStart", func(d *StormDef) { d.WindowEnd = d.WindowStart - 1 }},
		{"windowEnd>durationMs", func(d *StormDef) { d.WindowEnd = stormDuration + 1 }},
		{"durationMs=0", func(d *StormDef) { d.DurationMs = 0 }},
		{"routes空", func(d *StormDef) { d.Routes = nil }},
		{"島0", func(d *StormDef) { d.Routes = [][2]uint64{{0, 1}} }},
		{"島4", func(d *StormDef) { d.Routes = [][2]uint64{{1, 4}} }},
		{"同じ島", func(d *StormDef) { d.Routes = [][2]uint64{{2, 2}} }},
		{"向き違いの重複", func(d *StormDef) { d.Routes = [][2]uint64{{1, 2}, {2, 1}} }},
		{"forecast<0", func(d *StormDef) { d.ForecastMs = -1 }},
		{"forecast>windowStart", func(d *StormDef) { d.ForecastMs = d.WindowStart + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := validStorm()
			tc.mod(&d)
			got, err := PlanStorms([]StormDef{d}, stormIslands, stormDuration, newStormRNG(1))
			if !errors.Is(err, ErrInvalidStorm) {
				t.Fatalf("err = %v, want ErrInvalidStorm", err)
			}
			if got != nil {
				t.Errorf("誤りのときの結果 = %+v, want nil", got)
			}
		})
		// 2 つ目の定義が誤りのときは、乱数を 1 回も引かない（1 つ目は幅がある）。
		t.Run(tc.name+"/2つ目", func(t *testing.T) {
			t.Parallel()
			d := validStorm()
			tc.mod(&d)
			rng := newStormRNG(2)
			if _, err := PlanStorms([]StormDef{validStorm(), d}, stormIslands, stormDuration, rng); !errors.Is(err, ErrInvalidStorm) {
				t.Fatalf("err = %v, want ErrInvalidStorm", err)
			}
			if !sameNext(rng, newStormRNG(2)) {
				t.Error("検査の誤りのときに乱数を引いた")
			}
		})
	}
}

// GS-1 誤りにならないもの。
func TestPlanStormsValidEdges(t *testing.T) {
	t.Parallel()
	for _, defs := range [][]StormDef{nil, {}} {
		got, err := PlanStorms(defs, stormIslands, stormDuration, nil)
		if err != nil || got != nil {
			t.Errorf("PlanStorms(%v) = (%v, %v), want (nil, nil)", defs, got, err)
		}
	}
	cases := []struct {
		name string
		def  StormDef
		want Storm
	}{
		{
			"終わりちょうど",
			StormDef{WindowStart: stormDuration, WindowEnd: stormDuration, DurationMs: 1, Routes: [][2]uint64{{1, 2}}},
			Storm{Start: stormDuration, End: stormDuration + 1, Routes: [][2]uint64{{1, 2}}},
		},
		{
			"forecast=windowStart",
			StormDef{WindowStart: 3000, WindowEnd: 3000, DurationMs: 10, Routes: [][2]uint64{{3, 2}}, ForecastMs: 3000},
			Storm{Start: 3000, End: 3010, HasForecast: true, ForecastAt: 0, Routes: [][2]uint64{{3, 2}}},
		},
		{
			"forecast=0",
			StormDef{WindowStart: 3000, WindowEnd: 3000, DurationMs: 10, Routes: [][2]uint64{{1, 3}}},
			Storm{Start: 3000, End: 3010, Routes: [][2]uint64{{1, 3}}},
		},
		{
			"終わりがdurationMsを超える",
			StormDef{WindowStart: 100000, WindowEnd: 100000, DurationMs: 50000, Routes: [][2]uint64{{1, 2}, {2, 3}}},
			Storm{Start: 100000, End: 150000, Routes: [][2]uint64{{1, 2}, {2, 3}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := PlanStorms([]StormDef{tc.def}, stormIslands, stormDuration, newStormRNG(1))
			if err != nil {
				t.Fatalf("PlanStorms: %v", err)
			}
			if want := []Storm{tc.want}; !reflect.DeepEqual(got, want) {
				t.Errorf("PlanStorms = %+v, want %+v", got, want)
			}
		})
	}
}

// GS-2 at だけなら乱数を引かない。
func TestPlanStormsAtOnly(t *testing.T) {
	t.Parallel()
	defs := []StormDef{
		{WindowStart: 10000, WindowEnd: 10000, DurationMs: 20000, Routes: [][2]uint64{{2, 1}}, ForecastMs: 5000},
		{WindowStart: 0, WindowEnd: 0, DurationMs: 1000, Routes: [][2]uint64{{2, 3}}, ForecastMs: 0},
	}
	rng := newStormRNG(3)
	got, err := PlanStorms(defs, stormIslands, stormDuration, rng)
	if err != nil {
		t.Fatalf("PlanStorms: %v", err)
	}
	want := []Storm{
		{Start: 10000, End: 30000, HasForecast: true, ForecastAt: 5000, Routes: [][2]uint64{{2, 1}}},
		{Start: 0, End: 1000, HasForecast: false, ForecastAt: 0, Routes: [][2]uint64{{2, 3}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PlanStorms = %+v, want %+v", got, want)
	}
	if !sameNext(rng, newStormRNG(3)) {
		t.Error("at だけの嵐で乱数を引いた")
	}
}

// GS-3 引く順序。
func TestPlanStormsDrawOrder(t *testing.T) {
	t.Parallel()
	r12 := [][2]uint64{{1, 2}}
	defs := []StormDef{
		{WindowStart: 10000, WindowEnd: 10000, DurationMs: 1000, Routes: r12, ForecastMs: 1000},
		{WindowStart: 20000, WindowEnd: 30000, DurationMs: 2000, Routes: r12, ForecastMs: 2000},
		{WindowStart: 40000, WindowEnd: 40000, DurationMs: 3000, Routes: r12, ForecastMs: 3000},
		{WindowStart: 0, WindowEnd: 3, DurationMs: 4000, Routes: r12, ForecastMs: 0},
	}
	for seed := uint64(1); seed <= 20; seed++ {
		ref := newStormRNG(seed)
		s2 := 20000 + ref.Int64N(10001)
		s4 := ref.Int64N(4)
		want := []Storm{
			{Start: 10000, End: 11000, HasForecast: true, ForecastAt: 9000, Routes: r12},
			{Start: s2, End: s2 + 2000, HasForecast: true, ForecastAt: s2 - 2000, Routes: r12},
			{Start: 40000, End: 43000, HasForecast: true, ForecastAt: 37000, Routes: r12},
			{Start: s4, End: s4 + 4000, Routes: r12},
		}
		got, err := PlanStorms(defs, stormIslands, stormDuration, newStormRNG(seed))
		if err != nil {
			t.Fatalf("seed %d: PlanStorms: %v", seed, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("seed %d: PlanStorms = %+v, want %+v", seed, got, want)
		}
	}
}

// GS-4 範囲は両端を含む。
func TestPlanStormsRange(t *testing.T) {
	t.Parallel()
	defs := []StormDef{{WindowStart: 0, WindowEnd: 2, DurationMs: 1, Routes: [][2]uint64{{1, 2}}}}
	var seen [3]bool
	for seed := uint64(1); seed <= 200; seed++ {
		got, err := PlanStorms(defs, stormIslands, stormDuration, newStormRNG(seed))
		if err != nil {
			t.Fatalf("seed %d: PlanStorms: %v", seed, err)
		}
		s := got[0].Start
		if s < 0 || s > 2 {
			t.Fatalf("seed %d: Start = %d, want 0..2", seed, s)
		}
		seen[s] = true
	}
	if seen != [3]bool{true, true, true} {
		t.Errorf("出た値 = %v, want 0・1・2 のすべて", seen)
	}
}

// GS-5 同じシードなら同じ結果。
func TestPlanStormsDeterministic(t *testing.T) {
	t.Parallel()
	defs := []StormDef{
		{WindowStart: 5000, WindowEnd: 90000, DurationMs: 1000, Routes: [][2]uint64{{1, 2}}, ForecastMs: 5000},
		{WindowStart: 1000, WindowEnd: 2000, DurationMs: 1000, Routes: [][2]uint64{{3, 2}, {1, 2}}},
	}
	a, err := PlanStorms(defs, stormIslands, stormDuration, newStormRNG(42))
	if err != nil {
		t.Fatalf("PlanStorms: %v", err)
	}
	b, err := PlanStorms(defs, stormIslands, stormDuration, newStormRNG(42))
	if err != nil {
		t.Fatalf("PlanStorms: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("同じシードで結果が違う:\n%+v\n%+v", a, b)
	}
}

// GS-6 複製。
func TestStormClones(t *testing.T) {
	t.Parallel()
	if got := CloneStormDefs(nil); got != nil {
		t.Errorf("CloneStormDefs(nil) = %v, want nil", got)
	}
	defs := []StormDef{{WindowStart: 1000, WindowEnd: 1000, DurationMs: 10, Routes: [][2]uint64{{1, 2}, {2, 3}}}}
	clone := CloneStormDefs(defs)
	if !reflect.DeepEqual(clone, defs) {
		t.Fatalf("CloneStormDefs = %+v, want %+v", clone, defs)
	}
	clone[0].Routes[0] = [2]uint64{3, 2}
	clone[0].DurationMs = 99
	plan, err := PlanStorms(defs, stormIslands, stormDuration, newStormRNG(1))
	if err != nil {
		t.Fatalf("PlanStorms: %v", err)
	}
	plan[0].Routes[1] = [2]uint64{1, 3}
	want := []StormDef{{WindowStart: 1000, WindowEnd: 1000, DurationMs: 10, Routes: [][2]uint64{{1, 2}, {2, 3}}}}
	if !reflect.DeepEqual(defs, want) {
		t.Errorf("元の defs が変わった: %+v", defs)
	}
}
