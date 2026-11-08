package coordcore

import (
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// Config は CoordinatorCore の設定。値はすべてステージ定義から渡す。
type Config struct {
	Islands  int // 島の数 N。ID は 1..N
	Routes   []ferry.RouteConfig
	Ferries  []ferry.FerryConfig
	DockMs   int64
	RelayCap int
}

// Core は Coordinator の核。Sched は Coordinator のイベントのスケジューラ（島のイベントは各島が持つ）。
type Core struct {
	Sched Scheduler
	Fleet *ferry.Fleet

	staleTerm []uint64 // 送信元の手紙を古いと判定した基準の Term。添字 = ID - 1
}

// New は設定を検査して Core を作る。
func New(cfg Config) (*Core, error) {
	if cfg.Islands < 1 {
		return nil, fmt.Errorf("coordcore: islands %d < 1", cfg.Islands)
	}
	ids := make([]uint64, cfg.Islands)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	fl, err := ferry.NewFleet(ferry.FleetConfig{
		Islands:  ids,
		Routes:   cfg.Routes,
		Ferries:  cfg.Ferries,
		DockMs:   cfg.DockMs,
		RelayCap: cfg.RelayCap,
	})
	if err != nil {
		return nil, fmt.Errorf("coordcore: %w", err)
	}
	return &Core{Fleet: fl, staleTerm: make([]uint64, cfg.Islands)}, nil
}

// Now は仮想時計の今の時刻を返す。
func (c *Core) Now() SimTime { return c.Sched.Now() }

// StaleTerm は、送信元 island の手紙を古いと判定した基準の Term を返す。
func (c *Core) StaleTerm(island uint64) uint64 { return c.staleTerm[island-1] }

// StaleLetters は、島 island のノードの Term が term に上がったと知らされたとき（島の出力 term.changed）に、
// island が送信元で港・船・中継倉庫にある Raft の手紙のうち、基準の Term <= メッセージの Term < term のものの ID を
// 昇順に返し、基準を term にする。term が基準以下なら何もしない（nil）。
//
// 基準の Term より古い手紙は、すでに古いと知らせ済み（letter.created の raft.stale == true、または letter.stale）。
// Term は減らず、手紙の ID は再利用しないので、各手紙は 1 回だけ古いと知らされる。
func (c *Core) StaleLetters(island, term uint64) []ferry.LetterID {
	old := c.staleTerm[island-1]
	if term <= old {
		return nil
	}
	fl := c.Fleet
	ids := fl.Port(island)
	for _, f := range fl.Ferries() {
		for _, lid := range f.Cargo {
			l, ok := fl.Letter(lid)
			if !ok {
				panic(fmt.Sprintf("coordcore: letter %d on ferry %q not found", lid, f.ID))
			}
			if l.From == island {
				ids = append(ids, lid)
			}
		}
	}
	for at := uint64(1); at <= uint64(len(c.staleTerm)); at++ {
		for _, lid := range fl.Relay(at) {
			l, ok := fl.Letter(lid)
			if !ok {
				panic(fmt.Sprintf("coordcore: letter %d in relay %d not found", lid, at))
			}
			if l.From == island {
				ids = append(ids, lid)
			}
		}
	}
	slices.Sort(ids)
	var out []ferry.LetterID
	for _, lid := range ids {
		l, ok := fl.Letter(lid)
		if !ok {
			panic(fmt.Sprintf("coordcore: stale: letter %d not found", lid))
		}
		if l.Kind != ferry.LetterRaft {
			continue
		}
		if mt := l.Raft.GetTerm(); old <= mt && mt < term {
			out = append(out, lid)
		}
	}
	c.staleTerm[island-1] = term
	return out
}
