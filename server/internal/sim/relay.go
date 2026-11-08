package sim

import (
	"fmt"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// 積み替え（中継倉庫、FR-FERRY-06・ADR-0006・DesignDoc §6.5）。
// 経由の手紙は cmd.ferry.unload で船のいる島の中継倉庫に預け（engine.go の applyFerry）、
// cmd.relay.load で別の船（同じ船でもよい）に積み直す。倉庫の手紙は Raft では処理しない。

// CmdRelayLoad は、停泊中の船にその島の中継倉庫の手紙を積むコマンド（FerryID・LetterIDs）。
const CmdRelayLoad CommandType = "cmd.relay.load"

// CodeRelayFull は、預けると中継倉庫が relayCap 通を超えるときのエラーコード（プロトコル §4.1）。
const CodeRelayFull = "relay_full"

// relayLoc は中継倉庫の位置。
func relayLoc(island uint64) LocView { return LocView{Type: "relay", IslandID: island} }

// applyRelayLoad は cmd.relay.load を適用し、成功したら渡した順に letter.moved{loc: ferry} を出す。
// 失敗したら何も変えずに ferry のエラーを返す。
func (e *Engine) applyRelayLoad(id ferry.FerryID, letters []uint64) error {
	ids := toLetterIDs(letters)
	if err := e.tr.RelayLoad(id, ids); err != nil {
		return err
	}
	f, _ := e.tr.Ferry(id)
	if e.remote != nil {
		e.remote.RelayLoad(e.Now(), id, f.At, ids)
	} else if _, err := e.islands[f.At-1].TakeRelay(ids); err != nil {
		panic(fmt.Sprintf("sim: relay load %q: island %d: %v", id, f.At, err))
	}
	for _, lid := range letters {
		e.emit(LetterMoved{LetterID: lid, Loc: ferryLoc(id)})
	}
	return nil
}
