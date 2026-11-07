package server

import (
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
)

// エンジニアモードの購読（sub.engineer）と node.detail の送信（プロトコル §4・§5.4、T-407b）。
//
// 購読はセッションごとに 1 つで、いまの接続のもの。新しい接続（onAttach）・切断（onDetach）でやめ、
// クライアントは再接続して snapshot を受けた後に送り直す（プロトコル §10.5）。
// node.detail は Engine.NodeDetail を読むだけで作り、トレースに入れない（イベントではない）。
// そのため、購読してもしなくても Engine の結果（トレースのハッシュ）は変わらない。

// detailInterval は node.detail を送る間隔（プロトコル §4 の 2Hz。プロトコルが決めた値なのでコードに置く）。
const detailInterval = 500 * time.Millisecond

// engineerSub はエンジニアモードの購読（セッションごと。run の goroutine だけが触れる）。
type engineerSub struct {
	enabled    bool
	nodeID     uint64
	lastSentAt time.Time // 最後に node.detail を送った実時刻。ゼロ値なら次の機会にすぐ送る
}

// reset は購読をやめる（enabled: false・新しい接続・切断）。
func (e *engineerSub) reset() { *e = engineerSub{} }

// handleSubEngineer は sub.engineer を処理する（プロトコル §4）。
// payload の誤り・範囲外の nodeId は bad_request（cmdId があれば ack、なければ fatal でない error）で、購読の状態を変えない。
// 成功したとき、cmdId があれば ok: true の ack を返し、なければ何も返さない。
func (s *session) handleSubEngineer(env protocol.Envelope) {
	var enabled bool
	if !decodeField(env.Payload, "enabled", &enabled) {
		s.reject(env, "bad payload")
		return
	}
	if !enabled {
		s.eng.reset()
		s.ackSubEngineer(env)
		return
	}
	var nodeID uint64
	if !decodeField(env.Payload, "nodeId", &nodeID) {
		s.reject(env, "bad payload")
		return
	}
	if nodeID < 1 || nodeID > uint64(len(s.st.Islands)) {
		s.reject(env, "unknown node")
		return
	}
	s.eng = engineerSub{enabled: true, nodeID: nodeID}
	s.ackSubEngineer(env)
	// 保留のイベントを先に送ってから、詳細をすぐに 1 つ送る（送れる状態なら）。
	s.flushNow(false)
	s.tickEngineer(s.wall.Now())
}

// ackSubEngineer は、cmdId があれば ok: true の ack（t はいまのゲーム内時刻）を返す。
func (s *session) ackSubEngineer(env protocol.Envelope) {
	if env.CmdID == "" {
		return
	}
	s.send(protocol.MessageTypeAck, protocol.Ack{CmdID: env.CmdID, OK: true, T: int64(s.engine.Now())})
}

// tickEngineer は、送れるとき node.detail を 1 つ送る（まとめ送りの後と、購読した直後に呼ぶ）。
// 購読中・接続がある・いまの接続に snapshot を送った・前に送ってから detailInterval 以上経った・
// 送信キューが詰まっていない（events と同じ基準）・NodeDetail が返せる（Intro でない）のすべてを満たすときだけ送る。
// 一時停止中・ラウンドの終了後も、購読中なら送る（状態が変わらなくても送る）。
func (s *session) tickEngineer(now time.Time) {
	switch {
	case s.broken, !s.eng.enabled, s.conn == nil, !s.snapshotSent:
		return
	case !s.eng.lastSentAt.IsZero() && now.Sub(s.eng.lastSentAt) < detailInterval:
		return
	case s.conn.queued() >= s.highWater:
		return
	}
	d, ok := s.engine.NodeDetail(s.eng.nodeID)
	if !ok {
		return
	}
	s.send(protocol.MessageTypeNodeDetail, simconv.NodeDetailFromSim(d))
	s.eng.lastSentAt = now
}
