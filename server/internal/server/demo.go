package server

import (
	"errors"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
)

// handleDemoTrigger は demo.trigger（プロトコル §4・§4.1）を処理する。デモのセッションだけで受け付け、
// Engine のコマンド（sim.CmdDemoTrigger）として Apply する。嵐を起こすか・どの航路をどれだけ遮断するかは
// Engine がステージの demo.storm のとおりに決める（Intro・終了後は phase_invalid、demo.storm がなければ bad_request）。
// 成功した ack は Durable Object が入力ログに記録し、復元の Replay で同じ嵐が起きる。
func (s *session) handleDemoTrigger(env protocol.Envelope) {
	cmd, err := simconv.DecodeEngineCommand(env.Type, env.Payload)
	switch {
	case errors.Is(err, simconv.ErrBadPayload):
		s.reject(env, "bad payload")
		return
	case err != nil:
		panic("server: handleDemoTrigger: " + err.Error())
	case env.CmdID == "":
		// Durable Object は cmdId で ack と組にして入力ログに記録するので、記録されない操作を Engine に入れない。
		s.sendError(codeBadRequest, "cmdId is required")
		return
	case !s.demo:
		s.send(protocol.MessageTypeAck, protocol.Ack{
			CmdID:   env.CmdID,
			OK:      false,
			T:       int64(s.engine.Now()),
			Code:    protocol.AckCodeBadRequest,
			Message: "demo controls disabled",
		})
		return
	}
	s.applyCommand(env.CmdID, cmd)
}
