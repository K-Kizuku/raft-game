package island

import (
	"bytes"
	"fmt"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/islandcore"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// receive は受け取った手紙 l（deliver・unload の 1 通）を確かめて ferry.Letter に戻す。
// digest の照合 → Decode → letterId（送信元の島 × 10^9 ＋ 連番）が欄と一致、の順に確かめる。
// 依頼の手紙なら、Bundle が運ぶ台帳のコマンドの bytes も返す（Raft の手紙は nil）。
func (a *Actor) receive(l *dtnproto.Letter, t int64) (ferry.Letter, []byte, *dtnproto.Error) {
	switch {
	case len(l.Bundle) > a.lim.MaxBundleBytes:
		return ferry.Letter{}, nil, dtnproto.Errorf(dtnproto.CodeTooLarge, "letter %d: bundle of %d bytes exceeds %d", l.LetterID, len(l.Bundle), a.lim.MaxBundleBytes)
	case dtnproto.Digest(l.Bundle) != l.Digest:
		return ferry.Letter{}, nil, invalid("letter %d: digest does not match the bundle", l.LetterID)
	case l.CreatedAt < 0 || l.CreatedAt > t:
		return ferry.Letter{}, nil, invalid("letter %d: createdAt %d is not in [0, %d]", l.LetterID, l.CreatedAt, t)
	}
	dl, cmd, _, err := a.codec.Decode(l.Bundle)
	if err != nil {
		return ferry.Letter{}, nil, invalid("letter %d: %v", l.LetterID, err)
	}
	n := uint64(a.islands)
	switch {
	case dl.ID != l.LetterID:
		return ferry.Letter{}, nil, invalid("letter %d: the bundle is letter %d", l.LetterID, dl.ID)
	case uint64(dl.ID)/islandcore.LetterIDBase != dl.From:
		return ferry.Letter{}, nil, invalid("letter %d: the id is not from island %d", l.LetterID, dl.From)
	case dl.From < 1 || dl.From > n || dl.To < 1 || dl.To > n || dl.From == dl.To:
		return ferry.Letter{}, nil, invalid("letter %d: %d→%d is not between two islands of 1..%d", l.LetterID, dl.From, dl.To, n)
	case dl.Kind == ferry.LetterRaft && len(cmd) != 0:
		return ferry.Letter{}, nil, invalid("letter %d: a raft letter carries a command", l.LetterID)
	}
	dl.CreatedAt = l.CreatedAt
	return dl, cmd, nil
}

// requestFromCommand は、依頼の手紙 reqID が運ぶ台帳のコマンドの bytes cmd（ledger.Encode の形）から依頼を作る。
// 窓口が使うのは台帳のコマンドの欄（ID・Origin・Kind・Resource）だけなので、ほかの欄はゼロ値。
// コマンドが読めない・reqId が違う・依頼元が 1..islands にない・Encode し直すと同じ bytes にならないものは誤り。
func requestFromCommand(reqID string, cmd []byte, islands int) (game.Request, error) {
	c, err := ledger.Decode(cmd)
	if err != nil {
		return game.Request{}, err
	}
	if c.ReqID != reqID {
		return game.Request{}, fmt.Errorf("command reqId %q is not the letter's %q", c.ReqID, reqID)
	}
	if c.Origin < 1 || c.Origin > uint64(islands) {
		return game.Request{}, fmt.Errorf("request %s: origin %d is not in 1..%d", reqID, c.Origin, islands)
	}
	r := game.Request{ID: c.ReqID, Origin: c.Origin, Kind: c.Kind, Resource: c.Resource}
	enc, err := ledger.Encode(r.Command())
	if err != nil {
		return game.Request{}, err
	}
	if !bytes.Equal(enc, cmd) {
		return game.Request{}, fmt.Errorf("request %s: the command is not in the canonical form", reqID)
	}
	return r, nil
}
