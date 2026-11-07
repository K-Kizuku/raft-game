package server

import (
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// LP-1: advance の表。
func TestAdvance(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		elapsed time.Duration
		speed   int64
		rem     int64
		adv     sim.SimTime
		nextRem int64
	}{
		{"1 倍 50ms", 50 * time.Millisecond, 1000, 0, 50, 0},
		{"2 倍 50ms", 50 * time.Millisecond, 2000, 0, 100, 0},
		{"4 倍 50ms", 50 * time.Millisecond, 4000, 0, 200, 0},
		{"8 倍 50ms", 50 * time.Millisecond, 8000, 0, 400, 0},
		{"0.5 倍 50ms", 50 * time.Millisecond, 500, 0, 25, 0},
		{"4 倍 5s は上限", 5 * time.Second, 4000, 0, 1000, 0},
		{"上限で余りを捨てる", 5 * time.Second, 4000, 999_999_999, 1000, 0},
		{"elapsed 0", 0, 1000, 123, 0, 123},
		{"elapsed 負", -time.Second, 1000, 456, 0, 456},
		{"1 時間（あふれない）", time.Hour, 8000, 0, 1000, 0},
		{"余りを足す", time.Millisecond, 500, 500_000_000, 1, 0},
	}
	for _, tt := range tests {
		adv, rem := advance(tt.elapsed, tt.speed, tt.rem)
		if adv != tt.adv || rem != tt.nextRem {
			t.Errorf("%s: advance(%v, %d, %d) = (%d, %d), want (%d, %d)",
				tt.name, tt.elapsed, tt.speed, tt.rem, adv, rem, tt.adv, tt.nextRem)
		}
	}

	// 0.5 倍で 1ms を 4 回 → 0・1・0・1（余りを渡し続ける。Issue の例の「0・1・1」は、実装仕様の手順 3
	// （切り捨てと余りの持ち越し）では 0・1・0 になる。1.5ms の実時間で 2ms 進むと「実時間の半分」を超えるため）。
	var rem int64
	var got []sim.SimTime
	for range 4 {
		var adv sim.SimTime
		adv, rem = advance(time.Millisecond, 500, rem)
		got = append(got, adv)
	}
	if got[0] != 0 || got[1] != 1 || got[2] != 0 || got[3] != 1 || rem != 0 {
		t.Errorf("0.5 倍 1ms × 4 = %v (rem %d), want [0 1 0 1] (rem 0)", got, rem)
	}
	// 長い目で見て実時間の半分だけ進む。
	var total sim.SimTime
	rem = 0
	for range 1000 {
		var adv sim.SimTime
		adv, rem = advance(3*time.Millisecond, 500, rem)
		total += adv
	}
	if total != 1500 {
		t.Errorf("0.5 倍 3ms × 1000 = %d, want 1500", total)
	}
}

// LP-6: decideFlush の表。
func TestDecideFlush(t *testing.T) {
	t.Parallel()
	base := flushInput{connected: true, snapshotSent: true, queued: 0, highWater: eventsHighWater}
	with := func(edit func(*flushInput)) flushInput {
		in := base
		edit(&in)
		return in
	}
	tests := []struct {
		name string
		in   flushInput
		want flushAction
	}{
		{"1: 接続がない", with(func(in *flushInput) { in.connected = false; in.pending = 3 }), flushHold},
		{"1: snapshot を送っていない", with(func(in *flushInput) { in.snapshotSent = false; in.pending = 3 }), flushHold},
		{"1: force でも snapshot を送っていなければ保留", with(func(in *flushInput) { in.snapshotSent = false; in.force = true }), flushHold},
		{"2: キューが詰まっている", with(func(in *flushInput) { in.queued = 64; in.pending = 3 }), flushHold},
		{"2: needSnapshot でもキューが詰まっていれば保留", with(func(in *flushInput) { in.queued = 100; in.needSnapshot = true }), flushHold},
		{"2: キューが 63", with(func(in *flushInput) { in.queued = 63; in.pending = 3 }), flushEvents},
		{"3: needSnapshot", with(func(in *flushInput) { in.needSnapshot = true; in.pending = 3 }), flushSnapshot},
		{"4: pending", with(func(in *flushInput) { in.pending = 1 }), flushEvents},
		{"4: force", with(func(in *flushInput) { in.force = true }), flushEvents},
		{"4: sinceLast 1s", with(func(in *flushInput) { in.sinceLast = time.Second }), flushEvents},
		{"5: sinceLast 999ms", with(func(in *flushInput) { in.sinceLast = 999 * time.Millisecond }), flushNone},
		{"5: 何もない", base, flushNone},
	}
	for _, tt := range tests {
		if got := decideFlush(tt.in); got != tt.want {
			t.Errorf("%s: decideFlush(%+v) = %d, want %d", tt.name, tt.in, got, tt.want)
		}
	}
}
