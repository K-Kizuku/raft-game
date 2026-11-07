package sim

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// CmdFerryAutoload はおまかせ積み込み（プロトコル §4 の cmd.ferry.autoload。payload は {ferryId, to}）。
const CmdFerryAutoload CommandType = "cmd.ferry.autoload"

// autoload はおまかせ積み込み（ゲームデザイン §3.4、FR-FERRY-03）。船 id のいる島の港の手紙のうち to 宛てのものを、
// 優先度の順に空きの数まで積み、積んだ順に letter.moved を出す。失敗したら何も変えずに ferry のエラーを返す。
//
// 積める手紙がなければ ferry.ErrLetterNotHere、満載なら ferry.ErrCapacityExceeded（何もしない成功にはしない）。
// 遮断中の航路の先へも積める（出航は cmd.ferry.depart が route_blocked にする）。
func (e *Engine) autoload(id ferry.FerryID, to uint64) error {
	f, ok := e.tr.Ferry(id)
	if !ok {
		return fmt.Errorf("%w: %q", ferry.ErrUnknownFerry, id)
	}
	if f.State != ferry.Docked {
		return fmt.Errorf("%w: %q", ferry.ErrNotDocked, id)
	}
	if _, err := e.tr.IsBlocked(f.At, to, int64(e.Now())); err != nil {
		return err
	}
	portIDs := e.tr.Port(f.At)
	port := make([]ferry.Letter, 0, len(portIDs))
	for _, lid := range portIDs {
		l, ok := e.tr.Letter(lid)
		if !ok {
			panic(fmt.Sprintf("sim: autoload: letter %d in port %d not found", lid, f.At))
		}
		port = append(port, l)
	}
	free := max(f.Capacity-len(f.Cargo), 0)
	picks := autoloadPick(port, to, free)
	if !slices.ContainsFunc(port, func(l ferry.Letter) bool { return l.To == to }) {
		return fmt.Errorf("%w: no letter to %d in port %d", ferry.ErrLetterNotHere, to, f.At)
	}
	if free == 0 {
		return fmt.Errorf("%w: %q has %d of %d", ferry.ErrCapacityExceeded, id, len(f.Cargo), f.Capacity)
	}
	if err := e.tr.Load(id, picks); err != nil {
		return err
	}
	for _, lid := range picks {
		e.emit(LetterMoved{LetterID: uint64(lid), Loc: ferryLoc(id)})
	}
	return nil
}

// autoloadPick は、port（港の手紙。ID の昇順）のうち To == to のものを、(autoloadRank, ID) の昇順に並べ、
// 先頭から free 通（足りなければ全部）の ID を返す。純粋な関数。
func autoloadPick(port []ferry.Letter, to uint64, free int) []ferry.LetterID {
	type cand struct {
		rank int
		id   ferry.LetterID
	}
	cands := make([]cand, 0, len(port))
	for _, l := range port {
		if l.To == to {
			cands = append(cands, cand{rank: autoloadRank(l), id: l.ID})
		}
	}
	slices.SortFunc(cands, func(a, b cand) int {
		if c := cmp.Compare(a.rank, b.rank); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})
	n := min(max(free, 0), len(cands))
	out := make([]ferry.LetterID, n)
	for i := range out {
		out[i] = cands[i].id
	}
	return out
}

// autoloadRank はおまかせ積み込みの順位（小さいほど先に積む）。ゲームデザイン §3.4 の 1〜5 に、
// 一覧にない手紙（other）を 6 として足す。append_probe は 3（Commit の通知を運ぶ MsgApp なので）。
func autoloadRank(l ferry.Letter) int {
	switch letterLabel(l) {
	case "request":
		return 1
	case "prevote", "prevote_resp", "vote", "vote_resp":
		return 2
	case "append", "append_probe":
		return 3
	case "append_resp", "append_reject":
		return 4
	case "heartbeat", "heartbeat_resp":
		return 5
	}
	return 6
}
