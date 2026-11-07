package cluster

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/fnv"
	"math/rand/v2"
	"slices"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

// maxDeliveries は即時配達の上限（§5.3 の開会式の上限と同じ）。
const maxDeliveries = 1000

// testCluster は、船を使わず、出てきた手紙をすぐ宛先に Step する 3 ノードのクラスタ。
type testCluster struct {
	nodes     []*Node // ID の昇順
	byID      map[uint64]*Node
	applied   map[uint64][]Applied
	delivered int
	hash      hash.Hash
}

func fnv64a(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func newTestCluster(t *testing.T, seed uint64) *testCluster {
	t.Helper()
	ids := []uint64{1, 2, 3}
	c := &testCluster{
		byID:    make(map[uint64]*Node),
		applied: make(map[uint64][]Applied),
		hash:    sha256.New(),
	}
	for _, id := range ids {
		r := rand.New(rand.NewPCG(seed, fnv64a(fmt.Sprintf("raft/%d", id))))
		n, err := NewNode(NodeConfig{
			ID:              id,
			Peers:           ids,
			ElectionTick:    16,
			HeartbeatTick:   2,
			MaxSizePerMsg:   4096,
			MaxInflightMsgs: 4,
			RandIntn:        r.IntN,
		})
		if err != nil {
			t.Fatalf("NewNode(%d): %v", id, err)
		}
		c.nodes = append(c.nodes, n)
		c.byID[id] = n
	}
	return c
}

// settle は、全ノードの Ready を処理して手紙を FIFO で 1 通ずつ配達することを、
// 手紙がなくなるまでくり返す。配達した手紙の数を返す。
func (c *testCluster) settle(t *testing.T) int {
	t.Helper()
	var queue []*pb.Message
	count := 0
	for {
		for _, n := range c.nodes {
			out, applied, err := n.processReady()
			if err != nil {
				t.Fatalf("node %d: processReady: %v", n.ID(), err)
			}
			queue = append(queue, out...)
			for _, a := range applied {
				c.hashApplied(t, n.ID(), a)
			}
			c.applied[n.ID()] = append(c.applied[n.ID()], applied...)
		}
		if len(queue) == 0 {
			return count
		}
		m := queue[0]
		queue = queue[1:]
		if count >= maxDeliveries {
			t.Fatalf("immediate delivery did not settle within %d messages", maxDeliveries)
		}
		count++
		c.delivered++
		c.hashMessage(t, m)
		dst, ok := c.byID[m.GetTo()]
		if !ok {
			t.Fatalf("message to unknown node %d", m.GetTo())
		}
		if err := dst.Deliver(m); err != nil {
			t.Fatalf("node %d: Deliver(%v): %v", m.GetTo(), m.GetType(), err)
		}
	}
}

func (c *testCluster) hashMessage(t *testing.T, m *pb.Message) {
	t.Helper()
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	c.writeUint64(uint64(len(b)))
	_, _ = c.hash.Write(b)
}

func (c *testCluster) hashApplied(t *testing.T, id uint64, a Applied) {
	t.Helper()
	c.writeUint64(id)
	c.writeUint64(a.Index)
	c.writeUint64(a.Term)
	c.writeUint64(uint64(len(a.Data)))
	_, _ = c.hash.Write(a.Data)
}

func (c *testCluster) writeUint64(v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	_, _ = c.hash.Write(buf[:])
}

func (c *testCluster) sum() []byte { return c.hash.Sum(nil) }

// bootstrapAndElect は、Bootstrap の構成変更を全ノードで適用し、ノード 1 を立候補させて落ち着かせる。
func (c *testCluster) bootstrapAndElect(t *testing.T) {
	t.Helper()
	c.settle(t)
	for _, n := range c.nodes {
		if n.applied != 3 {
			t.Fatalf("node %d: applied = %d after bootstrap, want 3", n.ID(), n.applied)
		}
	}
	if err := c.byID[1].rn.Campaign(); err != nil {
		t.Fatalf("Campaign: %v", err)
	}
	c.settle(t)
}

func (c *testCluster) proposeABC(t *testing.T, leader *Node) {
	t.Helper()
	for _, s := range []string{"a", "b", "c"} {
		if err := leader.rn.Propose([]byte(s)); err != nil {
			t.Fatalf("Propose(%q): %v", s, err)
		}
	}
	c.settle(t)
}

// checkElected は、ノード 1 が Leader で、全ノードが同じ Term・Lead 1 であることを確かめ、Term を返す。
func checkElected(t *testing.T, c *testCluster) uint64 {
	t.Helper()
	term := c.byID[1].rn.BasicStatus().HardState.GetTerm()
	for _, n := range c.nodes {
		st := n.rn.BasicStatus()
		want := raft.StateFollower
		if n.ID() == 1 {
			want = raft.StateLeader
		}
		if st.RaftState != want {
			t.Errorf("node %d: state = %v, want %v", n.ID(), st.RaftState, want)
		}
		if st.Lead != 1 {
			t.Errorf("node %d: lead = %d, want 1", n.ID(), st.Lead)
		}
		if got := st.HardState.GetTerm(); got != term {
			t.Errorf("node %d: term = %d, want %d", n.ID(), got, term)
		}
	}
	return term
}

func checkCommitApplied(t *testing.T, c *testCluster, want uint64) {
	t.Helper()
	for _, n := range c.nodes {
		st := n.rn.BasicStatus()
		if got := st.HardState.GetCommit(); got != want {
			t.Errorf("node %d: commit = %d, want %d", n.ID(), got, want)
		}
		if n.applied != want {
			t.Errorf("node %d: applied index = %d, want %d", n.ID(), n.applied, want)
		}
	}
}

func appliedEqual(a, b []Applied) bool {
	return slices.EqualFunc(a, b, func(x, y Applied) bool {
		return x.Index == y.Index && x.Term == y.Term && string(x.Data) == string(y.Data)
	})
}

func TestThreeNodesElectProposeCommitApply(t *testing.T) {
	t.Parallel()
	c := newTestCluster(t, 1)
	c.bootstrapAndElect(t)

	// E-1
	term := checkElected(t, c)
	t.Logf("term after election = %d, delivered = %d", term, c.delivered)
	checkCommitApplied(t, c, 4) // 構成変更 3 つ＋空のエントリ
	for _, n := range c.nodes {
		if got := len(c.applied[n.ID()]); got != 0 {
			t.Errorf("node %d: %d applied entries after election, want 0", n.ID(), got)
		}
	}

	// E-2
	c.proposeABC(t, c.byID[1])
	want := []Applied{
		{Index: 5, Term: term, Data: []byte("a")},
		{Index: 6, Term: term, Data: []byte("b")},
		{Index: 7, Term: term, Data: []byte("c")},
	}
	for _, n := range c.nodes {
		if got := c.applied[n.ID()]; !appliedEqual(got, want) {
			t.Errorf("node %d: applied = %+v, want %+v", n.ID(), got, want)
		}
	}
	checkCommitApplied(t, c, 7)
	if c.delivered > maxDeliveries {
		t.Errorf("delivered %d messages, want <= %d", c.delivered, maxDeliveries)
	}
}

func TestFollowerProposalIsDropped(t *testing.T) {
	t.Parallel()
	c := newTestCluster(t, 2)
	c.bootstrapAndElect(t)
	checkElected(t, c)

	if err := c.byID[2].rn.Propose([]byte("x")); !errors.Is(err, raft.ErrProposalDropped) {
		t.Fatalf("follower Propose: err = %v, want ErrProposalDropped", err)
	}
	c.settle(t)
	checkCommitApplied(t, c, 4)
	for _, n := range c.nodes {
		if got := len(c.applied[n.ID()]); got != 0 {
			t.Errorf("node %d: %d applied entries, want 0", n.ID(), got)
		}
	}
}

func TestCampaignBeforeApplyingBootstrapIsIgnored(t *testing.T) {
	t.Parallel()
	c := newTestCluster(t, 3)
	if err := c.byID[1].rn.Campaign(); err != nil {
		t.Fatalf("Campaign: %v", err)
	}
	c.settle(t)
	for _, n := range c.nodes {
		st := n.rn.BasicStatus()
		if st.RaftState != raft.StateFollower {
			t.Errorf("node %d: state = %v, want StateFollower", n.ID(), st.RaftState)
		}
		if st.Lead != 0 {
			t.Errorf("node %d: lead = %d, want 0", n.ID(), st.Lead)
		}
	}
}

func TestImmediateDeliveryIsDeterministic(t *testing.T) {
	t.Parallel()
	run := func() (int, []byte) {
		c := newTestCluster(t, 42)
		c.bootstrapAndElect(t)
		c.proposeABC(t, c.byID[1])
		return c.delivered, c.sum()
	}
	n1, h1 := run()
	n2, h2 := run()
	if n1 != n2 {
		t.Errorf("delivered = %d and %d, want equal", n1, n2)
	}
	if string(h1) != string(h2) {
		t.Errorf("hash = %x and %x, want equal", h1, h2)
	}
	if n1 == 0 {
		t.Errorf("delivered no messages")
	}
}
