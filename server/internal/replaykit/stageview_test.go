package replaykit

import (
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

func viewTestStage() *stage.Stage {
	return &stage.Stage{
		ID:    "x",
		Title: "題",
		Islands: []stage.Island{
			{NodeID: 1, Name: "一", X: 10, Y: 20, Color: "#000000", Population: 100},
			{NodeID: 2, Name: "二", X: 30, Y: 40, Color: "#FFFFFF", Population: 200},
			{NodeID: 3, Name: "三", X: 50, Y: 60, Color: "#123456", Population: 300},
		},
		Routes: []stage.Route{
			{A: 3, B: 1, TravelMs: 9000, Provenance: stage.Derived},
			{A: 1, B: 2, TravelMs: 4000, Provenance: stage.Fact},
		},
		Resources: []stage.Resource{
			{ID: "r2", Kind: stage.ResourceStall, Label: "屋台", Capacity: 2},
			{ID: "r1", Kind: stage.ResourceFirework, Label: "花火", Critical: true},
		},
	}
}

// RK-3: 島・航路・資源はステージの定義の順。航路の ID は小さい島 ID が先。
func TestStageViewOrder(t *testing.T) {
	t.Parallel()
	v := StageView(viewTestStage(), nil)
	if v.Title != "題" {
		t.Errorf("Title = %q", v.Title)
	}
	wantIslands := []protocol.Island{
		{ID: 1, Name: "一", Layout: protocol.IslandLayout{X: 10, Y: 20}, Population: protocol.Population{Value: 100}, Color: "#000000"},
		{ID: 2, Name: "二", Layout: protocol.IslandLayout{X: 30, Y: 40}, Population: protocol.Population{Value: 200}, Color: "#FFFFFF"},
		{ID: 3, Name: "三", Layout: protocol.IslandLayout{X: 50, Y: 60}, Population: protocol.Population{Value: 300}, Color: "#123456"},
	}
	if !reflect.DeepEqual(v.Islands, wantIslands) {
		t.Errorf("Islands = %+v, want %+v", v.Islands, wantIslands)
	}
	wantRoutes := []protocol.Route{
		{ID: "1-3", A: 3, B: 1, TravelMs: 9000, Provenance: "derived"},
		{ID: "1-2", A: 1, B: 2, TravelMs: 4000, Provenance: "fact"},
	}
	if !reflect.DeepEqual(v.Routes, wantRoutes) {
		t.Errorf("Routes = %+v, want %+v", v.Routes, wantRoutes)
	}
	wantResources := []protocol.Resource{
		{ID: "r2", Kind: "stall", Label: "屋台"},
		{ID: "r1", Kind: "firework", Label: "花火", Critical: true},
	}
	if !reflect.DeepEqual(v.Resources, wantResources) {
		t.Errorf("Resources = %+v, want %+v", v.Resources, wantResources)
	}
	if v.DemoStorm != nil {
		t.Errorf("DemoStorm = %+v, want nil", v.DemoStorm)
	}
}

// RK-3: 表示用の値は、長さがステージの島の数と同じときだけ埋める。
func TestStageViewDisplay(t *testing.T) {
	t.Parallel()
	st := viewTestStage()
	islands := make([]stage.IslandDisplay, len(st.Islands))
	for i := range islands {
		islands[i] = stage.IslandDisplay{
			Municipalities: []string{"町"},
			Population:     stage.Measure{AsOf: "令和8年4月1日", Provenance: stage.Fact, Note: "注", Source: "s"},
		}
	}
	v := StageView(st, islands)
	for i, is := range v.Islands {
		if is.Municipality != islands[i].MunicipalityLabel() || is.Municipality == "" {
			t.Errorf("Islands[%d].Municipality = %q, want %q", i, is.Municipality, islands[i].MunicipalityLabel())
		}
		want := protocol.Population{Value: int(st.Islands[i].Population), AsOf: "令和8年4月1日", Provenance: "fact", Note: "注", Source: "s"}
		if is.Population != want {
			t.Errorf("Islands[%d].Population = %+v, want %+v", i, is.Population, want)
		}
	}

	short := StageView(st, islands[:2])
	for i, is := range short.Islands {
		if is.Municipality != "" || is.Population.AsOf != "" || is.Population.Source != "" {
			t.Errorf("Islands[%d] = %+v, want no display values", i, is)
		}
	}
}

func TestRouteID(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b uint64
		want string
	}{
		{3, 1, "1-3"},
		{1, 3, "1-3"},
		{2, 2, "2-2"},
		{10, 9, "9-10"},
	} {
		if got := RouteID(c.a, c.b); got != c.want {
			t.Errorf("RouteID(%d, %d) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestDemoStormView(t *testing.T) {
	t.Parallel()
	st := viewTestStage()
	st.Demo = &stage.Demo{Storm: &stage.DemoStorm{Routes: [][2]uint64{{3, 1}, {1, 2}, {3, 1}}, DurationMs: 1000}}
	v := StageView(st, nil).DemoStorm
	if v == nil {
		t.Fatal("DemoStorm = nil")
	}
	if want := []string{"1-3", "1-2", "1-3"}; !reflect.DeepEqual(v.RouteIDs, want) {
		t.Errorf("RouteIDs = %v, want %v", v.RouteIDs, want)
	}
	if v.Flavor != nil {
		t.Errorf("Flavor = %+v, want nil", v.Flavor)
	}
}

func TestStoryNames(t *testing.T) {
	t.Parallel()
	n := StoryNames(viewTestStage())
	if want := []string{"一", "二", "三"}; !reflect.DeepEqual(n.Islands, want) {
		t.Errorf("Islands = %v, want %v", n.Islands, want)
	}
	if want := map[string]string{"r1": "花火", "r2": "屋台"}; !reflect.DeepEqual(n.Resources, want) {
		t.Errorf("Resources = %v, want %v", n.Resources, want)
	}
}
