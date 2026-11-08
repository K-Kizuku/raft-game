package server

import (
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/bot"
	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// autoFerryBot は自動航行の船を操作するボットの名前（T-724。強さは選べない）。
const autoFerryBot = "novice"

// autoPilot は自動航行（cmd.ferry.auto。T-724）の状態。run の goroutine だけが触れる。
//
// 船ごとに 1 つの novice（bot.NewFor で、その船だけを担当する）を持つ。ほかの船を切り替えても、
// 自動のままの船のボットの記憶（着いた時刻・来た島）は消えない。
type autoPilot struct {
	enabled bool               // 機能のスイッチ（single は internal.init の autoFerry、standalone は --auto-ferry）
	ids     []string           // 自動の船の ID（昇順。ボットを動かす順）
	bots    map[string]bot.Bot // 船の ID → その船だけを担当する novice
}

// set は船 id を自動（on）か手動にする。すでにその状態なら何もしない。
func (a *autoPilot) set(id string, on bool, seed uint64) error {
	_, has := a.bots[id]
	switch {
	case on && !has:
		b, err := bot.NewFor(autoFerryBot, seed, []string{id})
		if err != nil {
			return err
		}
		if a.bots == nil {
			a.bots = map[string]bot.Bot{}
		}
		a.bots[id] = b
		k, _ := slices.BinarySearch(a.ids, id)
		a.ids = slices.Insert(a.ids, k, id)
	case !on && has:
		delete(a.bots, id)
		k, _ := slices.BinarySearch(a.ids, id)
		a.ids = slices.Delete(a.ids, k, k+1)
	}
	return nil
}

// list は自動の船の ID（昇順）の複製。ひとつもなければ nil（snapshot の session.autoFerries を省く）。
func (a *autoPilot) list() []string {
	if len(a.ids) == 0 {
		return nil
	}
	return slices.Clone(a.ids)
}

// handleFerryAuto は cmd.ferry.auto（プロトコル §4）を処理する。成功したら ok: true の ack（t はいまの時刻）。
// Engine に触れない（入力ログに入らない）ので、Durable Object も記録しない。
func (s *session) handleFerryAuto(env protocol.Envelope) {
	if env.CmdID == "" {
		s.sendError(codeBadRequest, "cmdId is required")
		return
	}
	e := s.engine
	fail := func(code protocol.AckCode, message string) {
		s.send(protocol.MessageTypeAck, protocol.Ack{
			CmdID: env.CmdID, OK: false, T: int64(e.Now()), Code: code, Message: message,
		})
	}
	if !s.autos.enabled {
		fail(protocol.AckCodeFeatureDisabled, "auto ferry is disabled")
		return
	}
	var (
		id string
		on bool
	)
	if !decodeField(env.Payload, "ferryId", &id) || id == "" || !decodeField(env.Payload, "enabled", &on) {
		fail(protocol.AckCodeBadRequest, "bad payload")
		return
	}
	if !slices.ContainsFunc(s.st.Ferries, func(f stage.Ferry) bool { return f.ID == id }) {
		fail(protocol.AckCodeBadRequest, "unknown ferry")
		return
	}
	if e.Phase() == sim.PhaseEnded {
		fail(protocol.AckCodePhaseInvalid, "round has ended")
		return
	}
	if err := s.autos.set(id, on, s.seed); err != nil {
		panic(fmt.Sprintf("server: auto ferry %q: %v", id, err)) // 名前と船の ID は検査済みなので起きない
	}
	s.log.Info("auto ferry", "ferry", id, "enabled", on)
	s.send(protocol.MessageTypeAck, protocol.Ack{CmdID: env.CmdID, OK: true, T: int64(e.Now())})
}

// driveAutos は自動の船のボットに、いまの時刻の操作をさせる（ステップで Engine を進めた後）。
// 航行中でなければ何もしない（一時停止中・ラウンドの終わりはステップが呼ばない）。
//
// ボットが適用したコマンドは、プレイヤーのコマンドと同じく Engine の入力ログに載る。single では 1 件ずつ
// internal.input（payload は入力ログの 1 件 {t, cmd}）で Durable Object に知らせ、DO が入力ログに追記する
// （DesignDoc §10.1・プロトコル §9）。積めない・出航できない（嵐・故障など）ときの失敗の Ack はボットの誤りにせず、
// 次のステップでもう一度試させる。
func (s *session) driveAutos() {
	if len(s.autos.ids) == 0 || s.engine.Phase() != sim.PhaseSailing {
		return
	}
	before := len(s.engine.InputLog())
	for _, id := range s.autos.ids {
		if err := s.autos.bots[id].Act(s.engine); err != nil {
			s.log.Debug("auto ferry skipped", "ferry", id, "err", err.Error())
		}
	}
	log := s.engine.InputLog()
	if len(log) == before {
		return
	}
	for _, en := range log[before:] {
		s.reportInput(en)
	}
}

// reportInput は、ボットが適用したコマンド en を single の Durable Object に internal.input で知らせる。
// standalone は入力ログを持たないので送らない。
func (s *session) reportInput(en sim.InputEntry) {
	if s.mode != ModeSingle {
		return
	}
	cc, err := simconv.EncodeEngineCommand(en.Cmd)
	if err != nil {
		panic(fmt.Sprintf("server: encode bot command %q: %v", en.Cmd.Type, err)) // ボットは Engine のコマンドだけを出す
	}
	if s.conn == nil {
		// ステップは接続があるときだけ進む（切れたら自動で一時停止する）ので起きない。
		s.log.Error("auto ferry command not reported", "type", string(cc.Type), "t", int64(en.T))
		return
	}
	s.send(protocol.MessageTypeInternalInput, protocol.InputCommand{T: int64(en.T), Cmd: cc})
}
