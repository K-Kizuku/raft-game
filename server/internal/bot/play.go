package bot

import (
	"errors"
	"fmt"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// pollMs は、何も起きないときに次に Act を呼ぶまでの最大の間隔。手紙は RaftTick ごとに港に生まれるので、
// そのたびに起きられるよう、ステージの raft.tickMs（既定 500）より短くした。ボットの方針で、ゲームの数値ではない。
const pollMs sim.SimTime = 250

// Play は、PhaseIntro の e に cmd.game.start を Apply し、ラウンドの終わり（PhaseEnded）まで b で動かす。
// DrainEvents は呼ばない（イベントは e に溜まる。取り出すのは呼び出し側）。観察は Snapshot と NextNotableAt だけで行う。
func Play(e *sim.Engine, b Bot) error {
	switch {
	case e == nil:
		return errors.New("bot: play: nil engine")
	case b == nil:
		return errors.New("bot: play: nil bot")
	case e.Phase() != sim.PhaseIntro:
		return fmt.Errorf("bot: play: engine is in phase %s (want %s)", e.Phase(), sim.PhaseIntro)
	}
	if ack := e.Apply(sim.Command{Type: sim.CmdGameStart}); !ack.OK {
		return fmt.Errorf("bot: play: %s: %s: %s", sim.CmdGameStart, ack.Code, ack.Message)
	}
	for e.Phase() == sim.PhaseSailing {
		if err := b.Act(e); err != nil {
			return err
		}
		at, ok := e.NextNotableAt()
		e.RunUntil(nextWake(e.Snapshot(), at, ok, pollMs))
	}
	return nil
}

// nextWake は、次に Act を呼ぶ時刻を返す。s.T + poll・次の出来事（notableAt）・停泊時間の終わり（ReadyAt）のうち、
// s.T より後で最も早いもの。poll >= 1 なら、返す値は必ず s.T より大きい。純粋な関数。
func nextWake(s sim.Snapshot, notableAt sim.SimTime, hasNotable bool, poll sim.SimTime) sim.SimTime {
	next := s.T + poll
	if hasNotable && s.T < notableAt && notableAt < next {
		next = notableAt
	}
	for _, f := range s.Ferries {
		if f.State == "docked" && s.T < f.ReadyAt && f.ReadyAt < next {
			next = f.ReadyAt
		}
	}
	return next
}
