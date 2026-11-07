package server

import "time"

// tokenUnit は tokenBucket の 1 個の大きさ。
const tokenUnit = int64(time.Second)

// tokenBucket は 1 秒あたり commandRatePerSec 個まで通す。最初は満杯（DesignDoc §12.5）。
// 整数で数える（1 個 = 1e9 単位。経過したナノ秒 × commandRatePerSec 単位ずつ増え、commandRatePerSec × 1e9 で止まる）。
// run の goroutine だけが使う。
type tokenBucket struct {
	started bool
	tokens  int64
	last    time.Time
}

// allow は now に 1 個使えれば使って true を返す。
func (b *tokenBucket) allow(now time.Time) bool {
	const capacity = commandRatePerSec * tokenUnit
	if !b.started {
		b.started = true
		b.tokens = capacity
		b.last = now
	} else if elapsed := now.Sub(b.last); elapsed > 0 {
		// 時刻が戻ったときは last を動かさない（戻った分をもう一度数えないため）。
		b.last = now
		elapsed = min(elapsed, time.Second) // 1 秒で満杯に戻るので、切っても結果は同じ（あふれを防ぐ）
		b.tokens = min(capacity, b.tokens+elapsed.Nanoseconds()*commandRatePerSec)
	}
	if b.tokens < tokenUnit {
		return false
	}
	b.tokens -= tokenUnit
	return true
}
