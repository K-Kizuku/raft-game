package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// inboxSize は、セッションが受ける電文のキューの容量。
const inboxSize = 64

// inbound は、接続 c から届いた電文 1 つ。
type inbound struct {
	c   *wsConn
	env protocol.Envelope
}

// attachReq は、接続をセッションにつなぐ要求。snapshot なら、つないだ直後に snapshot を送る。
type attachReq struct {
	c        *wsConn
	snapshot bool
}

// sessionParams はセッションを作る値。
type sessionParams struct {
	id       string
	st       *stage.Stage
	seed     uint64
	demo     bool
	restored bool
	paused   bool
	mode     Mode
	build    string
	engine   *sim.Engine
	log      *slog.Logger
	now      func() time.Time
	hook     func(protocol.Envelope) // テスト用。handle の最初に呼ぶ
	onBroken func()                  // Engine が panic したとき（run の goroutine から呼ぶ）
}

// session は 1 ゲーム。Engine に触れるのは run の goroutine だけ（DesignDoc §12.4）。
type session struct {
	id, stageID string
	st          *stage.Stage
	durationMs  sim.SimTime
	seed        uint64
	demo        bool
	restored    bool
	paused      bool    // T-302 は restore で true にするだけ。意味を持たせるのは T-303
	speed       float64 // 1。T-303 が変える
	mode        Mode
	build       string
	engine      *sim.Engine // run の goroutine だけが触れる
	conn        *wsConn     // run の goroutine だけが触れる
	broken      bool        // Engine が panic した
	resultSent  bool        // round.result を一度でも送った（run の goroutine だけ）
	resultFor   *wsConn     // snapshot の後に round.result を送る接続（つなぎ直した接続。run の goroutine だけ）

	log      *slog.Logger
	now      func() time.Time
	hook     func(protocol.Envelope)
	onBroken func()

	inbox  chan inbound
	attach chan attachReq
	detach chan *wsConn
	call   chan func() // run の goroutine で関数を実行する（テスト用。T-303 も使ってよい）

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // run が終わったら閉じる

	lastSeen  atomic.Int64 // 片付け用。最後に接続が切れた（または作った）時刻の UnixNano
	connected atomic.Bool  // いま接続がある
}

// newSession はセッションを作る（goroutine はまだ始めない）。
func newSession(parent context.Context, p sessionParams) *session {
	ctx, cancel := context.WithCancel(parent)
	s := &session{
		id:         p.id,
		stageID:    p.st.ID,
		st:         p.st,
		durationMs: sim.SimTime(p.st.DurationMs),
		seed:       p.seed,
		demo:       p.demo,
		restored:   p.restored,
		paused:     p.paused,
		speed:      1,
		mode:       p.mode,
		build:      p.build,
		engine:     p.engine,
		log:        p.log.With("session", p.id),
		now:        p.now,
		hook:       p.hook,
		onBroken:   p.onBroken,
		inbox:      make(chan inbound, inboxSize),
		attach:     make(chan attachReq),
		detach:     make(chan *wsConn),
		call:       make(chan func()),
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	s.lastSeen.Store(p.now().UnixNano())
	return s
}

// start は run の goroutine を始める。
func (s *session) start() { go s.run(s.ctx) }

// stop は run の goroutine を止めて、終わるのを待つ。
func (s *session) stop() {
	s.cancel()
	<-s.done
}

// run は select で ctx・attach・detach・inbox を待つ。T-303 がここにステップと送信の ticker を足す。
func (s *session) run(ctx context.Context) {
	defer close(s.done)
	for {
		select {
		case <-ctx.Done():
			return
		case a := <-s.attach:
			s.onAttach(a)
		case c := <-s.detach:
			if c == s.conn {
				s.conn = nil
				s.connected.Store(false)
				s.lastSeen.Store(s.now().UnixNano())
				s.onDetach()
			}
		case m := <-s.inbox:
			if m.c != s.conn {
				continue // いまの接続でない接続からの電文は捨てる
			}
			s.handle(m)
		case f := <-s.call:
			f()
		}
	}
}

// attachConn は接続 c をセッションにつなぐ（run が受け取るまで待つ）。セッションが止まっていれば false。
// 受け取った後に届く c の電文は、つないだ後に処理される。
func (s *session) attachConn(c *wsConn, snapshot bool) bool {
	select {
	case s.attach <- attachReq{c: c, snapshot: snapshot}:
		return true
	case <-s.done:
		return false
	}
}

// detachConn は接続 c が切れたことを知らせる。
func (s *session) detachConn(c *wsConn) {
	select {
	case s.detach <- c:
	case <-s.done:
	}
}

// deliver は接続 c の電文をセッションに渡す。セッションが止まっていれば false。
func (s *session) deliver(c *wsConn, env protocol.Envelope) bool {
	select {
	case s.inbox <- inbound{c: c, env: env}:
		return true
	case <-s.done:
		return false
	}
}

// do は f を run の goroutine で実行し、終わるのを待つ。セッションが止まっていれば false。
func (s *session) do(f func()) bool {
	finished := make(chan struct{})
	select {
	case s.call <- func() { defer close(finished); f() }:
	case <-s.done:
		return false
	}
	select {
	case <-finished:
		return true
	case <-s.done:
		return false
	}
}

// onAttach は新しい接続をつなぐ。古い接続には replaced を送って閉じる。
func (s *session) onAttach(a attachReq) {
	if old := s.conn; old != nil && old != a.c {
		old.fatal(&fatalError{code: codeReplaced, message: "replaced by a new connection", close: closeNormal})
	}
	s.conn = a.c
	s.connected.Store(true)
	s.lastSeen.Store(s.now().UnixNano())
	if s.broken {
		s.closeBroken()
		return
	}
	s.resultFor = nil
	s.guard("attach", func() {
		if s.engine.Phase() == sim.PhaseEnded {
			s.resultFor = a.c // snapshot の後に round.result をもう一度送る
		}
		if a.snapshot {
			s.sendSnapshot()
		}
	})
}

// onDetach は接続が切れたとき。T-302 では何もしない（T-303 が自動一時停止を足す）。
func (s *session) onDetach() {}

// closeBroken は、Engine が panic したセッションのいまの接続に internal_error を送って閉じる。
func (s *session) closeBroken() {
	if s.conn != nil {
		s.conn.fatal(&fatalError{code: codeInternalError, message: "internal error", close: closeInternalError})
		s.conn = nil
		s.connected.Store(false)
	}
}

// guard は f を実行し、panic を回復する。回復したら broken にし、いまの接続を internal_error で閉じる。
func (s *session) guard(what string, f func()) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		s.log.Error("session panic", "where", what, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		s.broken = true
		s.closeBroken()
		if s.onBroken != nil {
			s.onBroken()
		}
	}()
	f()
}

// handle は電文 1 つを処理する（panic は回復する）。cmdId が空でない電文には、hello・ping を除いて
// 必ず ack を 1 つ返す。internal.* は、接続の受け口で処理してからここに来る。
func (s *session) handle(m inbound) {
	if s.broken {
		s.closeBroken()
		return
	}
	s.guard(string(m.env.Type), func() {
		if s.hook != nil {
			s.hook(m.env)
		}
		s.handleEnvelope(m.env)
	})
}

func (s *session) handleEnvelope(env protocol.Envelope) {
	switch env.Type {
	case protocol.MessageTypeHello:
		s.sendSnapshot()
		return
	case protocol.MessageTypeCmdSync:
		if env.CmdID != "" {
			s.send(protocol.MessageTypeAck, protocol.Ack{CmdID: env.CmdID, OK: true, T: int64(s.engine.Now())})
		}
		s.sendSnapshot()
		return
	case protocol.MessageTypePing:
		var p protocol.PingPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			s.sendError(codeBadRequest, "bad ping payload")
			return
		}
		s.send(protocol.MessageTypePong, protocol.PongPayload{ClientTime: p.ClientTime, SimTime: int64(s.engine.Now())})
		return
	case protocol.MessageTypeCmdGamePause, protocol.MessageTypeCmdGameSpeed, protocol.MessageTypeCmdGameSkip, // T-303
		protocol.MessageTypeSubEngineer,  // T-407
		protocol.MessageTypeDemoTrigger,  // T-305
		protocol.MessageTypeCmdRelayLoad: // P1
		s.reject(env, "not implemented")
		return
	}

	cmd, err := simconv.DecodeEngineCommand(env.Type, env.Payload)
	switch {
	case errors.Is(err, simconv.ErrNotEngineCommand):
		s.reject(env, "unknown type")
		return
	case err != nil: // ErrBadPayload
		s.reject(env, "bad payload")
		return
	case env.CmdID == "":
		// Durable Object は cmdId で ack と組にして入力ログに記録するので、記録されない操作を Engine に入れない。
		s.sendError(codeBadRequest, "cmdId is required")
		return
	}
	ack := s.engine.Apply(cmd)
	s.send(protocol.MessageTypeAck, simconv.AckFromSim(env.CmdID, ack))
	s.flush()
	s.checkEnded()
}

// reject は、cmdId があれば ack（ok: false、bad_request）、なければ error（bad_request、fatal でない）を返す。
func (s *session) reject(env protocol.Envelope, message string) {
	if env.CmdID == "" {
		s.sendError(codeBadRequest, message)
		return
	}
	s.send(protocol.MessageTypeAck, protocol.Ack{
		CmdID:   env.CmdID,
		OK:      false,
		T:       int64(s.engine.Now()),
		Code:    protocol.AckCodeBadRequest,
		Message: message,
	})
}

// flush は DrainEvents → events を 1 つ送る（イベントがなければ送らない）。T-303 が送る時機を変える。
// 接続がなければイベントは捨てる（つなぎ直した接続は snapshot から始める）。
func (s *session) flush() {
	evs := s.engine.DrainEvents()
	if len(evs) == 0 {
		return
	}
	raw := make([]json.RawMessage, len(evs))
	for i, ev := range evs {
		b, err := json.Marshal(ev)
		if err != nil {
			panic(fmt.Sprintf("server: marshal event %d: %v", ev.Seq, err))
		}
		raw[i] = b
	}
	s.send(protocol.MessageTypeEvents, protocol.EventsPayload{Clock: s.clock(), Events: raw})
}

// advanceTo は RunUntil(t) → checkEnded（panic は回復する）。T-302 では呼ぶ場所がない（T-303 のステップが呼ぶ）。
func (s *session) advanceTo(t sim.SimTime) {
	if s.broken {
		return
	}
	s.guard("advance", func() {
		s.engine.RunUntil(t)
		s.checkEnded()
	})
}

// checkEnded は、Phase() == PhaseEnded になったら、いまの接続に round.result（single は internal.result も）を
// 1 回だけ送る。送る前に、残っているイベント（phase.changed など）を flush する。
func (s *session) checkEnded() {
	if s.resultSent || s.engine.Phase() != sim.PhaseEnded {
		return
	}
	s.flush()
	s.sendResult()
}

// sendResult は round.result（single は続けて internal.result）を送る。
func (s *session) sendResult() {
	s.resultSent = true
	r := s.buildResult()
	s.send(protocol.MessageTypeRoundResult, r)
	if s.mode == ModeSingle {
		s.send(protocol.MessageTypeInternalResult, r)
	}
}

// sendSnapshot は snapshot を送る。つなぎ直した接続で、ラウンドが終わっていれば、続けて round.result も送る。
func (s *session) sendSnapshot() {
	if s.conn == nil {
		return
	}
	s.send(protocol.MessageTypeSnapshot, s.buildSnapshot())
	if s.resultFor != nil && s.resultFor == s.conn {
		s.resultFor = nil
		s.sendResult()
	}
}

// clock は、いまの時計（プロトコル §7）。
func (s *session) clock() protocol.Clock {
	return protocol.Clock{T: int64(s.engine.Now()), Speed: s.speed, Paused: s.paused, EndsAt: int64(s.durationMs)}
}

// send は、いまの接続に電文を送る。接続がなければ捨てる。
func (s *session) send(typ protocol.MessageType, payload any) {
	if s.conn != nil {
		s.conn.send(typ, payload)
	}
}

// sendError は、いまの接続に fatal でない error を送る。
func (s *session) sendError(code, message string) {
	if s.conn != nil {
		s.conn.sendError(code, message)
	}
}
