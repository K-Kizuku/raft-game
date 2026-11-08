package sim

import (
	"github.com/K-Kizuku/raft-game/server/internal/coordcore"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// CmdFerryAutoload はおまかせ積み込み（プロトコル §4 の cmd.ferry.autoload。payload は {ferryId, to}）。
const CmdFerryAutoload CommandType = "cmd.ferry.autoload"

// 選び方は coordcore（CoordinatorCore）に置いた（DTN-00 #363）。
var (
	autoloadPick = coordcore.AutoloadPick
	autoloadRank = coordcore.AutoloadRank
)

// autoload はおまかせ積み込み（ゲームデザイン §3.4、FR-FERRY-03）。coordcore.SelectAutoload で選んだ手紙を、
// ふつうの積み込みとして積み、積んだ順に letter.moved を出す。失敗したら何も変えずに ferry のエラーを返す。
func (e *Engine) autoload(id ferry.FerryID, to uint64) error {
	picks, err := coordcore.SelectAutoload(e.tr, id, to, int64(e.Now()))
	if err != nil {
		return err
	}
	if err := e.load(id, picks); err != nil {
		return err
	}
	for _, lid := range picks {
		e.emit(LetterMoved{LetterID: uint64(lid), Loc: ferryLoc(id)})
	}
	return nil
}
