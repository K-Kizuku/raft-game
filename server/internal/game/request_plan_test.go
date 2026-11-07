package game

import (
	"cmp"
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

const testDuration = 120000

func testResources() []ledger.ResourceDef {
	return []ledger.ResourceDef{
		{ID: "firework:a", Kind: ledger.ResourceFirework},
		{ID: "stall:main", Kind: ledger.ResourceStall, Capacity: 2},
	}
}

// baseConfig は台本の依頼が 1 件の設定（値はテストの中の値）。
func baseConfig() RequestConfig {
	return RequestConfig{
		StageID:            "t",
		DeadlineNormalMs:   60000,
		DeadlineCriticalMs: 90000,
		RetryMs:            25000,
		Script:             []ScriptRequest{{At: 0, Island: 1, Kind: ledger.KindStallReserve, Resource: "stall:main"}},
		Critical:           []string{"firework:a"},
	}
}

// randomConfig は台本がなく、ランダムな依頼だけの設定。
func randomConfig() RequestConfig {
	c := baseConfig()
	c.Script = nil
	c.Random = RandomConfig{
		RateMilliPerMin: 3000,
		SlotMs:          10000,
		Weights:         []int64{500, 300, 200},
		Kinds: []RandomKind{
			{Kind: ledger.KindStallReserve, Resource: "stall:main", Weight: 1},
			{Kind: ledger.KindFireworkReserve, Resource: "firework:a", Weight: 3},
		},
	}
	return c
}

func newRand(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, 2)) }

func mustPlan(t *testing.T, c RequestConfig, islands int, durationMs int64, rng *rand.Rand) []Request {
	t.Helper()
	got, err := PlanRequests(c, islands, durationMs, testResources(), rng)
	if err != nil {
		t.Fatalf("PlanRequests: %v", err)
	}
	return got
}

