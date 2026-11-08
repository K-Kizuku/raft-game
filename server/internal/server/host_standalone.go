package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"sync"
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/stageconv"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// standaloneEntry は一覧のセッション 1 つ。
type standaloneEntry struct {
	sess      *session
	tokenHash [sha256.Size]byte
}

// standaloneHost は standalone モードのセッションの一覧。
type standaloneHost struct {
	srv      *Server
	mu       sync.Mutex
	sessions map[string]*standaloneEntry
}

// add はセッションを一覧に足す。生きているセッションが MaxSessions 個あれば false。
func (h *standaloneHost) add(id string, e *standaloneEntry) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sessions) >= h.srv.cfg.MaxSessions {
		return false
	}
	h.sessions[id] = e
	return true
}

// full は、生きているセッションが MaxSessions 個あるかを返す。
func (h *standaloneHost) full() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions) >= h.srv.cfg.MaxSessions
}

// lookup は sessionId とトークンでセッションを探す。トークンの SHA-256 を定数時間で比べる。
// ない・トークンが違うは区別しない。
func (h *standaloneHost) lookup(id, token string) (*session, bool) {
	h.mu.Lock()
	e, ok := h.sessions[id]
	h.mu.Unlock()
	if !ok || token == "" {
		return nil, false
	}
	sum := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(sum[:], e.tokenHash[:]) != 1 {
		return nil, false
	}
	return e.sess, true
}

// remove は id のセッションを一覧から消し、goroutine を止める（終わるのは待たない）。
func (h *standaloneHost) remove(id string) {
	h.mu.Lock()
	e, ok := h.sessions[id]
	delete(h.sessions, id)
	h.mu.Unlock()
	if ok {
		e.sess.cancel()
	}
}

// stopAll はすべてのセッションを止めて、終わるのを待つ。
func (h *standaloneHost) stopAll() {
	h.mu.Lock()
	list := make([]*session, 0, len(h.sessions))
	for id, e := range h.sessions {
		list = append(list, e.sess)
		delete(h.sessions, id)
	}
	h.mu.Unlock()
	for _, s := range list {
		s.stop()
	}
}

// sweep は、接続のないまま idleTTL を過ぎたセッションを消す（作ってから一度もつながらないセッションも、
// 作った時刻から数える）。消した数を返す。
func (h *standaloneHost) sweep() int {
	now := h.srv.now().UnixNano()
	ttl := h.srv.idleTTL.Nanoseconds()
	h.mu.Lock()
	var expired []*session
	for id, e := range h.sessions {
		if e.sess.connected.Load() {
			continue
		}
		if now-e.sess.lastSeen.Load() > ttl {
			expired = append(expired, e.sess)
			delete(h.sessions, id)
		}
	}
	h.mu.Unlock()
	for _, s := range expired {
		s.log.Info("session expired")
		s.stop()
	}
	return len(expired)
}

// sweepLoop は sweepInterval ごとに sweep を呼ぶ。
func (h *standaloneHost) sweepLoop(ctx context.Context) {
	t := time.NewTicker(h.srv.sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.sweep()
		}
	}
}

// serveWS は GET /ws/sessions/{id}（パス・メソッド・Origin・Upgrade・トークンは検査済み）。
// snapshot は送らず、クライアントの hello を待つ（プロトコル §4）。
func (h *standaloneHost) serveWS(w http.ResponseWriter, r *http.Request, sess *session) {
	srv := h.srv
	c, err := acceptWS(w, r, clientMessageLimit)
	if err != nil {
		srv.log.Debug("websocket accept", "err", err.Error())
		return
	}
	wc := newWSConn(c, srv.log.With("session", sess.id))
	srv.track(wc)
	defer srv.untrack(wc)
	defer wc.finish()

	if !sess.attachConn(wc, false) {
		wc.fatal(&fatalError{code: codeInternalError, message: "session is gone", close: closeInternalError})
		return
	}
	defer sess.detachConn(wc)
	for {
		env, _, ok := wc.next()
		if !ok {
			return
		}
		if isInternal(env.Type) {
			// standalone では Go が Durable Object の役も兼ねるので、ブラウザからの internal.* を破棄する。
			wc.log.Debug("drop internal message", "type", string(env.Type))
			continue
		}
		if !sess.deliver(wc, env) {
			return
		}
	}
}

// newStandaloneEntry は、トークンのハッシュを覚えた一覧の要素を作る。
func newStandaloneEntry(sess *session, token string) *standaloneEntry {
	return &standaloneEntry{sess: sess, tokenHash: sha256.Sum256([]byte(token))}
}

// stageSummaries は /api/stages の本文を作る。形は stageconv.Summaries（Worker の一覧と同じ。#113）、
// デモ用のステージ（demo のあるステージ）は --demo-controls のときだけ出す（Worker の visibleStages と同じ規則）。
// features.autoFerry は --auto-ferry（Worker では AUTO_FERRY が "1"。T-724）。
func (s *Server) stageSummaries() protocol.StagesResponse {
	all := s.cat.Stages()
	visible := make([]*stage.Stage, 0, len(all))
	for _, st := range all {
		if s.stageVisible(st) {
			visible = append(visible, st)
		}
	}
	return protocol.StagesResponse{
		Stages:   stageconv.Summaries(visible),
		Features: protocol.Features{AutoFerry: s.cfg.AutoFerry},
	}
}

// stageVisible は、st を /api/stages に出し、POST /api/sessions で受け付けるかを返す。
// デモ用のステージ（st.Demo != nil）は --demo-controls（DEMO_CONTROLS=1）のときだけ。stageconv.Summary の demo と同じ判定。
func (s *Server) stageVisible(st *stage.Stage) bool {
	return st.Demo == nil || s.cfg.DemoControls
}
