package cluster

import (
	"reflect"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
)

func mustLog(t *testing.T, n *Node) []LogEntry {
	t.Helper()
	l, err := n.Log()
	if err != nil {
		t.Fatalf("node %d: Log: %v", n.ID(), err)
	}
	return l
}

func TestLogAfterOpening(t *testing.T) { // LG-1
	t.Parallel()
	c, _ := openCluster(t, 1)
	var first []LogEntry
	for _, n := range c.nodes {
		l := mustLog(t, n)
		if len(l) != 4 {
			t.Fatalf("node %d: len(Log()) = %d, want 4", n.ID(), len(l))
		}
		for i, ent := range l {
			want := LogEntry{Index: uint64(i + 1), Term: 1, Type: pb.EntryConfChange}
			if i == 3 {
				want = LogEntry{Index: 4, Term: 2, Type: pb.EntryNormal}
			}
			if ent.Index != want.Index || ent.Term != want.Term || ent.Type != want.Type {
				t.Errorf("node %d: entry %d = %+v, want index %d term %d type %v", n.ID(), i, ent, want.Index, want.Term, want.Type)
			}
			if i == 3 && len(ent.Data) != 0 {
				t.Errorf("node %d: entry 4 data = %q, want empty", n.ID(), ent.Data)
			}
		}
		if got := n.Status().LastIndex; uint64(len(l)) != got {
			t.Errorf("node %d: len(Log()) = %d, LastIndex = %d", n.ID(), len(l), got)
		}
		if first == nil {
			first = l
		} else if !reflect.DeepEqual(l, first) {
			t.Errorf("node %d: Log() = %+v, want %+v", n.ID(), l, first)
		}
	}
}

func TestLogIsCopyAndReadOnly(t *testing.T) { // LG-2
	t.Parallel()
	c, _ := openCluster(t, 1)
	leader := c.byID[1]
	if err := leader.Propose([]byte("x")); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if _, err := leader.Process(); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if leader.rn.HasReady() {
		t.Fatal("HasReady after Process, want false")
	}
	before := leader.Status()
	l := mustLog(t, leader)
	if len(l) != 5 {
		t.Fatalf("len(Log()) = %d, want 5", len(l))
	}
	if got := l[4]; got.Index != 5 || got.Term != 2 || got.Type != pb.EntryNormal || string(got.Data) != "x" {
		t.Errorf("entry 5 = %+v, want index 5 term 2 normal data x", got)
	}
	l[4].Data[0] = 'y'
	if got := mustLog(t, leader); string(got[4].Data) != "x" {
		t.Errorf("entry 5 data after modifying the copy = %q, want x", got[4].Data)
	}
	if after := leader.Status(); after != before {
		t.Errorf("Status changed by Log: %+v → %+v", before, after)
	}
	if leader.rn.HasReady() {
		t.Error("HasReady after Log, want false")
	}
}

func TestLogBeforeProcess(t *testing.T) { // LG-3
	t.Parallel()
	c := newTestCluster(t, 1)
	for _, n := range c.nodes {
		l, err := n.Log()
		if err != nil || len(l) != 0 {
			t.Errorf("node %d: Log() = %+v, %v; want empty, nil", n.ID(), l, err)
		}
	}
}
