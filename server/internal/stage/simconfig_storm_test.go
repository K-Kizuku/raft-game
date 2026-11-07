package stage

import (
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// stormEventsYAML は SS-1 の events（alpha.yaml の events: [] を置き換える）。
const stormEventsYAML = `events:
  - { kind: storm, at: 60000, durationMs: 20000, routes: [[hama-tsukikage, hama-hoshizuna]], forecastMs: 10000 }
  - { kind: storm, window: [90000, 100000], durationMs: 5000, routes: [[hama-tsukikage, hama-todai], [hama-hoshizuna, hama-tsukikage]], forecastMs: 0 }
`

// stormStage は alpha.yaml の events: [] を events に置き換えたステージ。
func stormStage(t *testing.T, events string) *Stage {
	t.Helper()
	return parseStage(t, "alpha.yaml", applyEdits(t, readTestdata(t, "stages/alpha.yaml"), edit{"events: []\n", events}))
}

func stormSimConfig(t *testing.T) (*Stage, sim.Config) {
	t.Helper()
	st := stormStage(t, stormEventsYAML)
	cfg, err := SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	return st, cfg
}

// SS-1 変換。
func TestSimConfigStorms(t *testing.T) {
	t.Parallel()
	_, cfg := stormSimConfig(t)
	want := []game.StormDef{
		{WindowStart: 60000, WindowEnd: 60000, DurationMs: 20000, Routes: [][2]uint64{{2, 1}}, ForecastMs: 10000},
		{WindowStart: 90000, WindowEnd: 100000, DurationMs: 5000, Routes: [][2]uint64{{2, 3}, {1, 2}}, ForecastMs: 0},
	}
	if !reflect.DeepEqual(cfg.Storms, want) {
		t.Errorf("Storms = %+v, want %+v", cfg.Storms, want)
	}
}

// SS-3 複製。
func TestSimConfigStormsCopied(t *testing.T) {
	t.Parallel()
	st, cfg := stormSimConfig(t)
	cfg.Storms[0].Routes[0] = [2]uint64{3, 2}
	if got := st.Events[0].Routes[0]; got != [2]uint64{2, 1} {
		t.Errorf("st.Events[0].Routes[0] = %v, want [2 1]", got)
	}
}

// SS-4 嵐なし。
func TestSimConfigNoStorms(t *testing.T) {
	t.Parallel()
	st := parseStage(t, "alpha.yaml", readTestdata(t, "stages/alpha.yaml"))
	cfg, err := SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	if cfg.Storms != nil {
		t.Errorf("Storms = %+v, want nil", cfg.Storms)
	}
}

// stormStep は嵐のイベントの時刻と中身。
type stormStep struct {
	T    sim.SimTime
	Data sim.EventData
}

// SS-5 変換した設定で Engine が動き、嵐が起きる。
func TestSimConfigStormsEngine(t *testing.T) {
	t.Parallel()
	_, cfg := stormSimConfig(t)
	const seed = 20261101
	e, err := sim.New(cfg, seed, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("sim.New: %v", err)
	}
	if ack := e.Apply(sim.Command{Type: sim.CmdGameStart}); !ack.OK {
		t.Fatalf("Apply(game.start) = %+v", ack)
	}
	e.RunUntil(180000)
	if got := e.Phase(); got != sim.PhaseEnded {
		t.Errorf("Phase() = %q, want %q", got, sim.PhaseEnded)
	}

	plan, err := game.PlanStorms(cfg.Storms, cfg.Islands, int64(cfg.DurationMs), sim.Sub(seed, sim.StreamEvents))
	if err != nil {
		t.Fatalf("PlanStorms: %v", err)
	}
	s := sim.SimTime(plan[1].Start)
	if s < 90000 || s > 100000 {
		t.Fatalf("2 つ目の嵐の開始 %d が 90000..100000 にない", s)
	}

	var got []stormStep
	for _, ev := range e.DrainEvents() {
		switch ev.Data.(type) {
		case sim.Forecast, sim.RouteBlocked, sim.RouteUnblocked:
			got = append(got, stormStep{T: ev.T, Data: ev.Data})
		}
	}
	want := []stormStep{
		{50000, sim.Forecast{Event: "storm", At: 60000, RouteIDs: []string{"1-2"}}},
		{60000, sim.RouteBlocked{RouteIDs: []string{"1-2"}, Until: 80000}},
		{80000, sim.RouteUnblocked{RouteIDs: []string{"1-2"}, Until: 80000}},
		{s, sim.RouteBlocked{RouteIDs: []string{"2-3", "1-2"}, Until: s + 5000}},
		{s + 5000, sim.RouteUnblocked{RouteIDs: []string{"2-3", "1-2"}, Until: s + 5000}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("嵐のイベント:\n got  %+v\n want %+v", got, want)
	}
}
