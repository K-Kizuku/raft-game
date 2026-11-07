package server

import (
	"encoding/json"
	"math"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// pauseState は一時停止の状態。auto は「切断で自動的に止めた（プレイヤーは止めていない）」。
// 速度・一時停止はセッションループの中だけの値で、Engine の結果を変えない（DesignDoc §3.1）。
type pauseState struct{ paused, auto bool }

// set は cmd.game.pause: paused にし、auto = false。
func (p *pauseState) set(paused bool) { p.paused, p.auto = paused, false }

// hold は cmd.game.skip・復元: paused = true、auto = false。
func (p *pauseState) hold() { p.paused, p.auto = true, false }

// detach は、止まっていなければ paused = true・auto = true。止まっていれば何もしない。
func (p *pauseState) detach() {
	if !p.paused {
		p.paused, p.auto = true, true
	}
}

// attach は、auto なら paused = false・auto = false にして true を返す。それ以外は何もしない。
func (p *pauseState) attach() (resumed bool) {
	if !p.auto {
		return false
	}
	p.paused, p.auto = false, false
	return true
}

func (p *pauseState) isPaused() bool { return p.paused }

// allowedSpeedsPermille は、このセッションで使える速度（千分率、昇順、新しいスライス）。
// ステージの speed.allowed に、demo なら 8000 を足す（すでにあれば足さない）。
func allowedSpeedsPermille(st *stage.Stage, demo bool) []int64 {
	out := slices.Clone(st.SpeedAllowedPermille)
	if out == nil {
		out = []int64{}
	}
	if demo && !slices.Contains(out, demoSpeedPermille) {
		out = append(out, demoSpeedPermille)
	}
	slices.Sort(out)
	return out
}

// parseSpeedPermille は cmd.game.speed の speed を千分率にする。allowed にない値は false。
func parseSpeedPermille(v float64, allowed []int64) (int64, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0, false
	}
	x := v * 1000
	p := math.Round(x)
	if math.Abs(p-x) > 1e-6 || p > math.MaxInt32 {
		return 0, false
	}
	n := int64(p)
	if !slices.Contains(allowed, n) {
		return 0, false
	}
	return n, true
}

// skipTarget は cmd.game.skip で進める先。ok で、next − now が skipHorizon 以下なら (next, true)。
func skipTarget(now, next sim.SimTime, ok bool) (sim.SimTime, bool) {
	if !ok || next-now > skipHorizon {
		return 0, false
	}
	return next, true
}

// payloadField は payload（JSON のオブジェクト）の key の値を返す。オブジェクトでない・key がない・null なら false。
// 知らないキーは無視する（プロトコル §8）。
func payloadField(payload json.RawMessage, key string) (json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(payload, &m); err != nil || m == nil {
		return nil, false
	}
	raw, ok := m[key]
	if !ok || string(raw) == "null" {
		return nil, false
	}
	return raw, true
}

// decodeField は payload の key を v に読む（必須）。読めなければ false。
func decodeField(payload json.RawMessage, key string, v any) bool {
	raw, ok := payloadField(payload, key)
	if !ok {
		return false
	}
	return json.Unmarshal(raw, v) == nil
}

// handleControl は cmd.game.pause・cmd.game.speed・cmd.game.skip を処理する（プロトコル §4・§4.1）。
// 成功したら ok: true の ack（t は処理した後の Now()）→ 新しい clock の events → （skip で終わったら）round.result。
func (s *session) handleControl(env protocol.Envelope) {
	e := s.engine
	if env.CmdID == "" {
		s.sendError(codeBadRequest, "cmdId is required")
		return
	}
	fail := func(code protocol.AckCode, message string) {
		s.send(protocol.MessageTypeAck, protocol.Ack{
			CmdID: env.CmdID, OK: false, T: int64(e.Now()), Code: code, Message: message,
		})
	}
	switch env.Type {
	case protocol.MessageTypeCmdGamePause:
		var paused bool
		if !decodeField(env.Payload, "paused", &paused) {
			fail(protocol.AckCodeBadRequest, "bad payload")
			return
		}
		if e.Phase() == sim.PhaseEnded {
			fail(protocol.AckCodePhaseInvalid, "round has ended")
			return
		}
		s.pause.set(paused)
		if !paused {
			s.resetStep()
		}
	case protocol.MessageTypeCmdGameSpeed:
		var v float64
		if !decodeField(env.Payload, "speed", &v) {
			fail(protocol.AckCodeBadRequest, "bad payload")
			return
		}
		if e.Phase() == sim.PhaseEnded {
			fail(protocol.AckCodePhaseInvalid, "round has ended")
			return
		}
		p, ok := parseSpeedPermille(v, s.allowed)
		if !ok {
			fail(protocol.AckCodeBadRequest, "speed not allowed")
			return
		}
		s.speedPermille = p
		s.resetStep()
	case protocol.MessageTypeCmdGameSkip:
		var until string
		if !decodeField(env.Payload, "until", &until) || until != "next_notable" {
			fail(protocol.AckCodeBadRequest, "bad payload")
			return
		}
		if !s.demo {
			fail(protocol.AckCodeBadRequest, "demo controls disabled")
			return
		}
		if e.Phase() != sim.PhaseSailing {
			fail(protocol.AckCodePhaseInvalid, "not sailing")
			return
		}
		next, ok := e.NextNotableAt()
		if t, ok := skipTarget(e.Now(), next, ok); ok {
			// advanceTo を通さない（送る順を ack → events → round.result にするため）。
			e.RunUntil(t)
		}
		s.pause.hold()
		s.resetStep()
	default:
		panic("server: handleControl: unexpected type " + string(env.Type))
	}
	s.send(protocol.MessageTypeAck, protocol.Ack{CmdID: env.CmdID, OK: true, T: int64(e.Now())})
	s.flushNow(true)
	s.checkEnded()
}

// resetStep は、次のステップが止まっていた間の実時間を数えないように、ステップの起点を今にする
// （再開・速度の変更・skip の後）。
func (s *session) resetStep() {
	s.lastStep = s.wall.Now()
	s.rem = 0
}