func TestPlanRequestsValidation(t *testing.T) { // P-1
	t.Parallel()
	script := func(mod func(*ScriptRequest)) func(*RequestConfig) {
		return func(c *RequestConfig) { mod(&c.Script[0]) }
	}
	random := func(mod func(*RandomConfig)) func(*RequestConfig) {
		return func(c *RequestConfig) {
			*c = randomConfig()
			mod(&c.Random)
		}
	}
	bad := []struct {
		name string
		mod  func(*RequestConfig)
	}{
		{"rate -1", func(c *RequestConfig) { c.Random.RateMilliPerMin = -1 }},
		{"rate -1 without script", func(c *RequestConfig) { c.Script = nil; c.Random.RateMilliPerMin = -1 }},
		{"stageId empty", func(c *RequestConfig) { c.StageID = "" }},
		{"deadline normal 0", func(c *RequestConfig) { c.DeadlineNormalMs = 0 }},
		{"deadline critical 0", func(c *RequestConfig) { c.DeadlineCriticalMs = 0 }},
		{"retry 0", func(c *RequestConfig) { c.RetryMs = 0 }},
		{"critical unknown", func(c *RequestConfig) { c.Critical = []string{"firework:x"} }},
		{"critical duplicated", func(c *RequestConfig) { c.Critical = []string{"firework:a", "firework:a"} }},
		{"script at -1", script(func(s *ScriptRequest) { s.At = -1 })},
		{"script at duration+1", script(func(s *ScriptRequest) { s.At = testDuration + 1 })},
		{"script island 0", script(func(s *ScriptRequest) { s.Island = 0 })},
		{"script island 4", script(func(s *ScriptRequest) { s.Island = 4 })},
		{"script kind budget", script(func(s *ScriptRequest) { s.Kind = "budget.spend" })},
		{"script unknown resource", script(func(s *ScriptRequest) { s.Resource = "stall:x" })},
		{"script kind mismatch", script(func(s *ScriptRequest) { s.Kind = ledger.KindFireworkReserve })},
		{"random slot 0", random(func(r *RandomConfig) { r.SlotMs = 0 })},
		{"random rate × slot too large", random(func(r *RandomConfig) { r.RateMilliPerMin, r.SlotMs = 6001, 10000 })},
		{"random weights length 2", random(func(r *RandomConfig) { r.Weights = []int64{500, 500} })},
		{"random weights sum 999", random(func(r *RandomConfig) { r.Weights = []int64{500, 300, 199} })},
		{"random weight -1", random(func(r *RandomConfig) { r.Weights = []int64{-1, 501, 500} })},
		{"random kinds empty", random(func(r *RandomConfig) { r.Kinds = nil })},
		{"random kind weight 0", random(func(r *RandomConfig) { r.Kinds[1].Weight = 0 })},
		{"random kind unknown", random(func(r *RandomConfig) { r.Kinds[0].Kind = "budget.spend" })},
		{"random kind mismatch", random(func(r *RandomConfig) { r.Kinds[0].Resource = "firework:a" })},
	}
	for _, tt := range bad {
		c := baseConfig()
		tt.mod(&c)
		got, err := PlanRequests(c, 3, testDuration, testResources(), newRand(1))
		if !errors.Is(err, ErrInvalidConfig) || got != nil {
			t.Errorf("%s: PlanRequests = %v, %v; want ErrInvalidConfig", tt.name, got, err)
		} else {
			t.Logf("%s: %v", tt.name, err)
		}
	}

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()
		got, err := PlanRequests(RequestConfig{}, 3, testDuration, testResources(), newRand(1))
		if got != nil || err != nil {
			t.Errorf("PlanRequests(zero) = %v, %v; want nil, nil", got, err)
		}
	})
	good := []struct {
		name string
		mod  func(*RequestConfig)
	}{
		{"rate 0 with zero random", func(c *RequestConfig) { c.Random = RandomConfig{} }},
		{"rate 0 ignores other random fields", func(c *RequestConfig) {
			c.Random = RandomConfig{SlotMs: -5, Weights: []int64{1}, Kinds: []RandomKind{{Kind: "x"}}}
		}},
		{"rate × slot == 60,000,000", random(func(r *RandomConfig) { r.RateMilliPerMin, r.SlotMs = 6000, 10000 })},
		{"script at duration", script(func(s *ScriptRequest) { s.At = testDuration })},
	}
	for _, tt := range good {
		c := baseConfig()
		tt.mod(&c)
		if _, err := PlanRequests(c, 3, testDuration, testResources(), newRand(1)); err != nil {
			t.Errorf("%s: PlanRequests = %v, want no error", tt.name, err)
		}
	}
}

