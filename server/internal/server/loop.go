package server

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// セッションループの値。ゲームの数値ではなく、プロトコルと設計書が決めた値なのでコードに置く。
const (
	stepInterval      = 50 * time.Millisecond  // シミュレーションを進める間隔（DesignDoc §12.4）
	flushInterval     = 100 * time.Millisecond // イベントをまとめて送る間隔（プロトコル §5）
	heartbeatInterval = time.Second            // イベントがなくても events を送る間隔（プロトコル §5）
	maxStepAdvance    = sim.Second             // 1 回のステップで進めるゲーム内時間の上限（DesignDoc §12.4）
	skipHorizon       = 30 * sim.Second        // cmd.game.skip で進める上限（プロトコル §4）
	pendingEventLimit = 1000                   // 送れずに溜まったイベントの上限。超えたら捨てて snapshot（DesignDoc §12.4）
	eventsHighWater   = 64                     // 送信キュー（容量 256）にこの数以上あれば events を積まない
	commandRatePerSec = 30                     // 1 セッションのコマンドの上限（DesignDoc §12.5）
	demoSpeedPermille = 8000                   // デモ操作が有効なセッションで足す 8 倍速（プロトコル §4）
)

const (
	// maxElapsed は advance が数える実時間の上限（掛け算のあふれを防ぐ）。
	maxElapsed = time.Minute
	// remUnit は advance の余りの単位（ナノ秒 × 千分率で 1 ミリ秒）。
	remUnit = int64(time.Millisecond) * 1000
)

// advance は、実時間の経過 elapsed と速度 speedPermille（1000 = 1 倍）から、
// 進めるゲーム内時間と、次に持ち越す余りを返す。
// 余り rem の単位は「ナノ秒 × 千分率」で、0 以上 1e9 未満。
func advance(elapsed time.Duration, speedPermille, rem int64) (adv sim.SimTime, nextRem int64) {
	if elapsed <= 0 {
		return 0, rem
	}
	elapsed = min(elapsed, maxElapsed)
	num := elapsed.Nanoseconds()*speedPermille + rem
	adv, nextRem = sim.SimTime(num/remUnit), num%remUnit
	if adv > maxStepAdvance {
		return maxStepAdvance, 0 // 上限で切ったら余りは捨てる
	}
	return adv, nextRem
}

// flushAction はまとめ送りで何をするか。
type flushAction int

const (
	flushNone     flushAction = iota // 何も送らない
	flushHold                        // 送らずに保留する（接続がない・snapshot を送っていない・キューが詰まっている）
	flushEvents                      // events を送る
	flushSnapshot                    // 保留を捨てて snapshot を送る
)

// flushInput は decideFlush の入力。
type flushInput struct {
	connected    bool
	snapshotSent bool
	queued       int // wsConn.queued()
	highWater    int // eventsHighWater
	pending      int // len(pending)
	needSnapshot bool
	force        bool // コマンドの後・pause・speed・skip の後・ラウンドの終わり
	sinceLast    time.Duration
}

// decideFlush はまとめ送りで何をするかを決める。
func decideFlush(in flushInput) flushAction {
	switch {
	case !in.connected || !in.snapshotSent:
		return flushHold
	case in.queued >= in.highWater:
		return flushHold
	case in.needSnapshot:
		return flushSnapshot
	case in.pending > 0 || in.force || in.sinceLast >= heartbeatInterval:
		return flushEvents
	default:
		return flushNone
	}
}

// step はステップ（stepInterval ごと）。速度に応じて Engine を進める。
// broken・一時停止中・航行中でない（Intro・Ended）なら進めない。
func (s *session) step(now time.Time) {
	elapsed := now.Sub(s.lastStep)
	s.lastStep = now
	if s.broken {
		return
	}
	s.guard("step", func() {
		if s.stepHook != nil {
			s.stepHook()
		}
		if s.pause.isPaused() || s.engine.Phase() != sim.PhaseSailing {
			s.rem = 0
			return
		}
		var adv sim.SimTime
		adv, s.rem = advance(elapsed, s.speedPermille, s.rem)
		if adv > 0 {
			s.runTo(s.engine.Now() + adv)
		}
	})
}

// flushTick はまとめ送り（flushInterval ごと）。
func (s *session) flushTick(now time.Time) {
	if s.broken {
		return
	}
	s.guard("flush", func() { s.flush(false, now) })
}

// flushNow は、いまの時刻でまとめ送りをする。force なら、イベントがなくても events を送る
// （コマンドの後・pause・speed・skip の後・ラウンドの終わり）。
func (s *session) flushNow(force bool) { s.flush(force, s.wall.Now()) }

// flush は DrainEvents → notice の差し込みと seq の振り直し（noticer）→ 保留 → decideFlush → events か snapshot。
func (s *session) flush(force bool, now time.Time) {
	if s.broken {
		return
	}
	s.pending = append(s.pending, s.notices.expand(s.engine.DrainEvents())...)
	if n := len(s.pending); n > s.pendingLimit {
		s.pending = nil
		s.needSnapshot = true
		s.log.Info("pending events dropped", "dropped", n)
	}
	queued := 0
	if s.conn != nil {
		queued = s.conn.queued()
	}
	switch decideFlush(flushInput{
		connected:    s.conn != nil,
		snapshotSent: s.snapshotSent,
		queued:       queued,
		highWater:    s.highWater,
		pending:      len(s.pending),
		needSnapshot: s.needSnapshot,
		force:        force,
		sinceLast:    now.Sub(s.lastEventsAt),
	}) {
	case flushEvents:
		s.sendEvents()
	case flushSnapshot:
		s.sendSnapshot()
	case flushHold, flushNone:
	}
}

// sendEvents は保留のイベントを events 1 つで送る（Events は空でも []）。
func (s *session) sendEvents() {
	raw := make([]json.RawMessage, len(s.pending))
	for i, ev := range s.pending {
		b, err := json.Marshal(ev)
		if err != nil {
			panic(fmt.Sprintf("server: marshal event %d: %v", ev.Seq, err))
		}
		raw[i] = b
	}
	s.send(protocol.MessageTypeEvents, protocol.EventsPayload{Clock: s.clock(), Events: raw})
	s.pending = s.pending[:0]
	s.lastEventsAt = s.wall.Now()
}

// runTo は RunUntil(t)。ラウンドが終わったら、round.result より前に events を送る（phase.changed（ended）が先に届く）。
func (s *session) runTo(t sim.SimTime) {
	s.engine.RunUntil(t)
	if s.engine.Phase() == sim.PhaseEnded && !s.resultSent {
		s.flushNow(true)
	}
	s.checkEnded()
}
