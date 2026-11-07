package sim

import (
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
)

// CmdDemoTrigger はデモ操作（プロトコル §4 の demo.trigger。payload は {event}）。今は event: "storm" だけ。
const CmdDemoTrigger CommandType = "demo.trigger"

// DemoEventStorm は demo.trigger の event の値（嵐を起こす）。
const DemoEventStorm = "storm"

// DemoStorm は発表者の操作で起こす嵐（ステージ定義の demo.storm。データ仕様 §5.3）。
type DemoStorm struct {
	Routes     [][2]uint64 // 遮断する航路（ノード ID の組。書いた順・向きのまま）
	DurationMs int64       // 遮断する長さ（1 以上）
}

// initDemo はデモの嵐の設定を検査して複製する（New の中。initStorms の後）。乱数を引かない。
// 航路は addStorm と同じく、向きを問わずに e.tr.Routes() から探す。
func (e *Engine) initDemo() error {
	ds := e.cfg.DemoStorm
	if ds == nil {
		return nil
	}
	switch {
	case ds.DurationMs < 1:
		return fmt.Errorf("sim: demo storm: durationMs %d < 1", ds.DurationMs)
	case len(ds.Routes) == 0:
		return fmt.Errorf("sim: demo storm: routes: empty")
	}
	routes := e.tr.Routes()
	for _, p := range ds.Routes {
		a, b := min(p[0], p[1]), max(p[0], p[1])
		if !slices.ContainsFunc(routes, func(r ferry.Route) bool { return r.A == a && r.B == b }) {
			return fmt.Errorf("sim: demo storm: %w: %d-%d", ferry.ErrNoRoute, p[0], p[1])
		}
	}
	e.cfg.DemoStorm = &DemoStorm{Routes: slices.Clone(ds.Routes), DurationMs: ds.DurationMs}
	return nil
}

// applyDemo は demo.trigger を適用する。失敗したら何も変えずにエラーコードと文を返す（成功なら code は ""）。
// 成功したら、いまの時刻から DurationMs だけデモの嵐の航路を遮断する（予報なし）。後処理は呼び出し側（Apply）。
func (e *Engine) applyDemo(cmd Command) (code, msg string) {
	switch {
	case cmd.Event != DemoEventStorm:
		return CodeBadRequest, fmt.Sprintf("sim: %s: unknown event %q", CmdDemoTrigger, cmd.Event)
	case e.cfg.DemoStorm == nil:
		return CodeBadRequest, fmt.Sprintf("sim: %s: stage has no demo.storm", CmdDemoTrigger)
	case e.phase != PhaseSailing:
		return CodePhaseInvalid, fmt.Sprintf("sim: %s in phase %s", CmdDemoTrigger, e.phase)
	}
	now := int64(e.Now())
	ds := e.cfg.DemoStorm
	s := game.Storm{Start: now, End: now + ds.DurationMs, Routes: slices.Clone(ds.Routes)}
	if _, err := e.raiseStorm(s); err != nil {
		// initDemo で検査済みなので起きない。
		panic(fmt.Sprintf("sim: demo storm: %v", err))
	}
	return "", ""
}
