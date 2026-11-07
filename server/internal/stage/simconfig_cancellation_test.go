package stage

import (
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/game"
)

// ST-C1 beta.yaml の cancellation（hama-todai・170・30000・10000）を写す。
// beta は raft.preVote: false で SimConfig が誤りになるので、cancellationDefs を直接呼ぶ。
func TestCancellationDefsBeta(t *testing.T) {
	t.Parallel()
	st := parseStage(t, "beta.yaml", readTestdata(t, "stages/beta.yaml"))
	got := cancellationDefs(st)
	// hama-todai は islands の 3 番目（ノード ID 3）。
	want := []game.CancellationDef{{Island: 3, ProbabilityPermille: 170, SlotMs: 30000, DurationMs: 10000}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cancellationDefs = %+v, want %+v", got, want)
	}
}

// ST-C2 欠航の無いステージ → nil（SimConfig の Cancellations も nil）。
func TestCancellationDefsNone(t *testing.T) {
	t.Parallel()
	st := parseStage(t, "alpha.yaml", readTestdata(t, "stages/alpha.yaml"))
	if got := cancellationDefs(st); got != nil {
		t.Errorf("cancellationDefs = %+v, want nil", got)
	}
	cfg, err := SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	if cfg.Cancellations != nil {
		t.Errorf("Cancellations = %+v, want nil", cfg.Cancellations)
	}
}
