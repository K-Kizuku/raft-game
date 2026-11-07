package server

import (
	"testing"
	"time"
)

// LP-5: tokenBucket。
func TestTokenBucket(t *testing.T) {
	t.Parallel()
	var b tokenBucket
	now := testStart
	for i := range 30 {
		if !b.allow(now) {
			t.Fatalf("allow #%d = false", i+1)
		}
	}
	if b.allow(now) {
		t.Fatal("allow #31 = true")
	}

	// 1/30 秒（33,333,334ns）で 1 回だけ。
	now = now.Add(33_333_334 * time.Nanosecond)
	if !b.allow(now) {
		t.Fatal("allow after 1/30s = false")
	}
	if b.allow(now) {
		t.Fatal("allow twice after 1/30s = true")
	}

	// 1 秒後に 30 回（満杯で止まる）。
	now = now.Add(time.Second)
	for i := range 30 {
		if !b.allow(now) {
			t.Fatalf("allow after 1s #%d = false", i+1)
		}
	}
	if b.allow(now) {
		t.Fatal("allow #31 after 1s = true")
	}

	// 時刻が戻っても増えない。戻って進んだ分を二重に数えない。
	back := now.Add(-time.Hour)
	if b.allow(back) {
		t.Fatal("allow after the clock went back = true")
	}
	if b.allow(now) {
		t.Fatal("allow at the same time after the clock went back = true")
	}

	// 長い間があいても 30 回まで。
	now = now.Add(24 * time.Hour)
	n := 0
	for b.allow(now) {
		n++
	}
	if n != 30 {
		t.Fatalf("after a day = %d, want 30", n)
	}
}
