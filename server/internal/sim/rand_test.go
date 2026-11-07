package sim

import (
	"reflect"
	"testing"
)

// R-1
func TestFNV64a(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want uint64
	}{
		{"", 0xcbf29ce484222325},
		{"a", 0xaf63dc4c8601ec8c},
		{"requests", 0x7166d3e80f26d1f3},
		{"raft/1", 0xb32ef2591a8ccc06},
	}
	for _, tt := range tests {
		if got := fnv64a(tt.in); got != tt.want {
			t.Errorf("fnv64a(%q) = %#x, want %#x", tt.in, got, tt.want)
		}
	}
}

func draw(seed uint64, name string, n int) []uint64 {
	r := Sub(seed, name)
	out := make([]uint64, n)
	for i := range out {
		out[i] = r.Uint64()
	}
	return out
}

// R-2
func TestSubSameAndDifferent(t *testing.T) {
	t.Parallel()
	const seed = 20261101
	if a, b := draw(seed, StreamRequests, 100), draw(seed, StreamRequests, 100); !reflect.DeepEqual(a, b) {
		t.Fatal("同じ (seed, name) で列が一致しない")
	}
	if draw(seed, StreamRequests, 1)[0] == draw(seed, StreamEvents, 1)[0] {
		t.Fatal("名前が違うのに最初の値が同じ")
	}
	if draw(seed, StreamRequests, 1)[0] == draw(seed+1, StreamRequests, 1)[0] {
		t.Fatal("シードが違うのに最初の値が同じ")
	}
	// 空の名前でも動く。
	_ = draw(seed, "", 1)
}

// R-3
func TestSubIndependent(t *testing.T) {
	t.Parallel()
	const seed = 7
	want := draw(seed, StreamRequests, 100)
	d := Sub(seed, StreamDrift)
	for range 50 {
		d.Uint64()
	}
	if got := draw(seed, StreamRequests, 100); !reflect.DeepEqual(got, want) {
		t.Fatal("別の用途の列を引くと requests の列が変わった")
	}
}

// R-4
func TestSubGolden(t *testing.T) {
	t.Parallel()
	const seed = 20261101
	wantU := []uint64{0xfec5021900a45819, 0x54cf078d47fbb256, 0xafba86259176a671}
	if got := draw(seed, StreamRequests, 3); !reflect.DeepEqual(got, wantU) {
		t.Errorf("Sub(%d, requests) = %#x, want %#x", seed, got, wantU)
	}
	wantN := map[uint64][]int{
		1: {6, 6, 7, 9, 1},
		2: {14, 8, 9, 10, 7},
		3: {7, 5, 13, 15, 8},
	}
	for id := uint64(1); id <= 3; id++ {
		f := RaftRandIntn(seed, id)
		got := make([]int, 5)
		for i := range got {
			got[i] = f(16)
		}
		if !reflect.DeepEqual(got, wantN[id]) {
			t.Errorf("RaftRandIntn(%d, %d)(16) = %v, want %v", seed, id, got, wantN[id])
		}
	}
}

// R-5
func TestRaftRandIntn(t *testing.T) {
	t.Parallel()
	if got := RaftStreamName(3); got != "raft/3" {
		t.Fatalf("RaftStreamName(3) = %q", got)
	}
	const seed = 99
	f := RaftRandIntn(seed, 1)
	g := Sub(seed, "raft/1")
	for i := range 100 {
		if a, b := f(16), g.IntN(16); a != b {
			t.Fatalf("%d 回目: RaftRandIntn = %d, Sub.IntN = %d", i, a, b)
		}
	}
	for _, n := range []int{2, 16, 240} {
		h := RaftRandIntn(seed, 2)
		for range 1000 {
			if v := h(n); v < 0 || v >= n {
				t.Fatalf("n=%d: %d が範囲外", n, v)
			}
		}
	}
}
