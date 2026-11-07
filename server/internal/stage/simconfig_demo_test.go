package stage

import (
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// demoBetaStage は beta.yaml（demo.storm がある）を読んだステージ。
// beta は raft.preVote: false で SimConfig が誤りにするので、preVote だけ true にする。
func demoBetaStage(t *testing.T) *Stage {
	t.Helper()
	return parseStage(t, "beta.yaml", applyEdits(t, readTestdata(t, "stages/beta.yaml"), edit{"preVote: false", "preVote: true"}))
}

func demoSimConfig(t *testing.T, st *Stage) sim.Config {
	t.Helper()
	cfg, err := SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	return cfg
}

// SD-1 変換。
func TestSimConfigDemoStorm(t *testing.T) {
	t.Parallel()
	want := &sim.DemoStorm{Routes: [][2]uint64{{5, 1}}, DurationMs: 20000}
	if got := demoSimConfig(t, demoBetaStage(t)).DemoStorm; !reflect.DeepEqual(got, want) {
		t.Errorf("DemoStorm = %+v, want %+v", got, want)
	}
	// preVote を直さない beta そのもの（SimConfig は誤り）でも、変換は同じ。
	if got := demoStorm(parseFixture(t, "beta.yaml")); !reflect.DeepEqual(got, want) {
		t.Errorf("demoStorm(beta) = %+v, want %+v", got, want)
	}

	alpha := readTestdata(t, "stages/alpha.yaml")
	cases := []struct {
		name string
		yaml string
	}{
		{"demo がない", alpha},
		{"demo に storm がない", applyEdits(t, alpha, edit{"events: []\n", "events: []\ndemo:\n  endOnAllCriticalSecured: true\n"})},
	}
	for _, tc := range cases {
		st := parseStage(t, "alpha.yaml", tc.yaml)
		if got := demoSimConfig(t, st).DemoStorm; got != nil {
			t.Errorf("%s: DemoStorm = %+v, want nil", tc.name, got)
		}
	}
}

// SD-2 複製。
func TestSimConfigDemoStormCopied(t *testing.T) {
	t.Parallel()
	st := demoBetaStage(t)
	cfg := demoSimConfig(t, st)
	cfg.DemoStorm.Routes[0] = [2]uint64{2, 3}
	if got := st.Demo.Storm.Routes[0]; got != [2]uint64{5, 1} {
		t.Errorf("st.Demo.Storm.Routes[0] = %v, want [5 1]", got)
	}
}

// SD-3 sim.New が受け付ける。
func TestSimConfigDemoStormNew(t *testing.T) {
	t.Parallel()
	cfg := demoSimConfig(t, demoBetaStage(t))
	e, err := sim.New(cfg, 1, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("sim.New: %v", err)
	}
	if ack := e.Apply(sim.Command{Type: sim.CmdGameStart}); !ack.OK {
		t.Fatalf("cmd.game.start: %+v", ack)
	}
	if ack := e.Apply(sim.Command{Type: sim.CmdDemoTrigger, Event: sim.DemoEventStorm}); !ack.OK {
		t.Fatalf("demo.trigger: %+v", ack)
	}
}
