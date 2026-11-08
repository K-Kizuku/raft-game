package game

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"testing"
)

const breakdownDuration = 120000

var breakdownFerries = []string{"f1", "f2", "f3"}

func newBreakdownRNG(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, 11)) }

// GB-1 at だけ・船を指定 → 乱数を引かない。
func TestPlanBreakdownsFixed(t *testing.T) {
	t.Parallel()
	defs := []BreakdownDef{
		{WindowStart: 30000, WindowEnd: 30000, DurationMs: 20000, Ferry: "f2"},
		{WindowStart: 0, WindowEnd: 0, DurationMs: 1, Ferry: "f1"},
	}
	rng := newBreakdownRNG(1)
	got, err := PlanBreakdowns(defs, breakdownFerries, breakdownDuration, rng)
	if err != nil {
		t.Fatal(err)
	}
	want := []Breakdown{{At: 30000, DurationMs: 20000, Ferry: "f2"}, {At: 0, DurationMs: 1, Ferry: "f1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if !sameNext(rng, newBreakdownRNG(1)) {
		t.Error("at と船の指定だけなのに乱数を引いた")
	}
}

// 空の定義 → nil、乱数を引かない。
func TestPlanBreakdownsEmpty(t *testing.T) {
	t.Parallel()
	rng := newBreakdownRNG(2)
	got, err := PlanBreakdowns(nil, breakdownFerries, breakdownDuration, rng)
	if err != nil || got != nil {
		t.Fatalf("got %+v, %v; want nil, nil", got, err)
	}
	if !sameNext(rng, newBreakdownRNG(2)) {
		t.Error("空の定義で乱数を引いた")
	}
}

// GB-2 window・any → 同じシードで同じ結果、引く順が「時刻 → 船」。
func TestPlanBreakdownsWindowAny(t *testing.T) {
	t.Parallel()
	defs := []BreakdownDef{
		{WindowStart: 80000, WindowEnd: 120000, DurationMs: 20000, Ferry: ""},
		{WindowStart: 10000, WindowEnd: 10000, DurationMs: 5000, Ferry: ""},
		{WindowStart: 1000, WindowEnd: 2000, DurationMs: 5000, Ferry: "f3"},
	}
	for seed := range uint64(20) {
		a, err := PlanBreakdowns(defs, breakdownFerries, breakdownDuration, newBreakdownRNG(seed))
		if err != nil {
			t.Fatal(err)
		}
		b, err := PlanBreakdowns(defs, breakdownFerries, breakdownDuration, newBreakdownRNG(seed))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("seed %d: 同じシードで結果が違う: %+v / %+v", seed, a, b)
		}
		// 同じ順で引いた値と一致する。
		r := newBreakdownRNG(seed)
		at0 := int64(80000) + r.Int64N(40001)
		f0 := breakdownFerries[r.IntN(3)]
		f1 := breakdownFerries[r.IntN(3)]
		at2 := int64(1000) + r.Int64N(1001)
		want := []Breakdown{
			{At: at0, DurationMs: 20000, Ferry: f0},
			{At: 10000, DurationMs: 5000, Ferry: f1},
			{At: at2, DurationMs: 5000, Ferry: "f3"},
		}
		if !reflect.DeepEqual(a, want) {
			t.Fatalf("seed %d: got %+v, want %+v", seed, a, want)
		}
	}
}

// ferry: any で船が 1 隻 → その船。
func TestPlanBreakdownsSingleFerry(t *testing.T) {
	t.Parallel()
	got, err := PlanBreakdowns([]BreakdownDef{{WindowStart: 5, WindowEnd: 5, DurationMs: 1}}, []string{"only"}, 10, newBreakdownRNG(3))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Ferry != "only" {
		t.Errorf("ferry = %q, want only", got[0].Ferry)
	}
}

// GB-3 検査。
func TestPlanBreakdownsInvalid(t *testing.T) {
	t.Parallel()
	valid := func() BreakdownDef {
		return BreakdownDef{WindowStart: 10000, WindowEnd: 20000, DurationMs: 5000, Ferry: "f1"}
	}
	cases := []struct {
		name    string
		mod     func(*BreakdownDef)
		ferries []string
	}{
		{"windowStart<0", func(d *BreakdownDef) { d.WindowStart = -1 }, breakdownFerries},
		{"windowの逆順", func(d *BreakdownDef) { d.WindowEnd = d.WindowStart - 1 }, breakdownFerries},
		{"windowEnd>durationMs", func(d *BreakdownDef) { d.WindowEnd = breakdownDuration + 1 }, breakdownFerries},
		{"durationMs=0", func(d *BreakdownDef) { d.DurationMs = 0 }, breakdownFerries},
		{"知らない船", func(d *BreakdownDef) { d.Ferry = "f9" }, breakdownFerries},
		{"ferriesが空", func(d *BreakdownDef) { d.Ferry = "" }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := valid()
			tc.mod(&d)
			// 1 つ目は幅があり any。2 つ目が誤りでも乱数を引かない。
			first := BreakdownDef{WindowStart: 0, WindowEnd: 1000, DurationMs: 1}
			rng := newBreakdownRNG(4)
			got, err := PlanBreakdowns([]BreakdownDef{first, d}, tc.ferries, breakdownDuration, rng)
			if !errors.Is(err, ErrInvalidBreakdown) {
				t.Fatalf("err = %v, want ErrInvalidBreakdown", err)
			}
			if got != nil {
				t.Errorf("誤りのときの結果 = %+v, want nil", got)
			}
			if !sameNext(rng, newBreakdownRNG(4)) {
				t.Error("誤りのときに乱数を引いた")
			}
		})
	}
}
