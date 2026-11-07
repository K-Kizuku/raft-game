package sim

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// CmdFerryAutoload はおまかせ積み込み（プロトコル §4 の cmd.ferry.autoload。payload は {ferryId, to}）。
const CmdFerryAutoload CommandType = "cmd.ferry.autoload"

// autoload はおまかせ積み込み（ゲームデザイン §3.4、FR-FERRY-03）。船 id のいる島の港の手紙のうち、to 宛てのものと、
// to を経由して先の島へ行くもの（船のいる島と直接の航路がない宛先で、宛先までの最短の経路で次に寄る島が to。
// ferry.Transport.NextHop）を、
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
	at := f.At
	nextHop := func(dest uint64) (uint64, bool) {
		// 直接の航路がある宛先は、その航路で運ぶ（経由にしない）。三角形の航路で、近い島を回る方が速くても、
		// 隣の島宛ての手紙を別の島への船に積まない（greedy の経路の考え方とも合う）。
		if e.tr.HasRoute(at, dest) {
			return 0, false
		}
		return e.tr.NextHop(at, dest)
	}
	picks := autoloadPick(port, to, nextHop, free)
	if !slices.ContainsFunc(port, func(l ferry.Letter) bool { return autoloadVia(l, to, nextHop) >= 0 }) {
		return fmt.Errorf("%w: no letter to or via %d in port %d", ferry.ErrLetterNotHere, to, f.At)
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

// autoloadPick は、port（港の手紙。ID の昇順）のうち to 宛てのものと to を経由するもの（autoloadVia）を、
// (autoloadRank, 宛て→経由, ID) の昇順に並べ、先頭から free 通（足りなければ全部）の ID を返す。
// nextHop(dest) は、船のいる島から dest へ経由で運ぶときに次に寄る島（経由にしない宛先・経路がなければ false）。純粋な関数。
func autoloadPick(port []ferry.Letter, to uint64, nextHop func(dest uint64) (uint64, bool), free int) []ferry.LetterID {
	type cand struct {
		rank int
		via  int
		id   ferry.LetterID
	}
	cands := make([]cand, 0, len(port))
	for _, l := range port {
		if via := autoloadVia(l, to, nextHop); via >= 0 {
			cands = append(cands, cand{rank: autoloadRank(l), via: via, id: l.ID})
		}
	}
	slices.SortFunc(cands, func(a, b cand) int {
		if c := cmp.Compare(a.rank, b.rank); c != 0 {
			return c
		}
		if c := cmp.Compare(a.via, b.via); c != 0 {
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

// autoloadVia は、行き先 to へのおまかせ積み込みで手紙 l を積むかを返す。to 宛てなら 0、to を経由して先の島へ
// 行く（nextHop(l.To) が to）なら 1、どちらでもない（経路のない宛先を含む）なら -1。
func autoloadVia(l ferry.Letter, to uint64, nextHop func(dest uint64) (uint64, bool)) int {
	if l.To == to {
		return 0
	}
	if next, ok := nextHop(l.To); ok && next == to {
		return 1
	}
	return -1
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
