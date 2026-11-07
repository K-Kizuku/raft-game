package game

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"testing"
)

const cancellationDuration = 240000

func newCancellationRNG(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, 17)) }

// skipDraws は rng から n 回 IntN(1000) を引く。
func skipDraws(rng *rand.Rand, n int) {
	for range n {
		rng.IntN(1000)
	}
}

// GC-1 1 つの定義 → 7 回引き（30000〜210000）、同じシードで同じ結果。Start・End は区間の境目と +10000。
func TestPlanCancellationsOne(t *testing.T) {
	t.Parallel()
	defs := []CancellationDef{{Island: 4, ProbabilityPermille: 410, SlotMs: 30000, DurationMs: 10000}}
	for seed := range uint64(20) {
		rng := newCancellationRNG(seed)
		got, err := PlanCancellations(defs, 6, cancellationDuration, rng)
		if err != nil {
			t.Fatal(err)
		}
		// 期待値を同じシードから手で引いて作る。
		hand := newCancellationRNG(seed)
		var want []Cancellation
		for k := int64(1); k <= 7; k++ {
			if hand.IntN(1000) < 410 {
				want = append(want, Cancellation{Island: 4, Start: k * 30000, End: k*30000 + 10000})
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: got %+v, want %+v", seed, got, want)
		}
		if !sameNext(rng, hand) {
			t.Fatalf("seed %d: 引いた回数が 7 回でない", seed)
		}
		again, err := PlanCancellations(defs, 6, cancellationDuration, newCancellationRNG(seed))
		if err != nil || !reflect.DeepEqual(again, got) {
			t.Fatalf("seed %d: 同じシードで結果が違う: %+v, %v", seed, again, err)
		}
	}
}

// GC-2 確率 0 → 0 件、1000 → 7 件。どちらも 7 回引く。
func TestPlanCancellationsZeroAndAll(t *testing.T) {
	t.Parallel()
	def := CancellationDef{Island: 2, SlotMs: 30000, DurationMs: 10000}
	r0 := newCancellationRNG(5)
	def.ProbabilityPermille = 0
	got0, err := PlanCancellations([]CancellationDef{def}, 6, cancellationDuration, r0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got0) != 0 {
		t.Errorf("確率 0 で %d 件", len(got0))
	}
	r1 := newCancellationRNG(5)
	def.ProbabilityPermille = 1000
	got1, err := PlanCancellations([]CancellationDef{def}, 6, cancellationDuration, r1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got1) != 7 {
		t.Fatalf("確率 1000 で %d 件、7 件のはず", len(got1))
	}
	for i, c := range got1 {
		start := int64(i+1) * 30000
		if c != (Cancellation{Island: 2, Start: start, End: start + 10000}) {
			t.Errorf("got1[%d] = %+v", i, c)
		}
	}
	ref := newCancellationRNG(5)
	skipDraws(ref, 7)
	want := ref.Uint64()
	if a, b := r0.Uint64(), r1.Uint64(); a != want || b != want {
		t.Errorf("次の値が違う: 0 件 %d・1000 件 %d・7 回引いたあと %d", a, b, want)
	}
}

// GC-3 2 つの定義 → Start の昇順・同じ Start は定義の順。引く順は「定義の順 → 区間の順」。
func TestPlanCancellationsOrder(t *testing.T) {
	t.Parallel()
	defs := []CancellationDef{
		{Island: 5, ProbabilityPermille: 600, SlotMs: 60000, DurationMs: 10000},
		{Island: 3, ProbabilityPermille: 600, SlotMs: 30000, DurationMs: 20000},
	}
	for seed := range uint64(30) {
		got, err := PlanCancellations(defs, 6, cancellationDuration, newCancellationRNG(seed))
		if err != nil {
			t.Fatal(err)
		}
		hand := newCancellationRNG(seed)
		var a, b []Cancellation
		for k := int64(1); k <= 3; k++ { // 60000・120000・180000
			if hand.IntN(1000) < 600 {
				a = append(a, Cancellation{Island: 5, Start: k * 60000, End: k*60000 + 10000})
			}
		}
		for k := int64(1); k <= 7; k++ { // 30000〜210000
			if hand.IntN(1000) < 600 {
				b = append(b, Cancellation{Island: 3, Start: k * 30000, End: k*30000 + 20000})
			}
		}
		// 手で併合する（同じ Start は定義 0 が先）。
		var want []Cancellation
		i, j := 0, 0
		for i < len(a) || j < len(b) {
			if j >= len(b) || (i < len(a) && a[i].Start <= b[j].Start) {
				want = append(want, a[i])
				i++
			} else {
				want = append(want, b[j])
				j++
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: got %+v, want %+v", seed, got, want)
		}
	}
}

// GC-4 検査 → ErrInvalidCancellation。乱数を引く前に返す。
func TestPlanCancellationsInvalid(t *testing.T) {
	t.Parallel()
	valid := CancellationDef{Island: 1, ProbabilityPermille: 170, SlotMs: 30000, DurationMs: 10000}
	cases := []struct {
		name       string
		mod        func(*CancellationDef)
		durationMs int64
	}{
		{"島 0", func(d *CancellationDef) { d.Island = 0 }, cancellationDuration},
		{"島が範囲の外", func(d *CancellationDef) { d.Island = 7 }, cancellationDuration},
		{"確率 1001", func(d *CancellationDef) { d.ProbabilityPermille = 1001 }, cancellationDuration},
		{"確率が負", func(d *CancellationDef) { d.ProbabilityPermille = -1 }, cancellationDuration},
		{"slotMs 0", func(d *CancellationDef) { d.SlotMs = 0 }, cancellationDuration},
		{"durationMs 0", func(d *CancellationDef) { d.DurationMs = 0 }, cancellationDuration},
		{"区間が 10001 個", func(d *CancellationDef) { d.SlotMs = 1 }, 10002},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bad := valid
			tc.mod(&bad)
			// 誤りは 2 つ目の定義に置き、1 つ目で引いていないことも確かめる。
			rng := newCancellationRNG(3)
			got, err := PlanCancellations([]CancellationDef{valid, bad}, 6, tc.durationMs, rng)
			if !errors.Is(err, ErrInvalidCancellation) {
				t.Fatalf("err = %v, want ErrInvalidCancellation", err)
			}
			if got != nil {
				t.Errorf("got = %+v, want nil", got)
			}
			if !sameNext(rng, newCancellationRNG(3)) {
				t.Error("誤りなのに乱数を引いた")
			}
		})
	}
	// 区間がちょうど 10000 個なら通る。
	d := valid
	d.SlotMs = 1
	if _, err := PlanCancellations([]CancellationDef{d}, 6, 10001, newCancellationRNG(3)); err != nil {
		t.Errorf("区間 10000 個: %v", err)
	}
}

// GC-5 defs が空 → nil で、rng を使わない。slotMs >= durationMs なら引かない。
func TestPlanCancellationsEmpty(t *testing.T) {
	t.Parallel()
	rng := newCancellationRNG(9)
	got, err := PlanCancellations(nil, 6, cancellationDuration, rng)
	if err != nil || got != nil {
		t.Fatalf("got %+v, %v; want nil, nil", got, err)
	}
	if !sameNext(rng, newCancellationRNG(9)) {
		t.Error("空の定義で乱数を引いた")
	}
	rng = newCancellationRNG(9)
	got, err = PlanCancellations([]CancellationDef{{Island: 1, ProbabilityPermille: 1000, SlotMs: 240000, DurationMs: 10000}}, 6, cancellationDuration, rng)
	if err != nil || got != nil {
		t.Fatalf("slotMs >= durationMs: got %+v, %v; want nil, nil", got, err)
	}
	if !sameNext(rng, newCancellationRNG(9)) {
		t.Error("区間の境目が無いのに乱数を引いた")
	}
}
