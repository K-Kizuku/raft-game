// Package runtime は、島・船・Coordinator の Actor が共通に使う Runtime（DTN-02 #159）。
//
// Runtime は、Actor（決定的。I/O・実時間・goroutine を持たない）の前に立ち、次を受け持つ:
//   - 電文の検証（dtnproto.Validate）
//   - init の前の要求を not_ready で断る。init で sessionId・actor・epoch を決める
//   - 別のセッション（session_mismatch）・別の Actor（unknown_actor）・別の世代（stale_epoch）を断る
//   - journal に入れる種類の eventSeq の冪等（actor-protocol.md §5）。持つのは最後の 1 件の応答だけで、
//     全件の保存は Actor Host（DTN-07）
//   - 要求を、1 つの goroutine と FIFO の待ち行列で、届いた順に 1 つずつ処理する
//   - Actor の panic と error を internal（*dtnproto.Error ならそのコード）にし、outputs の上限を確かめる
//
// Serve（serve.go）は、Runtime を WebSocket の /ws で開く。
package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
)

// Actor は Runtime の後ろの Actor（島・船・Coordinator。DTN-03・04・05 が実装する）。
// 決定的であること: I/O・実時間・goroutine・グローバルな乱数を持たず、同じ Init と同じ要求の列から
// 同じ状態と出力になる。Runtime は、メソッドを同時に 2 つ以上呼ばない。
//
//   - Init: init の payload で Actor を作り、最初の出力（島なら開幕式の後の node.state など）を返す
//   - Handle: journal に入れる種類（init・inspect・shutdown を除く）と bundle.export を処理する。
//     payload は Validate で確かめ済み（dtnproto.DecodePayload で型付きの値にできる）
//   - Inspect: 状態の要約（状態は変えない）
//   - Cursor: 今の上限と状態の digest
//
// error に *dtnproto.Error を返すと、そのコードが応答に入る。それ以外の error と panic は internal。
type Actor interface {
	Init(p dtnproto.InitPayload) ([]dtnproto.Output, error)
	Handle(kind dtnproto.Kind, payload json.RawMessage) ([]dtnproto.Output, error)
	Inspect() (json.RawMessage, error)
	Cursor() dtnproto.Cursor
}

// InspectOutputType は inspect の応答の outputs の 1 つの type。data に Actor.Inspect の値が入る
// （init の前は {"ready":false}）。
const InspectOutputType = "inspect"

// Option は New の設定。
type Option func(*Runtime)

// WithLimits は上限を変える（既定は dtnproto.DefaultLimits）。
func WithLimits(lim dtnproto.Limits) Option { return func(rt *Runtime) { rt.lim = lim } }

// Runtime は Actor 1 つの Runtime。New で作り、使い終わったら Close する。並行に呼んでよい。
type Runtime struct {
	actor Actor
	lim   dtnproto.Limits

	jobs      chan job
	quit      chan struct{}
	loopDone  chan struct{}
	stopped   chan struct{} // shutdown を処理したら閉じる
	closeOnce sync.Once

	st state // loop の goroutine だけが触る
}

type job struct {
	raw   []byte
	reply chan []byte
}

// state は Runtime の状態（loop の goroutine だけが触る）。
type state struct {
	inited    bool
	sessionID string
	actor     dtnproto.ActorRef
	epoch     uint64

	// 最後に処理した、journal に入れる種類の要求（eventSeq・要求の digest・応答の bytes）
	lastSeq    uint64
	lastDigest string
	lastResp   []byte

	stopped bool
}

