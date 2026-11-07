package raftcheck

import (
	"hash/fnv"
	"io"
	"log"
	"math/rand/v2"
	"slices"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
)

// T-003: ADR-0005 のパッチ（Config.RandIntn）の性質を確かめるテスト。
// raftcheck_test.go（T-002）で確かめていない、既定の乱数源への切り戻し・
// リセットのたびの呼び出し・ノードごとの独立・シード付きでの再現を扱う。
//
// どのテストも投票者が自分だけの RawNode を使い、Ready は処理しない。この状態の
// Candidate は自分への投票が Ready を経由するので Leader にならず、選挙の
// タイムアウトのたびに立候補し直して Term が 1 つ上がる。Term が変わる間隔の
// tick 数は ElectionTick ＋ 直前のリセットで RandIntn が返した値になる。

const patchElectionTick = 10

// maxTicksPerTerm は、Term が変わるのを待つ tick 数の上限。タイムアウトは
// [ElectionTick, 2×ElectionTick) なので、これを超えたら異常として止める。
const maxTicksPerTerm = 4 * patchElectionTick

// newSingleVoterRawNode は、投票者が自分（ID 1）だけの RawNode を作る。
func newSingleVoterRawNode(t *testing.T, randIntn func(n int) int) *raft.RawNode {
	t.Helper()
	storage := raft.NewMemoryStorage()
	if err := storage.ApplySnapshot(&pb.Snapshot{
		Metadata: &pb.SnapshotMetadata{ConfState: &pb.ConfState{Voters: []uint64{1}}},
	}); err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	rn, err := raft.NewRawNode(&raft.Config{
		ID:              1,
		ElectionTick:    patchElectionTick,
		HeartbeatTick:   1,
		Storage:         storage,
		MaxSizePerMsg:   1 << 20,
		MaxInflightMsgs: 256,
		Logger:          &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)},
		RandIntn:        randIntn,
	})
	if err != nil {
		t.Fatalf("NewRawNode: %v", err)
	}
	return rn
}

// termOf は、RawNode の現在の Term を返す。
func termOf(rn *raft.RawNode) uint64 {
	st := rn.BasicStatus()
	return st.HardState.GetTerm()
}

// ticksUntilTermChange は、Term が変わるまで Tick し、その回数を返す。
func ticksUntilTermChange(t *testing.T, rn *raft.RawNode) int {
	t.Helper()
	before := termOf(rn)
	for k := 1; k <= maxTicksPerTerm; k++ {
		rn.Tick()
		if termOf(rn) != before {
			return k
		}
	}
	t.Fatalf("term did not change from %d within %d ticks", before, maxTicksPerTerm)
	return 0
}

// termIntervals は、Term が count 回変わるまでの、それぞれの tick 数を返す。
func termIntervals(t *testing.T, rn *raft.RawNode, count int) []int {
	t.Helper()
	out := make([]int, 0, count)
	for range count {
		out = append(out, ticksUntilTermChange(t, rn))
	}
	return out
}

