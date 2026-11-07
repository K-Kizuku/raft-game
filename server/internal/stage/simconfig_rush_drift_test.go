package stage

import (
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

const betaRushDriftYAML = `  - kind: rush
    window: [100000, 110000]
    requests: 4
    forecastMs: 5000
  - kind: drift
    at: 130000
    letters: 3
    minAgeMs: 30000
`

// ST-R1 beta.yaml の rush（window）・drift（at）を写す。
// beta は raft.preVote: false で SimConfig が誤りになるので、rushDefs・driftDefs を直接呼ぶ。
func TestRushDriftDefsBeta(t *testing.T) {
	t.Parallel()
	st := parseStage(t, "beta.yaml", readTestdata(t, "stages/beta.yaml"))
	wantRush := []game.RushDef{{WindowStart: 100000, WindowEnd: 110000, Requests: 4, ForecastMs: 5000}}
	if got := rushDefs(st); !reflect.DeepEqual(got, wantRush) {
		t.Errorf("rushDefs = %+v, want %+v", got, wantRush)
	}
	wantDrift := []sim.DriftDef{{WindowStart: 130000, WindowEnd: 130000, Letters: 3, MinAgeMs: 30000}}
	if got := driftDefs(st); !reflect.DeepEqual(got, wantDrift) {
		t.Errorf("driftDefs = %+v, want %+v", got, wantDrift)
	}
}

// 書いた順を保つ。rush・drift がなければ nil。
func TestRushDriftDefsOrder(t *testing.T) {
	t.Parallel()
	data := applyEdits(t, readTestdata(t, "stages/beta.yaml"), edit{betaRushDriftYAML, `  - kind: drift
    window: [120000, 140000]
    letters: 1
    minAgeMs: 0
  - kind: rush
    at: 50000
    requests: 2
    forecastMs: 0
  - kind: rush
    window: [100000, 110000]
    requests: 4
    forecastMs: 5000
  - kind: drift
    at: 130000
    letters: 3
    minAgeMs: 30000
`})
	st := parseStage(t, "beta.yaml", data)
	wantRush := []game.RushDef{
		{WindowStart: 50000, WindowEnd: 50000, Requests: 2, ForecastMs: 0},
		{WindowStart: 100000, WindowEnd: 110000, Requests: 4, ForecastMs: 5000},
	}
	if got := rushDefs(st); !reflect.DeepEqual(got, wantRush) {
		t.Errorf("rushDefs = %+v, want %+v", got, wantRush)
	}
	wantDrift := []sim.DriftDef{
		{WindowStart: 120000, WindowEnd: 140000, Letters: 1, MinAgeMs: 0},
		{WindowStart: 130000, WindowEnd: 130000, Letters: 3, MinAgeMs: 30000},
	}
	if got := driftDefs(st); !reflect.DeepEqual(got, wantDrift) {
		t.Errorf("driftDefs = %+v, want %+v", got, wantDrift)
	}

	none := applyEdits(t, readTestdata(t, "stages/beta.yaml"), edit{betaRushDriftYAML, ""})
	st = parseStage(t, "beta.yaml", none)
	if got := rushDefs(st); got != nil {
		t.Errorf("rushDefs without rush = %+v, want nil", got)
	}
	if got := driftDefs(st); got != nil {
		t.Errorf("driftDefs without drift = %+v, want nil", got)
	}
}