// New は a の Runtime を作り、処理の goroutine を始める。
func New(a Actor, opts ...Option) *Runtime {
	rt := &Runtime{
		actor:    a,
		lim:      dtnproto.DefaultLimits,
		jobs:     make(chan job),
		quit:     make(chan struct{}),
		loopDone: make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	for _, o := range opts {
		o(rt)
	}
	go rt.loop()
	return rt
}

// Limits は上限を返す。
func (rt *Runtime) Limits() dtnproto.Limits { return rt.lim }

// Stopped は shutdown を処理したら閉じるチャネルを返す。
func (rt *Runtime) Stopped() <-chan struct{} { return rt.stopped }

// Close は処理の goroutine を止める（待ち行列の要求には internal を返す）。2 回呼んでよい。
func (rt *Runtime) Close() {
	rt.closeOnce.Do(func() { close(rt.quit) })
	<-rt.loopDone
}

// Handle は要求の電文 raw を処理して応答を返す（HandleRaw の応答を読んだもの）。
func (rt *Runtime) Handle(raw []byte) dtnproto.Response {
	var resp dtnproto.Response
	if err := json.Unmarshal(rt.HandleRaw(raw), &resp); err != nil {
		panic(fmt.Sprintf("runtime: own response is not JSON: %v", err))
	}
	return resp
}

// HandleRaw は要求の電文 raw を待ち行列に入れ、処理した応答の bytes を返す。
// 同じ eventSeq・同じ digest の送り直しには、最初の応答と同じ bytes を返す。
func (rt *Runtime) HandleRaw(raw []byte) []byte {
	j := job{raw: raw, reply: make(chan []byte, 1)}
	select {
	case rt.jobs <- j:
		return <-j.reply
	case <-rt.quit:
		return encode(errorResponse(dtnproto.Request{}, 0, dtnproto.Errorf(dtnproto.CodeInternal, "runtime closed")))
	}
}

func (rt *Runtime) loop() {
	defer close(rt.loopDone)
	for {
		select {
		case j := <-rt.jobs:
			j.reply <- rt.process(j.raw)
		case <-rt.quit:
			return
		}
	}
}

// process は要求 1 つを処理する（loop の goroutine だけが呼ぶ）。
func (rt *Runtime) process(raw []byte) []byte {
	st := &rt.st
	req, verr := dtnproto.Validate(raw, rt.lim)
	if verr != nil {
		return encode(errorResponse(req, rt.respEpoch(req), verr))
	}
	ki, _ := dtnproto.LookupKind(req.Kind)
	if ki.HostOnly {
		return encode(errorResponse(req, rt.respEpoch(req), dtnproto.Errorf(dtnproto.CodeInvalid, "%q is for the Actor Host, not the Container", req.Kind)))
	}

	if !st.inited {
		if st.stopped {
			return encode(errorResponse(req, req.Epoch, dtnproto.Errorf(dtnproto.CodeNotReady, "shut down")))
		}
		switch req.Kind {
		case dtnproto.KindInit:
			return rt.init(req)
		case dtnproto.KindInspect:
			out := dtnproto.Output{Type: InspectOutputType, Data: json.RawMessage(`{"ready":false}`)}
			return encode(okResponse(req, req.Epoch, []dtnproto.Output{out}, nil))
		case dtnproto.KindShutdown:
			rt.stop()
			return encode(okResponse(req, req.Epoch, nil, nil))
		default:
			return encode(errorResponse(req, req.Epoch, dtnproto.Errorf(dtnproto.CodeNotReady, "not initialized")))
		}
	}

	switch {
	case req.SessionID != st.sessionID:
		return encode(errorResponse(req, st.epoch, dtnproto.Errorf(dtnproto.CodeSessionMismatch, "session %q is not this actor's", req.SessionID)))
	case req.Actor != st.actor:
		return encode(errorResponse(req, st.epoch, dtnproto.Errorf(dtnproto.CodeUnknownActor, "this is %q, not %q", st.actor, req.Actor)))
	case req.Epoch != st.epoch:
		return encode(errorResponse(req, st.epoch, dtnproto.Errorf(dtnproto.CodeStaleEpoch, "epoch %d is not %d", req.Epoch, st.epoch)))
	case req.Kind == dtnproto.KindInit && req.EventSeq != st.lastSeq:
		return encode(errorResponse(req, st.epoch, dtnproto.Errorf(dtnproto.CodeInvalid, "already initialized")))
	}

	if !ki.Journaled {
		if st.stopped {
			return encode(errorResponse(req, st.epoch, dtnproto.Errorf(dtnproto.CodeNotReady, "shut down")))
		}
		return encode(rt.run(req))
	}

	digest, err := dtnproto.RequestDigest(req)
	if err != nil {
		return encode(errorResponse(req, st.epoch, asProtoError(err)))
	}
	switch {
	case req.EventSeq < st.lastSeq:
		return encode(errorResponse(req, st.epoch, dtnproto.Errorf(dtnproto.CodeStaleEvent, "eventSeq %d < %d", req.EventSeq, st.lastSeq)))
	case req.EventSeq == st.lastSeq && digest == st.lastDigest:
		return st.lastResp
	case req.EventSeq == st.lastSeq:
		return encode(errorResponse(req, st.epoch, dtnproto.Errorf(dtnproto.CodeConflict, "eventSeq %d was processed with another request", req.EventSeq)))
	case st.stopped:
		return encode(errorResponse(req, st.epoch, dtnproto.Errorf(dtnproto.CodeNotReady, "shut down")))
	}
	b := rt.finish(rt.run(req))
	st.lastSeq, st.lastDigest, st.lastResp = req.EventSeq, digest, b
	return b
}

// init は init の要求を処理する。成功したら sessionId・actor・epoch を決め、応答を冪等のために持つ。
func (rt *Runtime) init(req dtnproto.Request) []byte {
	digest, err := dtnproto.RequestDigest(req)
	if err != nil {
		return encode(errorResponse(req, req.Epoch, asProtoError(err)))
	}
	pv, perr := dtnproto.DecodePayload(req.Kind, req.Payload, req.Actor, rt.lim)
	if perr != nil {
		return encode(errorResponse(req, req.Epoch, perr))
	}
	p := pv.(*dtnproto.InitPayload)
	var outs []dtnproto.Output
	if err := safely(func() (err error) { outs, err = rt.actor.Init(*p); return err }); err != nil {
		// 状態を決めない。Host は Container を作り直す（persistence-and-recovery.md §4.1）
		return encode(errorResponse(req, req.Epoch, asProtoError(err)))
	}
	st := &rt.st
	st.inited, st.sessionID, st.actor, st.epoch = true, req.SessionID, req.Actor, req.Epoch
	b := rt.finish(rt.okWithCursor(req, outs))
	st.lastSeq, st.lastDigest, st.lastResp = req.EventSeq, digest, b
	return b
}

// run は init の後の要求を Actor に渡す。
func (rt *Runtime) run(req dtnproto.Request) dtnproto.Response {
	switch req.Kind {
	case dtnproto.KindShutdown:
		rt.stop()
		return rt.okWithCursor(req, nil)
	case dtnproto.KindInspect:
		var data json.RawMessage
		if err := safely(func() (err error) { data, err = rt.actor.Inspect(); return err }); err != nil {
			return errorResponse(req, rt.st.epoch, asProtoError(err))
		}
		resp := rt.okWithCursor(req, nil)
		if resp.Status == dtnproto.StatusOK {
			resp.Outputs = []dtnproto.Output{{T: resp.Cursor.Horizon.T, Type: InspectOutputType, Data: data}}
		}
		return resp
	}
	var outs []dtnproto.Output
	if err := safely(func() (err error) { outs, err = rt.actor.Handle(req.Kind, req.Payload); return err }); err != nil {
		return errorResponse(req, rt.st.epoch, asProtoError(err))
	}
	return rt.okWithCursor(req, outs)
}

func (rt *Runtime) okWithCursor(req dtnproto.Request, outs []dtnproto.Output) dtnproto.Response {
	var cur dtnproto.Cursor
	if err := safely(func() error { cur = rt.actor.Cursor(); return nil }); err != nil {
		return errorResponse(req, rt.st.epoch, asProtoError(err))
	}
	return okResponse(req, rt.st.epoch, outs, &cur)
}

// finish は outputs の数と応答の大きさを確かめて符号化する。
func (rt *Runtime) finish(resp dtnproto.Response) []byte {
	if len(resp.Outputs) > rt.lim.MaxOutputs {
		return encode(errorResponse(dtnproto.Request{Actor: resp.Actor, EventSeq: resp.EventSeq}, resp.Epoch,
			dtnproto.Errorf(dtnproto.CodeTooLarge, "%d outputs exceeds %d", len(resp.Outputs), rt.lim.MaxOutputs)))
	}
	b := encode(resp)
	if len(b) > rt.lim.MaxMessageBytes {
		return encode(errorResponse(dtnproto.Request{Actor: resp.Actor, EventSeq: resp.EventSeq}, resp.Epoch,
			dtnproto.Errorf(dtnproto.CodeTooLarge, "response of %d bytes exceeds %d", len(b), rt.lim.MaxMessageBytes)))
	}
	return b
}

func (rt *Runtime) stop() {
	if !rt.st.stopped {
		rt.st.stopped = true
		close(rt.stopped)
	}
}

// respEpoch は応答に書く epoch（init の後は Runtime の epoch、前は要求の epoch）。
func (rt *Runtime) respEpoch(req dtnproto.Request) uint64 {
	if rt.st.inited {
		return rt.st.epoch
	}
	return req.Epoch
}

func okResponse(req dtnproto.Request, epoch uint64, outs []dtnproto.Output, cur *dtnproto.Cursor) dtnproto.Response {
	if outs == nil {
		outs = []dtnproto.Output{}
	}
	for i := range outs {
		if len(outs[i].Data) == 0 {
			outs[i].Data = json.RawMessage(`{}`)
		}
	}
	return dtnproto.Response{V: dtnproto.Version, Actor: req.Actor, Epoch: epoch, EventSeq: req.EventSeq,
		Status: dtnproto.StatusOK, Outputs: outs, Cursor: cur}
}

func errorResponse(req dtnproto.Request, epoch uint64, e *dtnproto.Error) dtnproto.Response {
	return dtnproto.Response{V: dtnproto.Version, Actor: req.Actor, Epoch: epoch, EventSeq: req.EventSeq,
		Status: dtnproto.StatusError, Outputs: []dtnproto.Output{}, Error: e}
}

func encode(resp dtnproto.Response) []byte {
	b, err := json.Marshal(resp)
	if err != nil {
		// Actor が不正な JSON の data を返したときなど
		resp = errorResponse(dtnproto.Request{Actor: resp.Actor, EventSeq: resp.EventSeq}, resp.Epoch,
			dtnproto.Errorf(dtnproto.CodeInternal, "encode response: %v", err))
		b, _ = json.Marshal(resp)
	}
	return b
}

// safely は f を呼び、panic を error にする。
func safely(f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return f()
}

// asProtoError は err を *dtnproto.Error にする（*dtnproto.Error でなければ internal）。
func asProtoError(err error) *dtnproto.Error {
	if pe, ok := errors.AsType[*dtnproto.Error](err); ok && pe != nil {
		return pe
	}
	return dtnproto.Errorf(dtnproto.CodeInternal, "%v", err)
}
