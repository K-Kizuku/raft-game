package cluster

import (
	"errors"
	"slices"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
)

// recStorage は MemoryStorage を包み、Append・SetHardState を記録してから元の処理に渡す（V-5）。
type recStorage struct {
	*raft.MemoryStorage
	rec       *[]readyStep
	appendErr error
	hsErr     error
}

func (s *recStorage) Append(entries []*pb.Entry) error {
	*s.rec = append(*s.rec, stepAppend)
	if s.appendErr != nil {
		return s.appendErr
	}
	return s.MemoryStorage.Append(entries)
}

func (s *recStorage) SetHardState(st *pb.HardState) error {
	*s.rec = append(*s.rec, stepSetHardState)
	if s.hsErr != nil {
		return s.hsErr
	}
	return s.MemoryStorage.SetHardState(st)
}

func newRecordedNode(cfg NodeConfig, appendErr, hsErr error) (*Node, *[]readyStep, error) {
	rec := new([]readyStep)
	n, err := newNode(cfg, &recStorage{MemoryStorage: raft.NewMemoryStorage(), rec: rec, appendErr: appendErr, hsErr: hsErr})
	if err != nil {
		return nil, nil, err
	}
	n.onStep = func(s readyStep) { *rec = append(*rec, s) }
	return n, rec, nil
}

func TestReadyOrder(t *testing.T) { // O-1
	t.Parallel()
	recs := make(map[uint64]*[]readyStep)
	ids := []uint64{1, 2, 3}
	c := newTestClusterWith(t, 1, ids, func(cfg NodeConfig) (*Node, error) {
		n, rec, err := newRecordedNode(cfg, nil, nil)
		if err == nil {
			recs[cfg.ID] = rec
		}
		return n, err
	})
	if _, err := Opening(c.nodes, 1); err != nil {
		t.Fatalf("Opening: %v", err)
	}
	c.proposeABC(t, c.byID[1])

	full := false
	for _, id := range ids {
		rec := *recs[id]
		if len(rec) == 0 || rec[len(rec)-1] != stepAdvance {
			t.Fatalf("node %d: record does not end with stepAdvance: %v", id, rec)
		}
		start := 0
		for i, s := range rec {
			if s != stepAdvance {
				continue
			}
			seg := rec[start : i+1]
			start = i + 1
			count := func(x readyStep) int {
				k := 0
				for _, s := range seg {
					if s == x {
						k++
					}
				}
				return k
			}
			if got := count(stepAppend); got != 1 {
				t.Errorf("node %d: Ready %v: %d stepAppend, want 1", id, seg, got)
			}
			if got := count(stepSetHardState); got > 1 {
				t.Errorf("node %d: Ready %v: %d stepSetHardState, want 0..1", id, seg, got)
			}
			if got := count(stepAdvance); got != 1 {
				t.Errorf("node %d: Ready %v: %d stepAdvance, want 1", id, seg, got)
			}
			if !slices.IsSorted(seg) {
				t.Errorf("node %d: Ready %v: steps out of order", id, seg)
			}
			if count(stepSetHardState) > 0 && count(stepSend) > 0 && count(stepApply) > 0 {
				full = true
			}
		}
	}
	if !full {
		t.Errorf("no Ready contains stepSetHardState, stepSend and stepApply together")
	}
}

func TestAppendErrorStopsReady(t *testing.T) { // O-2
	t.Parallel()
	errBoom := errors.New("boom")
	n, rec, err := newRecordedNode(testNodeConfig(1, []uint64{1, 2, 3}, 1, &fakeSM{id: 1}), errBoom, nil)
	if err != nil {
		t.Fatalf("newNode: %v", err)
	}
	out, err := n.Process()
	if !errors.Is(err, errBoom) {
		t.Fatalf("Process: err = %v, want %v", err, errBoom)
	}
	if out.Messages != nil || out.Applied != nil || out.Events != nil {
		t.Errorf("Output = %+v, want empty", out)
	}
	if want := []readyStep{stepAppend}; !slices.Equal(*rec, want) {
		t.Errorf("record = %v, want %v", *rec, want)
	}
}

func TestSetHardStateErrorStopsReady(t *testing.T) { // O-3
	t.Parallel()
	errBoom := errors.New("boom")
	n, rec, err := newRecordedNode(testNodeConfig(1, []uint64{1, 2, 3}, 1, &fakeSM{id: 1}), nil, errBoom)
	if err != nil {
		t.Fatalf("newNode: %v", err)
	}
	out, err := n.Process()
	if !errors.Is(err, errBoom) {
		t.Fatalf("Process: err = %v, want %v", err, errBoom)
	}
	if out.Messages != nil || out.Applied != nil || out.Events != nil {
		t.Errorf("Output = %+v, want empty", out)
	}
	if want := []readyStep{stepAppend, stepSetHardState}; !slices.Equal(*rec, want) {
		t.Errorf("record = %v, want %v", *rec, want)
	}
}
