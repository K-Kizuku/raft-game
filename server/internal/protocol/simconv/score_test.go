package simconv

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

func scoreJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestIslandStatesFromSimEmpty(t *testing.T) { // SC-1
	t.Parallel()
	for name, in := range map[string][]sim.IslandState{"nil": nil, "空": {}} {
		got := IslandStatesFromSim(in)
		if got == nil || len(got) != 0 {
			t.Errorf("%s: got %#v, want an empty non-nil slice", name, got)
		}
		if j := scoreJSON(t, got); j != "[]" {
			t.Errorf("%s: JSON = %s, want []", name, j)
		}
	}
}

func TestIslandStatesFromSim(t *testing.T) { // SC-2
	t.Parallel()
	in := []sim.IslandState{
		{ID: 1, Satisfaction: 64, LastDeliveredAt: 60500},
		{ID: 2, Satisfaction: 60, LastDeliveredAt: 0},
	}
	got := IslandStatesFromSim(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for i, w := range []struct {
		id   uint64
		sat  int
		last int64
	}{{1, 64, 60500}, {2, 60, 0}} {
		g := got[i]
		if g.LastDeliveredAt == nil {
			t.Fatalf("[%d].LastDeliveredAt = nil", i)
		}
		if g.ID != w.id || g.Satisfaction != w.sat || *g.LastDeliveredAt != w.last {
			t.Errorf("[%d] = {%d %d %d}, want %+v", i, g.ID, g.Satisfaction, *g.LastDeliveredAt, w)
		}
	}
	const want = `[{"id":1,"satisfaction":64,"lastDeliveredAt":60500},{"id":2,"satisfaction":60,"lastDeliveredAt":0}]`
	if j := scoreJSON(t, got); j != want {
		t.Errorf("JSON = %s, want %s", j, want)
	}
	if got[0].LastDeliveredAt == got[1].LastDeliveredAt {
		t.Error("LastDeliveredAt shares a pointer between elements")
	}
	*got[0].LastDeliveredAt = 1
	if in[0].LastDeliveredAt != 60500 || *got[1].LastDeliveredAt != 0 {
		t.Errorf("changing the output changed the input or another element: in %+v", in)
	}
}

func TestScoreFromSimNil(t *testing.T) { // SC-3
	t.Parallel()
	got := ScoreFromSim(nil, 2)
	if want := (protocol.Score{Total: 0, Successes: 0, Target: 2, CriticalSecured: []string{}}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if j, want := scoreJSON(t, got), `{"total":0,"successes":0,"target":2,"criticalSecured":[]}`; j != want {
		t.Errorf("JSON = %s, want %s", j, want)
	}
}

func TestScoreFromSim(t *testing.T) { // SC-4
	t.Parallel()
	in := &sim.ScoreState{Total: -3, Successes: 1, Target: 2, CriticalSecured: []string{"firework:a"}}
	got := ScoreFromSim(in, 99)
	if want := (protocol.Score{Total: -3, Successes: 1, Target: 2, CriticalSecured: []string{"firework:a"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	got.CriticalSecured[0] = "x"
	if in.CriticalSecured[0] != "firework:a" {
		t.Errorf("changing the output changed the input: %v", in.CriticalSecured)
	}

	got = ScoreFromSim(&sim.ScoreState{Total: 5, Successes: 0, Target: 2, CriticalSecured: nil}, 99)
	if got.CriticalSecured == nil || len(got.CriticalSecured) != 0 {
		t.Errorf("CriticalSecured = %#v, want []", got.CriticalSecured)
	}
	if j, want := scoreJSON(t, got), `{"total":5,"successes":0,"target":2,"criticalSecured":[]}`; j != want {
		t.Errorf("JSON = %s, want %s", j, want)
	}
}
