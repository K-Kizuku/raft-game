package cluster

import (
	"errors"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
)

func TestProposeAfterOpening(t *testing.T) { // C-1
	t.Parallel()
	c, res := openCluster(t, 1)
	if res.Leader != 1 {
		t.Fatalf("Opening leader = %d, want 1", res.Leader)
	}

	if err := c.byID[1].Propose([]byte("x")); err != nil {
		t.Fatalf("leader Propose: %v", err)
	}
	out, err := c.byID[1].Process()
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	var to []uint64
	for _, m := range out.Messages {
		if m.GetType() == pb.MsgApp {
			to = append(to, m.GetTo())
		}
	}
	if len(to) != 2 || to[0] != 2 || to[1] != 3 {
		t.Errorf("MsgApp destinations = %v, want [2 3]", to)
	}

	for _, id := range []uint64{2, 3} {
		if err := c.byID[id].Propose([]byte("y")); !errors.Is(err, raft.ErrProposalDropped) {
			t.Errorf("follower %d Propose = %v, want ErrProposalDropped", id, err)
		}
	}
}