func TestPlanRequestsScriptOnly(t *testing.T) { // P-2
	t.Parallel()
	c := baseConfig()
	c.Script = []ScriptRequest{
		{At: 5000, Island: 3, Kind: ledger.KindFireworkReserve, Resource: "firework:a"},
		{At: 1000, Island: 2, Kind: ledger.KindStallReserve, Resource: "stall:main"},
		{At: 1000, Island: 1, Kind: ledger.KindStallReserve, Resource: "stall:main"},
	}
	rng := newRand(7)
	got := mustPlan(t, c, 3, testDuration, rng)
	want := []Request{
		{ID: "t-0001", Origin: 2, Kind: ledger.KindStallReserve, Resource: "stall:main", CreatedAt: 1000, Deadline: 61000},
		{ID: "t-0002", Origin: 1, Kind: ledger.KindStallReserve, Resource: "stall:main", CreatedAt: 1000, Deadline: 61000},
		{ID: "t-0003", Origin: 3, Kind: ledger.KindFireworkReserve, Resource: "firework:a", Critical: true, CreatedAt: 5000, Deadline: 95000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PlanRequests =\n %+v\nwant\n %+v", got, want)
	}
	if a, b := rng.Uint64(), newRand(7).Uint64(); a != b {
		t.Errorf("rng was used: next %d, fresh %d", a, b)
	}
}

func TestSpawnPPM(t *testing.T) { // P-3
	t.Parallel()
	tests := []struct{ w, rate, slot, want int64 }{
		{1000, 500, 10000, 83333},
		{1000, 6000, 10000, 1000000},
		{0, 6000, 10000, 0},
		{511, 500, 10000, 42583},
	}
	for _, tt := range tests {
		if got := spawnPPM(tt.w, tt.rate, tt.slot); got != tt.want {
			t.Errorf("spawnPPM(%d, %d, %d) = %d, want %d", tt.w, tt.rate, tt.slot, got, tt.want)
		}
	}
}

func TestPlanRequestsAlways(t *testing.T) { // P-4
	t.Parallel()
	c := randomConfig()
	c.Random.Weights = []int64{1000}
	c.Random.RateMilliPerMin = 6000
	got := mustPlan(t, c, 1, 60000, newRand(3))
	if len(got) != 6 {
		t.Fatalf("len = %d, want 6: %+v", len(got), got)
	}
	for k, r := range got {
		if r.CreatedAt < int64(k)*10000 || r.CreatedAt >= int64(k+1)*10000 {
			t.Errorf("request %d: createdAt %d is not in [%d, %d)", k, r.CreatedAt, k*10000, (k+1)*10000)
		}
		if r.ID != ReqID("t", k+1) || r.Origin != 1 {
			t.Errorf("request %d = %+v", k, r)
		}
	}
}

// expectRandom は「引く順序」のとおりに rng を引いて、ランダムな依頼の列（並べ替えの前）と捨てた数を返す。
func expectRandom(c RequestConfig, islands int, durationMs int64, rng *rand.Rand) ([]Request, int) {
	r := c.Random
	var total int64
	for _, k := range r.Kinds {
		total += k.Weight
	}
	var out []Request
	discarded := 0
	for k := int64(0); k*r.SlotMs < durationMs; k++ {
		for island := 1; island <= islands; island++ {
			p := r.Weights[island-1] * r.RateMilliPerMin * r.SlotMs / 60000
			if rng.Int64N(1_000_000) >= p {
				continue
			}
			at := k*r.SlotMs + rng.Int64N(r.SlotMs)
			x := rng.Int64N(total)
			var acc int64
			var kind RandomKind
			for _, kd := range r.Kinds {
				acc += kd.Weight
				if acc > x {
					kind = kd
					break
				}
			}
			if at > durationMs {
				discarded++
				continue
			}
			critical := kind.Resource == "firework:a"
			deadline := at + c.DeadlineNormalMs
			if critical {
				deadline = at + c.DeadlineCriticalMs
			}
			out = append(out, Request{Origin: uint64(island), Kind: kind.Kind, Resource: kind.Resource, Critical: critical, CreatedAt: at, Deadline: deadline})
		}
	}
	return out, discarded
}

func TestPlanRequestsDrawOrder(t *testing.T) { // P-5
	t.Parallel()
	const duration = 95000
	c := randomConfig()
	discarded := 0
	for seed := uint64(1); seed <= 20; seed++ {
		want, d := expectRandom(c, 3, duration, newRand(seed))
		discarded += d
		// 区間の中では島の順に引くので、時刻の順とは限らない。At で安定に並べ替える（同じ時刻は引いた順）。
		slices.SortStableFunc(want, func(a, b Request) int { return cmp.Compare(a.CreatedAt, b.CreatedAt) })
		for i := range want {
			want[i].ID = ReqID("t", i+1)
		}
		got := mustPlan(t, c, 3, duration, newRand(seed))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("seed %d: PlanRequests =\n %+v\nwant\n %+v", seed, got, want)
		}
		for i := 1; i < len(got); i++ {
			if got[i].CreatedAt < got[i-1].CreatedAt {
				t.Errorf("seed %d: createdAt decreases at %d", seed, i)
			}
		}
	}
	if discarded == 0 {
		t.Error("no request was discarded (at > durationMs); choose other seeds")
	}
}

