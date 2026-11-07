package sim

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
)

// traceHash はトレースのハッシュ（DesignDoc §10.2）。
// 各イベントの json.Marshal(DomainEvent) の後ろに改行を 1 つ付けたものを、出した順に連結した SHA-256。
// 最後のイベントの後ろにも改行を付ける。イベントが 0 件なら空のバイト列の SHA-256。
type traceHash struct {
	h hash.Hash
}

func newTraceHash() traceHash { return traceHash{h: sha256.New()} }

// write はイベント 1 件を書き込む。json.Marshal のエラーは起きないはずなので panic する。
func (t *traceHash) write(ev DomainEvent) {
	b, err := json.Marshal(ev)
	if err != nil {
		panic(fmt.Sprintf("sim: marshal event %d (%s): %v", ev.Seq, ev.Type, err))
	}
	_, _ = t.h.Write(b) // hash.Hash の Write はエラーを返さない
	_, _ = t.h.Write([]byte{'\n'})
}

// sum は今までに書き込んだ分のハッシュを返す。状態は変えない。
func (t *traceHash) sum() [32]byte {
	var out [32]byte
	copy(out[:], t.h.Sum(nil))
	return out
}
