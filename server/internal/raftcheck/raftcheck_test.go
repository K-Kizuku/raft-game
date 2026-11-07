package raftcheck

import (
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
)

// TestRawNodeUsesInjectedRandIntn は、Config.RandIntn（ADR-0005 のパッチ）に渡した
// 関数が選挙のタイムアウトの乱数として使われることを確かめる。
//
// 投票者が自分だけの RawNode を作り、RandIntn が固定の値 extra を返すようにすると、
// 選挙のタイムアウトは ElectionTick+extra になる。ElectionTick+extra-1 回の Tick では
// Follower のままで、ElectionTick+extra 回目の Tick で選挙を始めて Candidate になる
// （v3.7 では自分への投票も Ready を経由するので、Ready を処理しないこのテストでは
// Leader にはならない）。
func TestRawNodeUsesInjectedRandIntn(t *testing.T) {
	const electionTick = 10

	for _, extra := range []int{0, 3, 7} {
		calls := 0
		var gotN []int
		randIntn := func(n int) int {
			calls++
			gotN = append(gotN, n)
			return extra
		}

		storage := raft.NewMemoryStorage()
		if err := storage.ApplySnapshot(&pb.Snapshot{
			Metadata: &pb.SnapshotMetadata{ConfState: &pb.ConfState{Voters: []uint64{1}}},
		}); err != nil {
			t.Fatalf("extra=%d: ApplySnapshot: %v", extra, err)
		}

		rn, err := raft.NewRawNode(&raft.Config{
			ID:              1,
			ElectionTick:    electionTick,
			HeartbeatTick:   1,
			Storage:         storage,
			MaxSizePerMsg:   1 << 20,
			MaxInflightMsgs: 256,
			RandIntn:        randIntn,
		})
		if err != nil {
			t.Fatalf("extra=%d: NewRawNode: %v", extra, err)
		}

		if calls == 0 {
			t.Fatalf("extra=%d: RandIntn was not called by NewRawNode", extra)
		}
		for _, n := range gotN {
			if n != electionTick {
				t.Fatalf("extra=%d: RandIntn called with n=%d, want %d", extra, n, electionTick)
			}
		}

		timeout := electionTick + extra
		for i := 1; i < timeout; i++ {
			rn.Tick()
			if st := rn.BasicStatus().RaftState; st != raft.StateFollower {
				t.Fatalf("extra=%d: after %d ticks state=%v, want %v", extra, i, st, raft.StateFollower)
			}
		}
		callsBefore := calls
		rn.Tick()
		if st := rn.BasicStatus().RaftState; st != raft.StateCandidate {
			t.Fatalf("extra=%d: after %d ticks state=%v, want %v", extra, timeout, st, raft.StateCandidate)
		}
		if calls <= callsBefore {
			t.Fatalf("extra=%d: RandIntn was not called during the election (calls=%d)", extra, calls)
		}
	}
}
