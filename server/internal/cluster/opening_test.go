package cluster

import (
	"errors"
	"reflect"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

// expectedOpeningDeliveries は事前の確認（3 ノード・シード 1）での配達の通数。
const expectedOpeningDeliveries = 16

func TestOpeningElectsInitialLeader(t *testing.T) { // P-1
	t.Parallel()
	c, res := openCluster(t, 1)
	if res.Leader != 1 || res.Term != 2 {
		t.Errorf("Leader, Term = %d, %d, want 1, 2", res.Leader, res.Term)
	}
	if got := len(res.Delivered); got != expectedOpeningDeliveries {
		t.Errorf("len(Delivered) = %d, want %d", got, expectedOpeningDeliveries)
	}
	checkStatus(t, c, 1, 2, 4)
	for _, m := range res.Delivered {
		if to := m.GetTo(); to < 1 || to > 3 {
			t.Errorf("delivered message to %d", to)
		}
	}
}

func TestOpeningOtherInitialLeader(t *testing.T) { // P-2
	t.Parallel()
	c := newTestCluster(t, 1)
	res, err := Opening(c.nodes, 3)
	if err != nil {
		t.Fatalf("Opening: %v", err)
	}
	if res.Leader != 3 {
		t.Errorf("Leader = %d, want 3", res.Leader)
	}
	for _, n := range c.nodes {
		if got := n.Status().Lead; got != 3 {
			t.Errorf("node %d: Lead = %d, want 3", n.ID(), got)
		}
	}
}

func marshalAll(t *testing.T, ms []*pb.Message) [][]byte {
	t.Helper()
	out := make([][]byte, len(ms))
	for i, m := range ms {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		out[i] = b
	}
	return out
}

func checkSameOpening(t *testing.T, a, b OpeningResult) {
	t.Helper()
	if !reflect.DeepEqual(marshalAll(t, a.Delivered), marshalAll(t, b.Delivered)) {
		t.Errorf("Delivered differ (%d vs %d messages)", len(a.Delivered), len(b.Delivered))
	}
	if !reflect.DeepEqual(a.Events, b.Events) {
		t.Errorf("Events differ:\n%+v\n%+v", a.Events, b.Events)
	}
	if a.Leader != b.Leader || a.Term != b.Term {
		t.Errorf("Leader/Term differ: %d/%d vs %d/%d", a.Leader, a.Term, b.Leader, b.Term)
	}
}

func TestOpeningIsDeterministic(t *testing.T) { // P-3
	t.Parallel()
	_, a := openCluster(t, 20261101)
	_, b := openCluster(t, 20261101)
	checkSameOpening(t, a, b)
}

func TestOpeningNodeOrderDoesNotMatter(t *testing.T) { // P-4
	t.Parallel()
	_, a := openCluster(t, 7)
	c := newTestCluster(t, 7)
	b, err := Opening([]*Node{c.byID[3], c.byID[1], c.byID[2]}, 1)
	if err != nil {
		t.Fatalf("Opening: %v", err)
	}
	checkSameOpening(t, a, b)
}

func TestOpeningDeliveryLimit(t *testing.T) { // P-5
	t.Parallel()
	_, ref := openCluster(t, 1)
	limit := len(ref.Delivered)
	c1 := newTestCluster(t, 1)
	if _, err := opening(c1.nodes, 1, limit-1); !errors.Is(err, errOpeningTooManyDeliveries) {
		t.Errorf("opening(limit %d): err = %v, want errOpeningTooManyDeliveries", limit-1, err)
	}
	c2 := newTestCluster(t, 1)
	if _, err := opening(c2.nodes, 1, limit); err != nil {
		t.Errorf("opening(limit %d): %v", limit, err)
	}
}

func TestOpeningValidation(t *testing.T) { // P-6
	t.Parallel()
	tests := []struct {
		name   string
		nodes  func(c *testCluster) []*Node
		leader uint64
	}{
		{name: "empty", nodes: func(*testCluster) []*Node { return nil }, leader: 1},
		{name: "nil element", nodes: func(c *testCluster) []*Node { return []*Node{c.byID[1], nil, c.byID[3]} }, leader: 1},
		{name: "duplicate", nodes: func(c *testCluster) []*Node { return []*Node{c.byID[1], c.byID[2], c.byID[2], c.byID[3]} }, leader: 1},
		{name: "unknown leader", nodes: func(c *testCluster) []*Node { return c.nodes }, leader: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newTestCluster(t, 1)
			if _, err := Opening(tt.nodes(c), tt.leader); err == nil {
				t.Fatalf("Opening: err = nil, want error")
			}
			for _, n := range c.nodes {
				if n.applied != 0 {
					t.Errorf("node %d: applied = %d, want 0", n.ID(), n.applied)
				}
			}
		})
	}
	t.Run("twice", func(t *testing.T) {
		t.Parallel()
		c, _ := openCluster(t, 1)
		if _, err := Opening(c.nodes, 1); err == nil {
			t.Fatalf("second Opening: err = nil, want error")
		}
	})
}

func TestOpeningMessageToUnknownNode(t *testing.T) { // P-7
	t.Parallel()
	c := newTestCluster(t, 1)
	if _, err := Opening([]*Node{c.byID[1], c.byID[2]}, 1); err == nil {
		t.Fatalf("Opening: err = nil, want error for message to node 3")
	}
}

func TestOpeningFiveNodes(t *testing.T) {
	t.Parallel()
	c := newTestClusterN(t, 1, 5)
	res, err := Opening(c.nodes, 1)
	if err != nil {
		t.Fatalf("Opening: %v", err)
	}
	if res.Leader != 1 {
		t.Errorf("Leader = %d, want 1", res.Leader)
	}
	for _, n := range c.nodes {
		if got := n.Status().Lead; got != 1 {
			t.Errorf("node %d: Lead = %d, want 1", n.ID(), got)
		}
	}
}
