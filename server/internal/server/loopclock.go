package server

import "time"

// loopClock はセッションループが使う実時間。テストで差し替える（DesignDoc §12.4）。
//
// セッションループ（ステップ・まとめ送り・レート制限）は、実時間の時刻とティッカーをすべてここから取り、
// time.Now・time.NewTicker を直接呼ばない。
type loopClock interface {
	Now() time.Time
	NewTicker(d time.Duration) loopTicker
}

// loopTicker は loopClock のティッカー。
type loopTicker interface {
	C() <-chan time.Time
	Stop()
}

// realClock は time.Now と time.NewTicker を使う。
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTicker(d time.Duration) loopTicker { return realTicker{time.NewTicker(d)} }

// realTicker は *time.Ticker を loopTicker にする。
type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }
