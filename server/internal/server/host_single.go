package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// restoreMessageMax は restore_failed の message の上限（バイト）。
const restoreMessageMax = 512

// singleHost は single モードの、プロセスに 1 つのセッションの枠。最初の internal.init・internal.restore で
// セッションを作り、以後はそのセッションだけを持つ（Engine が panic しても枠に残す。DO が復元か終了を決める）。
type singleHost struct {
	srv  *Server
	mu   sync.Mutex
	sess *session
}

// stop は枠のセッションの goroutine を止める。
func (h *singleHost) stop() {
	h.mu.Lock()
	sess := h.sess
	h.mu.Unlock()
	if sess != nil {
		sess.stop()
	}
}

// serveWS は GET /ws（メソッドと Upgrade は検査済み）。
func (h *singleHost) serveWS(w http.ResponseWriter, r *http.Request) {
	srv := h.srv
	c, err := acceptWS(w, r, singleReadLimit)
	if err != nil {
		srv.log.Debug("websocket accept", "err", err.Error())
		return
	}
	wc := newWSConn(c, srv.log)
	srv.track(wc)
	defer srv.untrack(wc)
	defer wc.finish()

	sess := h.waitInit(wc)
	if sess == nil {
		return
	}
	defer sess.detachConn(wc)
	h.readLoop(wc, sess)
}

// waitInit は接続の最初の電文を待つ（プロトコル §9）。initTimeout のうちに internal.init か internal.restore が
// 来なければ not_initialized（fatal）。それより前の internal.ping には internal.pong を返す（数えない）。
// ほかの電文が来たら not_initialized（fatal）。成功したら、つないだセッションを返す。
func (h *singleHost) waitInit(wc *wsConn) *session {
	const (
		waiting = iota
		started
		timedOut
	)
	var state atomic.Int32
	notInit := func(msg string) *fatalError {
		return &fatalError{code: codeNotInitialized, message: msg, close: closePolicy}
	}
	timer := time.AfterFunc(h.srv.initTimeout, func() {
		if state.CompareAndSwap(waiting, timedOut) {
			wc.fatal(notInit("internal.init was not received in time"))
		}
	})
	defer timer.Stop()

	for {
		env, _, ok := wc.next()
		if !ok || state.Load() == timedOut {
			return nil
		}
		switch env.Type {
		case protocol.MessageTypeInternalPing:
			wc.send(protocol.MessageTypeInternalPong, struct{}{})
			continue
		case protocol.MessageTypeInternalInit, protocol.MessageTypeInternalRestore:
		default:
			if state.CompareAndSwap(waiting, timedOut) {
				wc.fatal(notInit("internal.init is required first"))
			}
			return nil
		}
		if !state.CompareAndSwap(waiting, started) {
			return nil
		}
		var (
			sess *session
			ferr *fatalError
		)
		if env.Type == protocol.MessageTypeInternalInit {
			sess, ferr = h.init(env.Payload, wc)
		} else {
			sess, ferr = h.restore(env.Payload, wc)
		}
		if ferr != nil {
			wc.fatal(ferr)
			return nil
		}
		return sess
	}
}

// readLoop は初期化した後の電文を読む（プロトコル §9）。
func (h *singleHost) readLoop(wc *wsConn, sess *session) {
	for {
		env, size, ok := wc.next()
		if !ok {
			return
		}
		switch env.Type {
		case protocol.MessageTypeInternalPing:
			wc.send(protocol.MessageTypeInternalPong, struct{}{})
		case protocol.MessageTypeInternalInit, protocol.MessageTypeInternalRestore:
			wc.sendError(codeAlreadyInitialized, "already initialized")
		case protocol.MessageTypeInternalResult, protocol.MessageTypeInternalPong:
			// 無視する
		default:
			// ブラウザの電文（DO が中継したもの）。ブラウザ由来の internal.* は DO が破棄する前提。
			if !isInternal(env.Type) && size > clientMessageLimit {
				wc.sendError(codeBadRequest, "message too big")
				continue
			}
			if !sess.deliver(wc, env) {
				return
			}
		}
	}
}

// isInternal は内部の電文（internal.*）かを返す。
func isInternal(t protocol.MessageType) bool {
	return len(t) >= len("internal.") && t[:len("internal.")] == "internal."
}

func badPayload(typ protocol.MessageType) *fatalError {
	return &fatalError{code: codeBadRequest, message: "bad " + string(typ) + " payload", close: closePolicy}
}

func restoreFailed(msg string) *fatalError {
	return &fatalError{code: codeRestoreFailed, message: truncateUTF8(msg, restoreMessageMax), close: closeInternalError}
}

func internalError() *fatalError {
	return &fatalError{code: codeInternalError, message: "internal error", close: closeInternalError}
}

// init は internal.init を処理する。
func (h *singleHost) init(payload json.RawMessage, wc *wsConn) (*session, *fatalError) {
	var p protocol.InternalInitPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, badPayload(protocol.MessageTypeInternalInit)
	}
	return h.open(p, nil, wc)
}

// restore は internal.restore を処理する。
func (h *singleHost) restore(payload json.RawMessage, wc *wsConn) (*session, *fatalError) {
	var p protocol.InternalRestorePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, badPayload(protocol.MessageTypeInternalRestore)
	}
	return h.open(p.InternalInitPayload, &p, wc)
}

// open は枠のセッションにつなぐ（なければ作る）。restore が nil なら internal.init。
func (h *singleHost) open(p protocol.InternalInitPayload, restore *protocol.InternalRestorePayload, wc *wsConn) (*session, *fatalError) {
	srv := h.srv
	if !sessionIDPattern.MatchString(p.SessionID) {
		return nil, &fatalError{code: codeBadRequest, message: "invalid sessionId", close: closePolicy}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if sess := h.sess; sess != nil {
		if sess.id != p.SessionID {
			return nil, &fatalError{code: codeSessionMismatch, message: "another session is running", close: closePolicy}
		}
		// つなぎ直し。Engine を作り直さない（internal.restore の入力ログも使わない。メモリの状態のほうが新しい）。
		srv.log.Info("reconnect", "session", sess.id, "restore", restore != nil)
		if !sess.attachConn(wc, true) {
			return nil, internalError()
		}
		return sess, nil
	}

	st, ok := srv.cat.Lookup(p.StageID)
	if !ok {
		return nil, &fatalError{code: codeStageNotFound, message: "stage not found", close: closeInternalError}
	}
	var (
		engine *sim.Engine
		ferr   *fatalError
	)
	if restore == nil {
		engine, ferr = h.newEngine(p, st)
	} else {
		engine, ferr = h.replay(restore, st)
	}
	if ferr != nil {
		return nil, ferr
	}

	params := srv.sessionParams()
	params.id = p.SessionID
	params.st = st
	params.islands, _ = srv.cat.islands(st.ID)
	params.seed = p.Seed
	params.demo = p.Demo
	params.restored = restore != nil
	params.paused = restore != nil
	params.engine = engine
	sess := newSession(srv.baseCtx, params)
	sess.start()
	h.sess = sess
	if restore == nil {
		srv.log.Info("session created", "session", sess.id, "stage", st.ID)
	}
	if !sess.attachConn(wc, true) {
		return nil, internalError()
	}
	return sess, nil
}

// newEngine は internal.init の Engine を作る。エラー・panic は internal_error。
func (h *singleHost) newEngine(p protocol.InternalInitPayload, st *stage.Stage) (*sim.Engine, *fatalError) {
	log := h.srv.log.With("session", p.SessionID)
	cfg, err := stage.SimConfig(st)
	if err != nil {
		log.Error("stage config", "stage", st.ID, "err", err.Error())
		return nil, internalError()
	}
	var e *sim.Engine
	if r := recovered(func() { e, err = sim.New(cfg, p.Seed, h.srv.simOptions()) }); r != nil {
		log.Error("sim.New panic", "stage", st.ID, "panic", fmt.Sprint(r))
		return nil, internalError()
	}
	if err != nil {
		log.Error("sim.New", "stage", st.ID, "err", err.Error())
		return nil, internalError()
	}
	return e, nil
}

// replay は internal.restore の Engine を入力ログから作る（プロトコル §9 の「復元」）。
func (h *singleHost) replay(p *protocol.InternalRestorePayload, st *stage.Stage) (*sim.Engine, *fatalError) {
	srv := h.srv
	log := srv.log.With("session", p.SessionID)
	in := p.InputLog
	switch {
	case in.Format != sim.ReplayFormat:
		return nil, restoreFailed(fmt.Sprintf("inputLog.format is %d, want %d", in.Format, sim.ReplayFormat))
	case in.StageID != p.StageID:
		return nil, restoreFailed("inputLog.stageId does not match stageId")
	case in.Seed != p.Seed:
		return nil, restoreFailed("inputLog.seed does not match seed")
	}
	entries, err := simconv.InputEntriesFromLog(in.Commands)
	if err != nil {
		return nil, restoreFailed(err.Error())
	}
	if in.Build != srv.cfg.Build {
		// デプロイで版が変わった直後の復元を止めない。食い違えば Replay が ErrReplayDiverged を返す。
		log.Warn("restore across builds", "logBuild", in.Build, "build", srv.cfg.Build)
	}
	cfg, err := stage.SimConfig(st)
	if err != nil {
		log.Error("stage config", "stage", st.ID, "err", err.Error())
		return nil, internalError()
	}
	started := time.Now()
	var e *sim.Engine
	rl := sim.ReplayLog{
		Format:    in.Format,
		StageHash: in.StageHash,
		Seed:      p.Seed,
		Entries:   entries,
		Until:     sim.SimTime(p.Until),
	}
	if r := recovered(func() { e, err = sim.Replay(cfg, st.Hash, rl, srv.simOptions()) }); r != nil {
		log.Error("sim.Replay panic", "stage", st.ID, "panic", fmt.Sprint(r))
		return nil, internalError()
	}
	if err != nil {
		log.Warn("restore failed", "stage", st.ID, "err", err.Error())
		return nil, restoreFailed(err.Error())
	}
	// Replay の Engine は New からの全イベントを持っている。Snapshot().LastSeq 以下はスナップショットに含まれる。
	e.DrainEvents()
	log.Info("restore ok", "stage", st.ID, "commands", len(entries), "now", int64(e.Now()),
		"ms", time.Since(started).Milliseconds())
	return e, nil
}

// simOptions は sim.New・sim.Replay に渡す設定。
func (s *Server) simOptions() sim.Options {
	return sim.Options{CheckInvariants: s.cfg.Invariants}
}

// recovered は f を実行し、panic したらその値を返す。
func recovered(f func()) (r any) {
	defer func() { r = recover() }()
	f()
	return nil
}

// truncateUTF8 は s を max バイト以下に、UTF-8 の文字の途中で切らないように切る。
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
