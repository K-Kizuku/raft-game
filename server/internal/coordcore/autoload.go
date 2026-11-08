package coordcore

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// SelectAutoload はおまかせ積み込み（ゲームデザイン §3.4、FR-FERRY-03）で積む手紙を選ぶ。船 id のいる島の港の手紙
// （写し）のうち、to 宛てのものと、to を経由して先の島へ行くもの（船のいる島と直接の航路がない宛先で、宛先までの
// 最短の経路で次に寄る島が to。ferry.Fleet.NextHop）を、優先度の順に空きの数まで、積む順に返す。状態は変えない。
//
// 積める手紙がなければ ferry.ErrLetterNotHere、満載なら ferry.ErrCapacityExceeded（何もしない成功にはしない）。
// 遮断中の航路の先へも選べる（出航は cmd.ferry.depart が route_blocked にする）。
func SelectAutoload(fl *ferry.Fleet, id ferry.FerryID, to uint64, now int64) ([]ferry.LetterID, error) {
	f, ok := fl.Ferry(id)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ferry.ErrUnknownFerry, id)
	}
	if f.State != ferry.Docked {
		return nil, fmt.Errorf("%w: %q", ferry.ErrNotDocked, id)
	}
	if _, err := fl.IsBlocked(f.At, to, now); err != nil {
		return nil, err
	}
	portIDs := fl.Port(f.At)
	port := make([]ferry.Letter, 0, len(portIDs))
	for _, lid := range portIDs {
		l, ok := fl.Letter(lid)
		if !ok {
			panic(fmt.Sprintf("coordcore: autoload: letter %d in port %d not found", lid, f.At))
		}
		port = append(port, l)
	}
	free := max(f.Capacity-len(f.Cargo), 0)
	at := f.At
	nextHop := func(dest uint64) (uint64, bool) {
		// 直接の航路がある宛先は、その航路で運ぶ（経由にしない）。三角形の航路で、近い島を回る方が速くても、
		// 隣の島宛ての手紙を別の島への船に積まない（greedy の経路の考え方とも合う）。
		if fl.HasRoute(at, dest) {
			return 0, false
		}
		return fl.NextHop(at, dest)
	}
	picks := AutoloadPick(port, to, nextHop, free)
	if !slices.ContainsFunc(port, func(l ferry.Letter) bool { return AutoloadVia(l, to, nextHop) >= 0 }) {
		return nil, fmt.Errorf("%w: no letter to or via %d in port %d", ferry.ErrLetterNotHere, to, f.At)
	}
	if free == 0 {
		return nil, fmt.Errorf("%w: %q has %d of %d", ferry.ErrCapacityExceeded, id, len(f.Cargo), f.Capacity)
	}
	return picks, nil
}

// AutoloadPick は、port（港の手紙。ID の昇順）のうち to 宛てのものと to を経由するもの（AutoloadVia）を、
// (AutoloadRank, 宛て→経由, ID) の昇順に並べ、先頭から free 通（足りなければ全部）の ID を返す。
// nextHop(dest) は、船のいる島から dest へ経由で運ぶときに次に寄る島（経由にしない宛先・経路がなければ false）。純粋な関数。
func AutoloadPick(port []ferry.Letter, to uint64, nextHop func(dest uint64) (uint64, bool), free int) []ferry.LetterID {
	type cand struct {
		rank int
		via  int
		id   ferry.LetterID
	}
	cands := make([]cand, 0, len(port))
	for _, l := range port {
		if via := AutoloadVia(l, to, nextHop); via >= 0 {
			cands = append(cands, cand{rank: AutoloadRank(l), via: via, id: l.ID})
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

// AutoloadVia は、行き先 to へのおまかせ積み込みで手紙 l を積むかを返す。to 宛てなら 0、to を経由して先の島へ
// 行く（nextHop(l.To) が to）なら 1、どちらでもない（経路のない宛先を含む）なら -1。
func AutoloadVia(l ferry.Letter, to uint64, nextHop func(dest uint64) (uint64, bool)) int {
	if l.To == to {
		return 0
	}
	if next, ok := nextHop(l.To); ok && next == to {
		return 1
	}
	return -1
}

// AutoloadRank はおまかせ積み込みの順位（小さいほど先に積む）。ゲームデザイン §3.4 の 1〜5 に、
// 一覧にない手紙（other）を 6 として足す。append_probe は 3（Commit の通知を運ぶ MsgApp なので）。
func AutoloadRank(l ferry.Letter) int {
	switch LetterLabel(l) {
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
