package stage

import (
	"errors"
	"io/fs"
	"os"
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// parseStage は testdata/stages/ の data を Parse する。
func parseStage(t *testing.T, name, data string) *Stage {
	t.Helper()
	st, err := Parse("stages/"+name, []byte(data), fixtureIslands(t))
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return st
}

// alphaSimConfig は SC-1 の期待値。
func alphaSimConfig() sim.Config {
	return sim.Config{
		Islands:       3,
		InitialLeader: 1,
		DurationMs:    180000,
		Raft: sim.RaftConfig{
			TickMs:          500,
			HeartbeatTick:   2,
			ElectionTick:    40,
			MaxSizePerMsg:   4096,
			MaxInflightMsgs: 4,
		},
		Routes: []ferry.RouteConfig{
			{A: 1, B: 2, TravelMs: 1500},
			{A: 2, B: 3, TravelMs: 4000},
		},
		Ferries: []ferry.FerryConfig{
			{ID: "f1", Name: "テスト丸", Home: 1, Capacity: 4, SpeedPermille: 1000},
		},
		DockMs:        1500,
		PortCap:       24,
		PerLinkAppCap: 4,
		Resources: []ledger.ResourceDef{
			{ID: "firework:a", Kind: "firework", Capacity: 0},
			{ID: "stall:main", Kind: "stall", Capacity: 2},
		},
	}
}

// SC-1 alpha.yaml の変換。
func TestSimConfigAlpha(t *testing.T) {
	t.Parallel()
	st := parseStage(t, "alpha.yaml", readTestdata(t, "stages/alpha.yaml"))
	cfg, err := SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	if want := alphaSimConfig(); !reflect.DeepEqual(cfg, want) {
		t.Errorf("SimConfig(alpha) =\n%+v\nwant\n%+v", cfg, want)
	}
}

// SC-2 作った設定で Engine が作れて、ラウンドの終わりまで動く。
func TestSimConfigEngineRuns(t *testing.T) {
	t.Parallel()
	st := parseStage(t, "alpha.yaml", readTestdata(t, "stages/alpha.yaml"))
	cfg, err := SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	e, err := sim.New(cfg, 20261101, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("sim.New: %v", err)
	}
	if ack := e.Apply(sim.Command{Type: sim.CmdGameStart}); !ack.OK {
		t.Fatalf("Apply(game.start) = %+v", ack)
	}
	e.RunUntil(cfg.DurationMs)
	if got := e.Phase(); got != sim.PhaseEnded {
		t.Errorf("Phase() = %q, want %q", got, sim.PhaseEnded)
	}
}

// SC-3 checkQuorum・preVote が false なら *Error。
func TestSimConfigRaftFlags(t *testing.T) {
	t.Parallel()
	alpha := readTestdata(t, "stages/alpha.yaml")
	cq := edit{"checkQuorum: true", "checkQuorum: false"}
	pv := edit{"preVote: true", "preVote: false"}
	cases := []struct {
		name  string
		edits []edit
		paths []string
	}{
		{"checkQuorum", []edit{cq}, []string{"raft.checkQuorum"}},
		{"preVote", []edit{pv}, []string{"raft.preVote"}},
		{"both", []edit{cq, pv}, []string{"raft.checkQuorum", "raft.preVote"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := parseStage(t, "alpha.yaml", applyEdits(t, alpha, tc.edits...))
			cfg, err := SimConfig(st)
			se := asError(t, err)
			if se.File != "alpha" {
				t.Errorf("File = %q, want %q", se.File, "alpha")
			}
			paths := make([]string, len(se.Issues))
			for i, is := range se.Issues {
				paths[i] = is.Path
				if is.Message == "" {
					t.Errorf("Message が空の Issue: %+v", is)
				}
			}
			if !reflect.DeepEqual(paths, tc.paths) {
				t.Errorf("Issues の Path = %v, want %v", paths, tc.paths)
			}
			if !reflect.DeepEqual(cfg, sim.Config{}) {
				t.Errorf("誤りのときの sim.Config がゼロ値でない: %+v", cfg)
			}
		})
	}
}

// SC-4 beta.yaml（preVote: false）は *Error。
func TestSimConfigBeta(t *testing.T) {
	t.Parallel()
	st := parseStage(t, "beta.yaml", readTestdata(t, "stages/beta.yaml"))
	cfg, err := SimConfig(st)
	se := asError(t, err)
	if se.File != st.ID {
		t.Errorf("File = %q, want %q", se.File, st.ID)
	}
	if len(se.Issues) != 1 || se.Issues[0].Path != "raft.preVote" {
		t.Errorf("Issues = %+v, want [raft.preVote] の 1 件", se.Issues)
	}
	if !reflect.DeepEqual(cfg, sim.Config{}) {
		t.Errorf("誤りのときの sim.Config がゼロ値でない: %+v", cfg)
	}
}

// SC-5 relayCap は結果に影響しない。
func TestSimConfigIgnoresRelayCap(t *testing.T) {
	t.Parallel()
	data := applyEdits(t, readTestdata(t, "stages/alpha.yaml"), edit{"relayCap: 12", "relayCap: 0"})
	st := parseStage(t, "alpha.yaml", data)
	cfg, err := SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	if want := alphaSimConfig(); !reflect.DeepEqual(cfg, want) {
		t.Errorf("SimConfig(relayCap: 0) =\n%+v\nwant\n%+v", cfg, want)
	}
}

// SC-6 返した値は st とスライスを共有せず、同じ st からは同じ値になる。
func TestSimConfigCopies(t *testing.T) {
	t.Parallel()
	st := parseStage(t, "alpha.yaml", readTestdata(t, "stages/alpha.yaml"))
	first, err := SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	second, err := SimConfig(st)
	if err != nil {
		t.Fatalf("SimConfig: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("2 回の結果が違う:\n%+v\n%+v", first, second)
	}

	travel, capacity, id := st.Routes[0].TravelMs, st.Ferries[0].Capacity, st.Resources[0].ID
	first.Routes[0].TravelMs = 99999
	first.Ferries[0].Capacity = 99
	first.Resources[0].ID = "changed"
	if st.Routes[0].TravelMs != travel {
		t.Errorf("st.Routes[0].TravelMs が変わった: %d", st.Routes[0].TravelMs)
	}
	if st.Ferries[0].Capacity != capacity {
		t.Errorf("st.Ferries[0].Capacity が変わった: %d", st.Ferries[0].Capacity)
	}
	if st.Resources[0].ID != id {
		t.Errorf("st.Resources[0].ID が変わった: %q", st.Resources[0].ID)
	}
	if !reflect.DeepEqual(second, alphaSimConfig()) {
		t.Errorf("別の結果が変わった: %+v", second)
	}
}

// SC-7 nil はエラー（panic しない）。
func TestSimConfigNil(t *testing.T) {
	t.Parallel()
	cfg, err := SimConfig(nil)
	if err == nil {
		t.Fatal("SimConfig(nil) がエラーにならなかった")
	}
	if !reflect.DeepEqual(cfg, sim.Config{}) {
		t.Errorf("sim.Config がゼロ値でない: %+v", cfg)
	}
}

// SC-8 リポジトリのステージはすべて変換でき、sim.New も通る。
func TestSimConfigRepoData(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("../../../data/islands.json"); errors.Is(err, fs.ErrNotExist) {
		t.Skip("data/islands.json がない。提供データを置いて islandsdata で作る（data/raw/README.md）")
	}
	stages, err := LoadDir(os.DirFS("../../.."), "data/islands.json", "data/stages")
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range stages {
		cfg, err := SimConfig(st)
		if err != nil {
			t.Errorf("%s: SimConfig: %v", st.ID, err)
			continue
		}
		if _, err := sim.New(cfg, 20261101, sim.Options{CheckInvariants: true}); err != nil {
			t.Errorf("%s: sim.New: %v", st.ID, err)
		}
	}
}