// fnv64a は、文字列の FNV-1a（64 ビット）を返す（ADR-0005・DesignDoc §9.1 の
// 乱数列の作り方と同じ）。
func fnv64a(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// seededRandIntn は、シードとノードの ID から作った PCG の IntN を返す。
func seededRandIntn(seed uint64, id string) func(n int) int {
	return rand.New(rand.NewPCG(seed, fnv64a("raft/"+id))).IntN
}

// countingRandIntn は、呼び出しを数え、値を列から順に返す関数を返す。
// 列を使い切ったら最後の値を返し続ける。
func countingRandIntn(seq []int) (fn func(n int) int, calls *int) {
	calls = new(int)
	fn = func(int) int {
		i := *calls
		*calls++
		if i >= len(seq) {
			return seq[len(seq)-1]
		}
		return seq[i]
	}
	return fn, calls
}

// TestNilRandIntnFallsBackToDefaultSource は、RandIntn が nil のときに上流の
// globalRand に切り戻り、選挙のタイムアウトが既定の範囲に入ることを確かめる。
func TestNilRandIntnFallsBackToDefaultSource(t *testing.T) {
	t.Parallel()
	for i := range 20 {
		rn := newSingleVoterRawNode(t, nil)
		if st := rn.BasicStatus().RaftState; st != raft.StateFollower {
			t.Fatalf("node %d: initial state=%v, want %v", i, st, raft.StateFollower)
		}
		k := 0
		for rn.BasicStatus().RaftState != raft.StateCandidate {
			k++
			if k > maxTicksPerTerm {
				t.Fatalf("node %d: did not become candidate within %d ticks", i, maxTicksPerTerm)
			}
			rn.Tick()
		}
		if k < patchElectionTick || k > 2*patchElectionTick-1 {
			t.Fatalf("node %d: became candidate after %d ticks, want [%d, %d]",
				i, k, patchElectionTick, 2*patchElectionTick-1)
		}
	}
}

// TestRandIntnIsConsultedOnEveryReset は、選挙のタイムアウトをリセットするたびに
// RandIntn が 1 回呼ばれ、その値がタイムアウトに使われることを確かめる。
func TestRandIntnIsConsultedOnEveryReset(t *testing.T) {
	t.Parallel()
	seq := []int{3, 0, 9, 5, 1}
	fn, calls := countingRandIntn(seq)
	rn := newSingleVoterRawNode(t, fn)
	if *calls != 1 {
		t.Fatalf("NewRawNode called RandIntn %d times, want 1", *calls)
	}
	if got := termOf(rn); got != 0 {
		t.Fatalf("initial term=%d, want 0", got)
	}

	want := []int{13, 10, 19, 15, 11}
	for i, w := range want {
		k := ticksUntilTermChange(t, rn)
		if got := termOf(rn); got != uint64(i+1) {
			t.Fatalf("after change %d: term=%d, want %d", i+1, got, i+1)
		}
		if k != w {
			t.Fatalf("term %d->%d took %d ticks, want %d", i, i+1, k, w)
		}
		if *calls != 1+(i+1) {
			t.Fatalf("after term %d: RandIntn called %d times, want %d", i+1, *calls, 1+(i+1))
		}
	}
}

// TestRandIntnIsPerNode は、RandIntn がノードごとに独立していて、パッケージ全体の
// 乱数源を共有していないことを確かめる。
func TestRandIntnIsPerNode(t *testing.T) {
	t.Parallel()
	seq := []int{4, 8, 1, 6, 2, 9, 0, 7, 3, 5}

	fnA, callsA := countingRandIntn(seq)
	fnB, callsB := countingRandIntn(seq)
	a := newSingleVoterRawNode(t, fnA)
	b := newSingleVoterRawNode(t, fnB)

	beforeA, beforeB := *callsA, *callsB
	for range 50 {
		a.Tick()
	}
	if *callsB != beforeB {
		t.Fatalf("ticking A called B's RandIntn %d times", *callsB-beforeB)
	}
	if *callsA == beforeA {
		t.Fatalf("ticking A 50 times did not call A's RandIntn")
	}
	beforeA, beforeB = *callsA, *callsB
	for range 50 {
		b.Tick()
	}
	if *callsA != beforeA {
		t.Fatalf("ticking B called A's RandIntn %d times", *callsA-beforeA)
	}
	if *callsB == beforeB {
		t.Fatalf("ticking B 50 times did not call B's RandIntn")
	}

	// A を単独で動かしたときの Term の間隔の列。
	const changes = 8
	soloFn, _ := countingRandIntn(seq)
	solo := termIntervals(t, newSingleVoterRawNode(t, soloFn), changes)

	// A と B（B は別の列）を交互に Tick したときの A の列。
	pairFnA, _ := countingRandIntn(seq)
	pairFnB, _ := countingRandIntn([]int{9, 9, 0, 0, 5})
	pairA := newSingleVoterRawNode(t, pairFnA)
	pairB := newSingleVoterRawNode(t, pairFnB)
	interleaved := make([]int, 0, changes)
	for range changes {
		before := termOf(pairA)
		k := 0
		for termOf(pairA) == before {
			k++
			if k > maxTicksPerTerm {
				t.Fatalf("interleaved: term did not change within %d ticks", maxTicksPerTerm)
			}
			pairA.Tick()
			pairB.Tick()
		}
		interleaved = append(interleaved, k)
	}
	if !slices.Equal(solo, interleaved) {
		t.Fatalf("A's intervals differ: solo=%v interleaved=%v", solo, interleaved)
	}
}

// TestSeededRandIntnIsReproducible は、シード付きの PCG を渡すと、同じシードで
// 同じ展開が再現され、シードが違えば展開が変わることを確かめる。
func TestSeededRandIntnIsReproducible(t *testing.T) {
	t.Parallel()
	const changes = 10
	run := func(seed uint64) []int {
		return termIntervals(t, newSingleVoterRawNode(t, seededRandIntn(seed, "1")), changes)
	}

	first := run(1)
	second := run(1)
	if !slices.Equal(first, second) {
		t.Fatalf("seed 1 is not reproducible: %v vs %v", first, second)
	}
	for i, k := range first {
		if k < patchElectionTick || k >= 2*patchElectionTick {
			t.Fatalf("interval %d = %d, want [%d, %d)", i, k, patchElectionTick, 2*patchElectionTick)
		}
	}
	other := run(2)
	if slices.Equal(first, other) {
		t.Fatalf("seeds 1 and 2 produced the same intervals: %v", first)
	}
	t.Logf("seed 1: %v / seed 2: %v", first, other)
}