func TestPlanRequestsZeroWeight(t *testing.T) { // P-6
	t.Parallel()
	c := randomConfig()
	c.Random.RateMilliPerMin = 6000
	c.Random.Weights = []int64{1000, 0, 0}
	got := mustPlan(t, c, 3, 600000, newRand(5))
	if len(got) != 60 {
		t.Errorf("len = %d, want 60", len(got))
	}
	for _, r := range got {
		if r.Origin != 1 {
			t.Errorf("request from island %d: %+v", r.Origin, r)
		}
	}
}

func TestPlanRequestsScriptFirst(t *testing.T) { // P-7
	t.Parallel()
	const duration = 95000
	c := randomConfig()
	old := mustPlan(t, c, 3, duration, newRand(1))
	if len(old) == 0 {
		t.Fatal("no random request")
	}
	j := len(old) / 2
	for j > 0 && old[j-1].CreatedAt == old[j].CreatedAt {
		j--
	}
	r := old[j]
	island := uint64(1)
	if r.Origin == 1 {
		island = 2
	}
	c.Script = []ScriptRequest{{At: r.CreatedAt, Island: island, Kind: ledger.KindStallReserve, Resource: "stall:main"}}
	got := mustPlan(t, c, 3, duration, newRand(1))
	if len(got) != len(old)+1 {
		t.Fatalf("len = %d, want %d", len(got), len(old)+1)
	}
	wantScript := Request{ID: ReqID("t", j+1), Origin: island, Kind: ledger.KindStallReserve, Resource: "stall:main", CreatedAt: r.CreatedAt, Deadline: r.CreatedAt + 60000}
	if got[j] != wantScript {
		t.Errorf("got[%d] = %+v, want the script request %+v", j, got[j], wantScript)
	}
	for i, o := range old {
		k := i
		if i >= j {
			k = i + 1
		}
		o.ID = ReqID("t", k+1)
		if got[k] != o {
			t.Errorf("got[%d] = %+v, want %+v", k, got[k], o)
		}
	}
}

func TestPlanRequestsDeterministic(t *testing.T) { // P-8
	t.Parallel()
	c := randomConfig()
	a := mustPlan(t, c, 3, testDuration, newRand(1))
	b := mustPlan(t, c, 3, testDuration, newRand(1))
	if !reflect.DeepEqual(a, b) {
		t.Errorf("same seed gave different plans:\n %+v\n %+v", a, b)
	}
	if d := mustPlan(t, c, 3, testDuration, newRand(2)); reflect.DeepEqual(a, d) {
		t.Errorf("seeds 1 and 2 gave the same plan: %+v", a)
	}
}

func TestPickKind(t *testing.T) { // P-9
	t.Parallel()
	kinds := []RandomKind{{Weight: 1}, {Weight: 3}}
	for _, tt := range []struct {
		x    int64
		want int
	}{{0, 0}, {1, 1}, {2, 1}, {3, 1}} {
		if got := pickKind(kinds, tt.x); got != tt.want {
			t.Errorf("pickKind(%d) = %d, want %d", tt.x, got, tt.want)
		}
	}
}

func TestRequestConfigClone(t *testing.T) { // P-10
	t.Parallel()
	orig := randomConfig()
	orig.Script = baseConfig().Script
	want := randomConfig()
	want.Script = baseConfig().Script

	c := orig.Clone()
	if !reflect.DeepEqual(c, orig) {
		t.Fatalf("Clone = %+v, want %+v", c, orig)
	}
	c.Script[0].Island = 9
	c.Random.Weights[0] = 9
	c.Random.Kinds[0].Weight = 9
	c.Critical[0] = "changed"
	if !reflect.DeepEqual(orig, want) {
		t.Errorf("original changed: %+v", orig)
	}
}
