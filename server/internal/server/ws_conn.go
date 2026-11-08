package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

// 接続の上限（DesignDoc §12.5・プロトコル §9）。
const (
	// singleReadLimit は single の接続（Durable Object からだけ）の 1 電文の上限。internal.restore は
	// 入力ログ（最大 10,000 行・512 KiB）を含むので、余裕を見て 1 MiB にする。
	singleReadLimit = 1 << 20
	// clientMessageLimit は、ブラウザの電文（internal.* 以外）の 1 電文の上限。standalone の接続の読み取りの上限。
	clientMessageLimit = 16 << 10
	// sendQueueSize は、接続ごとの送信キューの容量。あふれたら 1013 で閉じる（まとめ送りは T-303）。
	sendQueueSize = 256
	// writeTimeout は書き込み 1 回のタイムアウト。
	writeTimeout = 5 * time.Second
)

// outMsg は送信キューの 1 件。fatal が nil でなければ、この電文を送った後に閉じる。
type outMsg struct {
	typ     protocol.MessageType
	payload any
	fatal   *fatalError
}

// wsConn は、いまつながっている WebSocket 1 本。
//
// 読み取りは HTTP のハンドラーの goroutine（read を呼ぶ側）、書き込みは writeLoop の goroutine 1 つで行う。
// seq は接続ごとに 1 から数え、writeLoop が付ける（つなぎ直した接続はまた 1 から）。
type wsConn struct {
	c   *websocket.Conn
	log *slog.Logger

	ctx    context.Context // 読み取りと書き込みの親。cancel で writeLoop を止める
	cancel context.CancelFunc
	out    chan outMsg
	kill   chan websocket.StatusCode // すぐに閉じる（キューの残りを送らない）
	done   chan struct{}             // writeLoop が終わったら閉じる

	mu      sync.Mutex
	closing bool // fatal か kill を受けた。以後の send は捨てる
}

// newWSConn は c を包み、書き込みの goroutine を始める。
func newWSConn(c *websocket.Conn, log *slog.Logger) *wsConn {
	ctx, cancel := context.WithCancel(context.Background())
	w := &wsConn{
		c:      c,
		log:    log,
		ctx:    ctx,
		cancel: cancel,
		out:    make(chan outMsg, sendQueueSize),
		kill:   make(chan websocket.StatusCode, 1),
		done:   make(chan struct{}),
	}
	go w.writeLoop()
	return w
}

// send は電文 1 つを送信キューに入れる。ふさがらない。キューがいっぱいなら接続を 1013 で閉じて false を返す
// （セッションは残り、クライアントはつなぎ直して snapshot を受ける）。閉じている途中なら捨てて false。
func (w *wsConn) send(typ protocol.MessageType, payload any) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closing {
		return false
	}
	select {
	case w.out <- outMsg{typ: typ, payload: payload}:
		return true
	default:
		w.closing = true
		w.kill <- closeTryAgainLater
		w.log.Warn("send queue overflow", "type", string(typ))
		return false
	}
}

// queued は送信キューにいま積まれている電文の数（len）。どの goroutine から呼んでもよい。
func (w *wsConn) queued() int { return len(w.out) }

// sendError は fatal でない error の電文を送る。
func (w *wsConn) sendError(code, message string) {
	w.send(protocol.MessageTypeError, protocol.ErrorPayload{Code: code, Message: message, Fatal: false})
}

// fatal は、キューの前の電文を送ってから fatal の error を送り、f.close で閉じる。
// 2 回目以降（すでに閉じている途中）は何もしない。
func (w *wsConn) fatal(f *fatalError) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closing {
		return
	}
	w.closing = true
	select {
	case w.out <- outMsg{
		typ:     protocol.MessageTypeError,
		payload: protocol.ErrorPayload{Code: f.code, Message: f.message, Fatal: true},
		fatal:   f,
	}:
	default:
		w.kill <- f.close
	}
}

// isClosing は fatal か kill を受けたかを返す。
func (w *wsConn) isClosing() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closing
}

// read は次の電文を読む。
func (w *wsConn) read() (websocket.MessageType, []byte, error) {
	return w.c.Read(w.ctx)
}

// next は次の電文を読んでエンベロープにする。続けられない（切断・fatal）なら ok は false。
//
//   - バイナリのフレーム → bad_request（fatal、1003）
//   - v が 1 でない・ない → version_mismatch（fatal、1008）
//   - エンベロープが読めない → bad_request（fatal でない）を送って読み直す
func (w *wsConn) next() (env protocol.Envelope, size int, ok bool) {
	for {
		typ, data, err := w.read()
		if err != nil {
			if s := websocket.CloseStatus(err); s == websocket.StatusMessageTooBig {
				w.log.Debug("message too big")
			}
			return protocol.Envelope{}, 0, false
		}
		if typ != websocket.MessageText {
			w.fatal(&fatalError{code: codeBadRequest, message: "binary frames are not supported", close: closeUnsupported})
			return protocol.Envelope{}, 0, false
		}
		env, err := protocol.DecodeEnvelope(data)
		switch {
		case errors.Is(err, protocol.ErrVersionMismatch):
			w.fatal(&fatalError{code: codeVersionMismatch, message: "protocol version mismatch", close: closePolicy})
			return protocol.Envelope{}, 0, false
		case err != nil:
			w.sendError(codeBadRequest, "bad envelope")
			continue
		}
		w.log.Debug("recv", "type", string(env.Type), "bytes", len(data))
		return env, len(data), true
	}
}

// finish は接続を片付ける。fatal・kill の途中なら、書き込みの goroutine が閉じ終えるのを待つ。
func (w *wsConn) finish() {
	if !w.isClosing() {
		w.cancel()
	}
	<-w.done
	w.cancel()
}

// writeLoop は送信キューを順に書き込む。seq は 1 から付ける。
func (w *wsConn) writeLoop() {
	defer close(w.done)
	var seq uint64
	for {
		select {
		case <-w.ctx.Done():
			_ = w.c.CloseNow()
			return
		case code := <-w.kill:
			_ = w.c.Close(code, "")
			return
		case m := <-w.out:
			seq++
			b, err := protocol.EncodeEnvelope(m.typ, seq, "", m.payload)
			if err != nil {
				// 自分で組み立てた payload なので起きないはず。送らずに閉じる。
				w.log.Error("encode", "type", string(m.typ), "err", err.Error())
				_ = w.c.Close(closeInternalError, "")
				return
			}
			ctx, cancel := context.WithTimeout(w.ctx, writeTimeout)
			err = w.c.Write(ctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				w.log.Debug("write failed", "type", string(m.typ), "err", err.Error())
				_ = w.c.CloseNow()
				return
			}
			w.log.Debug("send", "type", string(m.typ), "seq", seq, "bytes", len(b))
			if m.fatal != nil {
				_ = w.c.Close(m.fatal.close, "")
				return
			}
		}
	}
}

// acceptWS は WebSocket にアップグレードする。Origin の検査は呼び出し側が済ませる（ライブラリの検査は使わない）。
func acceptWS(w http.ResponseWriter, r *http.Request, readLimit int64) (*websocket.Conn, error) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(readLimit)
	return c, nil
}
