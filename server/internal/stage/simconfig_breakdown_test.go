package stage

import (
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/game"
)

const betaBreakdownYAML = `  - kind: breakdown
    window: [80000, 120000]
    ferry: any
    durationMs: 30000
`

// ST-B1 beta.yaml の breakdown（window・any）を写す。
// beta は raft.preVote: false で SimConfig が誤りになるので、breakdownDefs を直接呼ぶ。
func TestBreakdownDefsBeta(t *testing.T) {
	t.Parallel()
	st := parseStage(t, "beta.yaml", readTestdata(t, "stages/beta.yaml"))
	got := breakdownDefs(st)
	want := []game.BreakdownDef{{WindowStart: 80000, WindowEnd: 120000, DurationMs: 30000, Ferry: ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("breakdownDefs = %+v, want %+v", got, want)
	}
}

// ST-B1 ferry に船の ID を書けば、その ID。at は WindowStart と WindowEnd が同じ。書いた順を保つ。
func TestBreakdownDefsFerryID(t *testing.T) {
	t.Parallel()
	data := applyEdits(t, readTestdata(t, "stages/beta.yaml"), edit{betaBreakdownYAML, `  - kind: breakdown
    window: [80000, 120000]
    ferry: f2
    durationMs: 30000
  - kind: breakdown
    at: 50000
    ferry: f1
    durationMs: 1000
`})
	st := parseStage(t, "beta.yaml", data)
	got := breakdownDefs(st)
	want := []game.BreakdownDef{
		{WindowStart: 80000, WindowEnd: 120000, DurationMs: 30000, Ferry: "f2"},
		{WindowStart: 50000, WindowEnd: 50000, DurationMs: 1000, Ferry: "f1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("breakdownDefs = %+v, want %+v", got, want)
	}
}

// 故障がなければ nil。あれば SimConfig の Breakdowns に写る。
func TestSimConfigBreakdowns(t *testing.T) {
	t.Parallel()
	st := parseStage(t, "alpha.yaml", readTestdata(t, "stages/alpha.yaml"))
	cfg, err := SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	if cfg.Breakdowns != nil {
		t.Errorf("Breakdowns = %+v, want nil", cfg.Breakdowns)
	}
	st = parseStage(t, "alpha.yaml", applyEdits(t, readTestdata(t, "stages/alpha.yaml"), edit{"events: []\n", `events:
  - { kind: breakdown, at: 30000, durationMs: 20000, ferry: any }
`}))
	cfg, err = SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	want := []game.BreakdownDef{{WindowStart: 30000, WindowEnd: 30000, DurationMs: 20000}}
	if !reflect.DeepEqual(cfg.Breakdowns, want) {
		t.Errorf("Breakdowns = %+v, want %+v", cfg.Breakdowns, want)
	}
}
