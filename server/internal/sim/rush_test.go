package sim

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// rushTestConfig はラッシュのテストの設定。台本の依頼 1 件（10000）とランダムな依頼（重みと種類）を持つ。
func rushTestConfig(defs ...game.RushDef) Config {
	cfg := testConfig()
	cfg.Raft.ElectionTick = 200
	cfg.Requests = reqConfig(stallReq(10000, 2))
	cfg.Requests.Random = game.RandomConfig{
		RateMilliPerMin: 600,
		SlotMs:          10000,
		Weights:         []int64{400, 300, 300},
		Kinds: []game.RandomKind{
			{Kind: ledger.KindStallReserve, Resource: "stall:main", Weight: 3},
			{Kind: ledger.KindFireworkReserve, Resource: "firework:a", Weight: 1},
		},
	}
	cfg.Rushes = defs
	return cfg
}

func rushAt(at int64, requests int, forecastMs int64) game.RushDef {
	return game.RushDef{WindowStart: at, WindowEnd: at, Requests: requests, ForecastMs: forecastMs}
}

// SR-1 ラッシュ（at 60000・requests: 3・forecastMs 5000）→ 55000 に forecast{rush}、60000 に request.created が 3 件。
// ID は台本・ランダムの依頼と通しの番号。
func TestRushForecastAndRequests(t *testing.T) {
	t.Parallel()
	cfg := rushTestConfig(rushAt(60000, 3, 5000))
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	e.RunUntil(cfg.DurationMs)
	evs := e.DrainEvents()

	fcs := ofTypeAt[Forecast](evs)
	if len(fcs) != 1 {
		t.Fatalf("forecast = %+v, want 1", fcs)
	}
	fc := fcs[0]
	if fc.T != 55000 || fc.D.Event != "rush" || fc.D.At != 60000 || fc.D.RouteIDs == nil || len(fc.D.RouteIDs) != 0 {
		t.Errorf("forecast = %+v", fc)
	}
	if len(fc.D.IslandIDs) != 3 || !slices.IsSorted(fc.D.IslandIDs) {
		t.Errorf("forecast islandIds = %v, want 3 islands in order", fc.D.IslandIDs)
	}
	b, err := json.Marshal(fc.D)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"routeIds":[]`) || !strings.Contains(string(b), `"islandIds":[`) {
		t.Errorf("forecast JSON = %s", b)
	}

	created := ofTypeAt[RequestCreated](evs)
	var at60 []uint64
	for i, c := range created {
		if want := game.ReqID("t", i+1); c.D.Request.ID != want {
			t.Errorf("request %d: id %s, want %s（通しの番号）", i, c.D.Request.ID, want)
		}
		if i > 0 && c.T < created[i-1].T {
			t.Errorf("request %d: t %d < %d", i, c.T, created[i-1].T)
		}
		if c.T == 60000 {
			at60 = append(at60, c.D.Request.Origin)
		}
	}
	slices.Sort(at60)
	if !slices.Equal(at60, fc.D.IslandIDs) {
		t.Errorf("requests at 60000 from %v, want %v", at60, fc.D.IslandIDs)
	}
	if len(created) <= 4 {
		t.Errorf("requests = %d, want script + rush + random", len(created))
	}
}

// 予報なし（forecastMs: 0）は forecast を出さない。依頼は台本の依頼として発生する。時刻が durationMs ちょうどでも発生する。
func TestRushNoForecastAtDuration(t *testing.T) {
	t.Parallel()
	cfg := rushTestConfig(rushAt(int64(testConfig().DurationMs), 2, 0))
	cfg.Requests.Random = game.RandomConfig{Weights: []int64{1, 1, 0}, Kinds: cfg.Requests.Random.Kinds}
	e := startedEngineWith(t, cfg, Options{CheckInvariants: true})
	e.RunUntil(cfg.DurationMs)
	evs := e.DrainEvents()
	if got := ofType[Forecast](evs); len(got) != 0 {
		t.Errorf("forecast = %+v, want none", got)
	}
	var origins []uint64
	for _, c := range ofTypeAt[RequestCreated](evs) {
		if c.T == cfg.DurationMs {
			origins = append(origins, c.D.Request.Origin)
		}
	}
	slices.Sort(origins)
	if !slices.Equal(origins, []uint64{1, 2}) {
		t.Errorf("requests at durationMs from %v, want [1 2]", origins)
	}
}

// SR-2 ラッシュの定義が無い設定 → PlanRequests に渡す設定が今と同じで、トレースのハッシュが変わらない。
func TestRushNoneUnchanged(t *testing.T) {
	t.Parallel()
	cfg := rushTestConfig()
	e := newEngine(t, cfg, Options{})
	want, err := game.PlanRequests(cfg.Requests, cfg.Islands, int64(cfg.DurationMs), cfg.Resources, Sub(testSeed, StreamRequests))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]game.Request, len(e.reqs.list))
	for i, r := range e.reqs.list {
		got[i] = r.req
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %+v, want %+v", got, want)
	}
	rc, err := e.planRushes(testSeed)
	if err != nil || !reflect.DeepEqual(rc, e.cfg.Requests) {
		t.Errorf("planRushes = %+v, %v, want the config as is", rc, err)
	}

	run := func(c Config) [32]byte {
		e := startedEngineWith(t, c, Options{CheckInvariants: true})
		e.RunUntil(c.DurationMs)
		return e.TraceHash()
	}
	empty := rushTestConfig()
	empty.Rushes = []game.RushDef{}
	empty.Drifts = []DriftDef{}
	if run(cfg) != run(empty) {
		t.Error("empty rushes/drifts changed the trace")
	}
}

// ラッシュの依頼は Script の後ろに足した複製で、Config の Script・Rushes は書き換えない。
func TestRushConfigCopied(t *testing.T) {
	t.Parallel()
	cfg := rushTestConfig(rushAt(60000, 3, 5000))
	script := slices.Clone(cfg.Requests.Script)
	rushes := slices.Clone(cfg.Rushes)
	e := newEngine(t, cfg, Options{})
	if !reflect.DeepEqual(cfg.Requests.Script, script) || !reflect.DeepEqual(e.cfg.Requests.Script, script) {
		t.Errorf("script changed: %+v / %+v", cfg.Requests.Script, e.cfg.Requests.Script)
	}
	cfg.Rushes[0].Requests = 99
	if !reflect.DeepEqual(e.cfg.Rushes, rushes) {
		t.Errorf("Engine shares Rushes with the caller: %+v", e.cfg.Rushes)
	}
}

// ラッシュの定義の誤り（種類がない・重みが 0）は New が失敗する。
func TestRushConfigErrors(t *testing.T) {
	t.Parallel()
	noKinds := rushTestConfig(rushAt(60000, 3, 5000))
	noKinds.Requests.Random = game.RandomConfig{Weights: []int64{1, 1, 1}}
	zero := rushTestConfig(rushAt(60000, 3, 5000))
	zero.Requests.Random.RateMilliPerMin = 0
	zero.Requests.Random.Weights = []int64{0, 0, 0}
	late := rushTestConfig(rushAt(int64(testConfig().DurationMs)+1, 1, 0))
	for name, c := range map[string]Config{"no kinds": noKinds, "zero weights": zero, "after duration": late} {
		if _, err := New(c, testSeed, Options{}); !errors.Is(err, game.ErrInvalidRush) {
			t.Errorf("%s: New err = %v, want ErrInvalidRush", name, err)
		}
	}
}

// SN-1（ラッシュ）NextNotableAt がラッシュの予報を返す。
func TestRushNextNotable(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.Requests = reqConfig()
	cfg.Requests.Random = game.RandomConfig{Weights: []int64{1, 1, 1}, Kinds: []game.RandomKind{{Kind: ledger.KindStallReserve, Resource: "stall:main", Weight: 1}}}
	cfg.Rushes = []game.RushDef{rushAt(60000, 2, 5000)}
	e := startedEngineWith(t, cfg, Options{})
	if got, ok := e.NextNotableAt(); !ok || got != 55000 {
		t.Errorf("NextNotableAt = %d, %v, want 55000", got, ok)
	}
	e.RunUntil(55000)
	if got, ok := e.NextNotableAt(); !ok || got != 60000 {
		t.Errorf("after forecast: NextNotableAt = %d, %v, want 60000（依頼の発生）", got, ok)
	}
}
