package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// testStart は偽の時計の最初の時刻。
var testStart = time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)

// fakeClock は差し替えられる時刻と、手で発火させるティッカー（loopClock）。
// Advance は時刻を進めるだけで、ティッカーを発火させない。
type fakeClock struct {
	ns atomic.Int64

	mu      sync.Mutex
	tickers []*fakeTicker
	claimed map[*fakeTicker]bool
	changed chan struct{} // NewTicker のたびに閉じて作り直す
}

func newFakeClock(start time.Time) *fakeClock {
	c := &fakeClock{claimed: map[*fakeTicker]bool{}, changed: make(chan struct{})}
	c.ns.Store(start.UnixNano())
	return c
}

func (c *fakeClock) Now() time.Time          { return time.Unix(0, c.ns.Load()).UTC() }
func (c *fakeClock) Advance(d time.Duration) { c.ns.Add(int64(d)) }

// NewTicker は発火させるまで何も送らないティッカーを作る。
func (c *fakeClock) NewTicker(d time.Duration) loopTicker {
	t := &fakeTicker{c: c, d: d, ch: make(chan time.Time), stopped: make(chan struct{})}
	c.mu.Lock()
	c.tickers = append(c.tickers, t)
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
	return t
}

// waitTicker は、間隔 d のティッカーのうち、まだ waitTicker で返していない最初のものを返す
// （run が NewTicker(d) を呼ぶまで待つ）。ctx が終わったら nil。
func (c *fakeClock) waitTicker(ctx context.Context, d time.Duration) *fakeTicker {
	for {
		c.mu.Lock()
		for _, t := range c.tickers {
			if t.d == d && !c.claimed[t] {
				c.claimed[t] = true
				c.mu.Unlock()
				return t
			}
		}
		ch := c.changed
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil
		}
	}
}

// fakeTicker は fakeClock のティッカー。
type fakeTicker struct {
	c        *fakeClock
	d        time.Duration
	ch       chan time.Time // 容量 0
	stopOnce sync.Once
	stopped  chan struct{}
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               { t.stopOnce.Do(func() { close(t.stopped) }) }

// Fire は、いまの時刻を容量 0 のチャネルで送る（run が受け取るまでふさがる）。
func (t *fakeTicker) Fire(ctx context.Context) error {
	select {
	case t.ch <- t.c.Now():
		return nil
	case <-t.stopped:
		return errors.New("ticker stopped")
	case <-ctx.Done():
		return ctx.Err()
	}
}
