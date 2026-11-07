package cluster

import (
	"reflect"
	"testing"

	"go.etcd.io/raft/v3/tracker"
)

func TestProgressLeader(t *testing.T) { // P-1
	t.Parallel()
	c, _ := openCluster(t, 1)
	leader := c.byID[1]
	c.proposeABC(t, leader)
	prs := leader.Progress()
	if len(prs) != len(c.nodes) {
		t.Fatalf("len(Progress()) = %d, want %d", len(prs), len(c.nodes))
	}
	last := leader.Status().LastIndex
	for i, pr := range prs {
		if want := uint64(i + 1); pr.ID != want {
			t.Errorf("Progress()[%d].ID = %d, want %d", i, pr.ID, want)
		}
		if pr.Match > last {
			t.Errorf("node %d: Match = %d > LastIndex %d", pr.ID, pr.Match, last)
		}
		if pr.Next <= pr.Match {
			t.Errorf("node %d: Next = %d <= Match %d", pr.ID, pr.Next, pr.Match)
		}
		if pr.ID != leader.ID() && pr.State != tracker.StateReplicate {
			t.Errorf("node %d: State = %v, want StateReplicate", pr.ID, pr.State)
		}
	}
}

func TestProgressFollowerIsNil(t *testing.T) { // P-2
	t.Parallel()
	c, _ := openCluster(t, 1)
	for _, id := range []uint64{2, 3} {
		if got := c.byID[id].Progress(); got != nil {
			t.Errorf("node %d: Progress() = %+v, want nil", id, got)
		}
	}
}

func TestProgressIsReadOnly(t *testing.T) { // P-3
	t.Parallel()
	c, _ := openCluster(t, 1)
	c.proposeABC(t, c.byID[1])
	for _, n := range c.nodes {
		if n.rn.HasReady() {
			t.Fatalf("node %d: HasReady before Progress, want false", n.ID())
		}
		st := n.Status()
		l := mustLog(t, n)
		first := n.Progress()
		if got := n.Progress(); !reflect.DeepEqual(got, first) {
			t.Errorf("node %d: Progress() changed between calls: %+v → %+v", n.ID(), first, got)
		}
		if got := n.Status(); got != st {
			t.Errorf("node %d: Status changed by Progress: %+v → %+v", n.ID(), st, got)
		}
		if got := mustLog(t, n); !reflect.DeepEqual(got, l) {
			t.Errorf("node %d: Log changed by Progress", n.ID())
		}
		if n.rn.HasReady() {
			t.Errorf("node %d: HasReady after Progress, want false", n.ID())
		}
	}
}
