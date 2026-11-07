package cluster

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
)

// openCluster は 3 ノードのクラスタを作り、ノード 1 で開会式を行う。
func openCluster(t *testing.T, seed uint64) (*testCluster, OpeningResult) {
	t.Helper()
	c := newTestCluster(t, seed)
	res, err := Opening(c.nodes, 1)
	if err != nil {
		t.Fatalf("Opening: %v", err)
	}
	return c, res
}

func checkStatus(t *testing.T, c *testCluster, leader, term, index uint64) {
	t.Helper()
	for _, n := range c.nodes {
		want := Status{
			ID:        n.ID(),
			Role:      raft.StateFollower,
			Term:      term,
			Vote:      leader,
			Lead:      leader,
			Commit:    index,
			Applied:   index,
			LastIndex: index,
		}
		if n.ID() == leader {
			want.Role = raft.StateLeader
		}
		if got := n.Status(); got != want {
			t.Errorf("node %d: Status() = %+v, want %+v", n.ID(), got, want)
		}
	}
}

func TestProcessAppliesToStateMachine(t *testing.T) { // N-4
	t.Parallel()
	c, _ := openCluster(t, 1)
	c.proposeABC(t, c.byID[1])
	want := []smCall{{5, "a"}, {6, "b"}, {7, "c"}}
	for _, n := range c.nodes {
		if got := c.sms[n.ID()].got; !slices.Equal(got, want) {
			t.Errorf("node %d: StateMachine got %+v, want %+v", n.ID(), got, want)
		}
		applied := c.applied[n.ID()]
		if len(applied) != 3 {
			t.Fatalf("node %d: %d applied, want 3", n.ID(), len(applied))
		}
		for _, a := range applied {
			if want := fmt.Sprintf("n%d:%d", n.ID(), a.Index); a.Result != want {
				t.Errorf("node %d: Applied[%d].Result = %v, want %q", n.ID(), a.Index, a.Result, want)
			}
		}
	}
	checkStatus(t, c, 1, 2, 7) // N-9 の続き
}

func TestRoleChangeDuringElection(t *testing.T) { // N-5
	t.Parallel()
	_, res := openCluster(t, 1)
	want := []RoleChange{
		{Node: 1, Term: 1, PrevRole: raft.StateFollower, Role: raft.StatePreCandidate},
		{Node: 1, Term: 2, PrevRole: raft.StatePreCandidate, Role: raft.StateCandidate},
		{Node: 1, Term: 2, PrevRole: raft.StateCandidate, Role: raft.StateLeader, Lead: 1},
		{Node: 2, Term: 2, PrevRole: raft.StateFollower, Role: raft.StateFollower, Lead: 1},
		{Node: 3, Term: 2, PrevRole: raft.StateFollower, Role: raft.StateFollower, Lead: 1},
	}
	if !reflect.DeepEqual(res.Events, want) {
		t.Errorf("Events = %+v\nwant %+v", res.Events, want)
	}
}

func TestTickHeartbeat(t *testing.T) { // N-6
	t.Parallel()
	c, _ := openCluster(t, 1)
	l := c.byID[1]
	const heartbeatTick = 2 // testNodeConfig の HeartbeatTick
	for range heartbeatTick - 1 {
		l.Tick()
	}
	out, err := l.Process()
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(out.Messages) != 0 {
		t.Fatalf("messages before heartbeat = %v, want none", out.Messages)
	}
	l.Tick()
	out, err = l.Process()
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	var to []uint64
	for _, m := range out.Messages {
		if m.GetType() != pb.MessageType_MsgHeartbeat {
			t.Errorf("message type = %v, want MsgHeartbeat", m.GetType())
		}
		to = append(to, m.GetTo())
	}
	slices.Sort(to)
	if !slices.Equal(to, []uint64{2, 3}) {
		t.Errorf("heartbeat recipients = %v, want [2 3]", to)
	}
}

// tickAlone は node だけを手紙を配達せずに Tick し、毎回 Process する。最初の RoleChange が出た回数・
// その RoleChange・その Process の手紙を返す。limit 回までに出なければ失敗する。
func tickAlone(t *testing.T, n *Node, limit int) (int, RoleChange, []*pb.Message) {
	t.Helper()
	for i := 1; i <= limit; i++ {
		n.Tick()
		out, err := n.Process()
		if err != nil {
			t.Fatalf("node %d: Process: %v", n.ID(), err)
		}
		if len(out.Events) > 0 {
			if len(out.Events) != 1 {
				t.Fatalf("node %d: %d events in one Process, want 1: %+v", n.ID(), len(out.Events), out.Events)
			}
			return i, out.Events[0], out.Messages
		}
	}
	t.Fatalf("node %d: no RoleChange within %d ticks", n.ID(), limit)
	return 0, RoleChange{}, nil
}

const electionTick = 16 // testNodeConfig の ElectionTick

func TestTickFollowerElectionTimeout(t *testing.T) { // N-7
	t.Parallel()
	run := func() int {
		c, _ := openCluster(t, 1)
		i, ev, msgs := tickAlone(t, c.byID[2], 2*electionTick)
		if i < electionTick || i >= 2*electionTick {
			t.Errorf("election timeout after %d ticks, want [%d, %d)", i, electionTick, 2*electionTick)
		}
		want := RoleChange{Node: 2, Term: ev.Term, PrevRole: raft.StateFollower, Role: raft.StatePreCandidate, PrevLead: 1, Lead: 0}
		if ev != want {
			t.Errorf("RoleChange = %+v, want %+v", ev, want)
		}
		if len(msgs) != 2 {
			t.Errorf("%d messages, want 2 MsgPreVote", len(msgs))
		}
		for _, m := range msgs {
			if m.GetType() != pb.MessageType_MsgPreVote {
				t.Errorf("message type = %v, want MsgPreVote", m.GetType())
			}
		}
		return i
	}
	a, b := run(), run()
	t.Logf("ticks until PreCandidate = %d", a)
	if a != b {
		t.Errorf("ticks = %d and %d, want equal", a, b)
	}
}

func TestCheckQuorumStepDown(t *testing.T) { // N-8
	t.Parallel()
	c, res := openCluster(t, 1)
	i, ev, _ := tickAlone(t, c.byID[1], 2*electionTick)
	t.Logf("leader stepped down after %d ticks", i)
	want := RoleChange{Node: 1, Term: res.Term, PrevRole: raft.StateLeader, Role: raft.StateFollower, PrevLead: 1, Lead: 0}
	if ev != want {
		t.Errorf("RoleChange = %+v, want %+v", ev, want)
	}
	if res.Term != 2 {
		t.Errorf("opening term = %d, want 2", res.Term)
	}
}

func TestStatusAfterOpening(t *testing.T) { // N-9
	t.Parallel()
	c, _ := openCluster(t, 1)
	checkStatus(t, c, 1, 2, 4)
}
