package host

import (
	"context"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/runtime"
)

// ActorConn は Actor Host から Container（Actor の Runtime）への送信（actor-protocol.md §1）。
// ローカルの dtnfabric（DTN-11）は dtnactor の /ws につなぐ実装を、試験は RuntimeConn か台本の偽物を使う。
type ActorConn interface {
	// Call は要求の電文 raw を送り、応答の電文を返す。ctx の期限を過ぎたら ctx.Err() を返す。
	// Container が応答しない・接続が切れたときは誤りを返す（Host は not_ready を返し、Hooks.OnActorLost を呼ぶ）。
	Call(ctx context.Context, raw []byte) ([]byte, error)
	// Close は接続を閉じる（Container を止める）。2 回目は何もしない。
	Close() error
}

// RuntimeConn は同じプロセスの runtime.Runtime につなぐ ActorConn（試験と、ローカルでの単体の確認用）。
// Close は Runtime を閉じる。
type RuntimeConn struct {
	RT *runtime.Runtime
}

var _ ActorConn = RuntimeConn{}

// Call は RT.HandleRaw に渡す。ctx が先に終わったら ctx.Err() を返す（処理は Runtime の中で続く）。
func (c RuntimeConn) Call(ctx context.Context, raw []byte) ([]byte, error) {
	done := make(chan []byte, 1)
	go func() { done <- c.RT.HandleRaw(raw) }()
	select {
	case b := <-done:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close は RT を閉じる。
func (c RuntimeConn) Close() error {
	c.RT.Close()
	return nil
}
