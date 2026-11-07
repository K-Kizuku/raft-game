package server

import (
	"reflect"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol/stageconv"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// snapshot の stage.demoStorm（プロトコル §5.1。T-305b）。testCatalog の alpha を複製して Demo を足す。

// alphaWithDemo は alpha の複製に demo を足したものを返す（カタログの alpha は変えない）。
func alphaWithDemo(t *testing.T, demo *stage.Demo) *stage.Stage {
	t.Helper()
	st, ok := testCatalog(t).Lookup("alpha")
	if !ok {
		t.Fatal("alpha not found")
	}
	cp := *st
	cp.Demo = demo
	return &cp
}

func TestDemoStormViewNone(t *testing.T) { // SVD-1
	t.Parallel()
	s, _, alpha := resultTestSession(t)
	if alpha.Demo != nil {
		t.Fatalf("alpha has demo: %+v", alpha.Demo)
	}
	if v := stageView(alpha, nil); v.DemoStorm != nil {
		t.Errorf("stageView(alpha).DemoStorm = %+v, want nil", v.DemoStorm)
	}
	if j := mustJSON(t, s.buildSnapshot()); strings.Contains(j, "demoStorm") {
		t.Errorf("snapshot JSON has demoStorm: %s", j)
	}
}

func TestDemoStormViewStorm(t *testing.T) { // SVD-2
	t.Parallel()
	flavor := &stage.Text{
		Text: "海が荒れています", Provenance: stage.Derived,
		Source: &stage.Source{Dataset: "fixture-2026", Region: "星砂地域", Field: "DISASTER_LAND_CONSERVATION", Quote: "引用"},
	}
	st := alphaWithDemo(t, &stage.Demo{
		EndOnAllCriticalSecured: true,
		Storm:                   &stage.DemoStorm{Routes: [][2]uint64{{2, 1}}, DurationMs: 20000, Flavor: flavor},
	})
	v := stageView(st, nil).DemoStorm
	if v == nil {
		t.Fatal("DemoStorm = nil")
	}
	if want := []string{"1-2"}; !reflect.DeepEqual(v.RouteIDs, want) {
		t.Errorf("RouteIDs = %v, want %v", v.RouteIDs, want)
	}
	if want := stageconv.Text(*flavor); v.Flavor == nil || !reflect.DeepEqual(*v.Flavor, want) {
		t.Errorf("Flavor = %s, want %s", mustJSON(t, v.Flavor), mustJSON(t, want))
	}
}

func TestDemoStormViewNoFlavor(t *testing.T) { // SVD-3
	t.Parallel()
	st := alphaWithDemo(t, &stage.Demo{Storm: &stage.DemoStorm{Routes: [][2]uint64{{1, 2}}, DurationMs: 20000}})
	if j, want := mustJSON(t, stageView(st, nil).DemoStorm), `{"routeIds":["1-2"],"flavor":null}`; j != want {
		t.Errorf("DemoStorm = %s, want %s", j, want)
	}
	st = alphaWithDemo(t, &stage.Demo{EndOnAllCriticalSecured: true})
	if v := stageView(st, nil).DemoStorm; v != nil {
		t.Errorf("DemoStorm without storm = %+v, want nil", v)
	}
}
