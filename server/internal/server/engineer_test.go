package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

// details は電文の列から node.detail の payload を取り出す。
func details(t *testing.T, envs []protocol.Envelope) []protocol.NodeDetail {
	t.Helper()
	var out []protocol.NodeDetail
	for _, env := range envs {
		if env.Type != protocol.MessageTypeNodeDetail {
			continue
		}
		var d protocol.NodeDetail
		if err := json.Unmarshal(env.Payload, &d); err != nil {
			t.Fatalf("decode node.detail %s: %v", env.Payload, err)
		}
		out = append(out, d)
	}
	return out
}

// subEngineer は sub.engineer を送る（cmdId なし）。
func subEngineer(c *client, enabled bool, nodeID uint64) {
	c.t.Helper()
	c.send(protocol.MessageTypeSubEngineer, "", protocol.SubEngineerPayload{Enabled: enabled, NodeID: nodeID})
}

// expectDetail は次の電文が node.detail で、id が一致することを確かめる。
func (c *client) expectDetail(id uint64) protocol.NodeDetail {
	c.t.Helper()
	var d protocol.NodeDetail
	c.expect(protocol.MessageTypeNodeDetail, &d)
	if d.ID != id {
		c.t.Fatalf("node.detail id = %d, want %d", d.ID, id)
	}
	return d
}

// detailCount は、まとめ送りを d 進めて発火し、届いた node.detail の数を返す。
func detailCount(t *testing.T, c *client, lt *loopTickers, d time.Duration) int {
	t.Helper()
	lt.flushAfter(d)
	envs, _ := c.collect()
	return len(details(t, envs))
}

// checkDetailEvery500ms は、まとめ送りを 100ms ずつ 5 回進めるごとに node.detail が 1 つ届く（4 回では届かない）ことを確かめる。
func checkDetailEvery500ms(t *testing.T, c *client, lt *loopTickers, rounds int) {
	t.Helper()
	for r := range rounds {
		for i := range 5 {
			want := 0
			if i == 4 {
				want = 1
			}
			if got := detailCount(t, c, lt, 100*time.Millisecond); got != want {
				t.Fatalf("round %d flush %d: %d node.detail, want %d", r, i+1, got, want)
			}
		}
	}
}

// EN-1・EN-2: 購読するとすぐに 1 つ、以後 500ms ごとに届く。enabled: false で止まる。
func TestEngineerDetail(t *testing.T) {
	t.Parallel()
	_, _, c, lt, sess := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
	lt.stepAfter(time.Second)
	lt.flushAfter(0)
	c.collect()

	subEngineer(c, true, 1)
	d := c.expectDetail(1)
	var n int
	probe(t, sess, func(s *session) { n = len(s.st.Islands) })
	if d.Role != protocol.RoleLeader || d.ElectionEstimate.Note != "推定値" || d.Log == nil || d.Ledger == nil {
		t.Errorf("node.detail = %+v", d)
	}
	if len(d.Progress) != n-1 {
		t.Errorf("progress = %+v, want %d followers", d.Progress, n-1)
	}
	c.pingPong() // ack は返さない

	checkDetailEvery500ms(t, c, lt, 2)

	// 一時停止中も送る。
	c.control(protocol.MessageTypeCmdGamePause, "p-1", protocol.GamePausePayload{Paused: true})
	checkDetailEvery500ms(t, c, lt, 1)

	subEngineer(c, false, 0)
	c.pingPong()
	for range 10 {
		if got := detailCount(t, c, lt, 100*time.Millisecond); got != 0 {
			t.Fatalf("after enabled: false: %d node.detail", got)
		}
	}
	probe(t, sess, func(s *session) {
		if s.eng.enabled {
			t.Errorf("eng = %+v after enabled: false", s.eng)
		}
	})
}

// EN-1: 別のノードに置き換えると、すぐにそのノードの詳細が届く。Follower の progress は出ない。
func TestEngineerSwitchNode(t *testing.T) {
	t.Parallel()
	_, _, c, lt, _ := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
	subEngineer(c, true, 1)
	c.expectDetail(1)
	lt.flushAfter(100 * time.Millisecond)
	c.pingPong()
	subEngineer(c, true, 2)
	env := c.expect(protocol.MessageTypeNodeDetail, nil)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(env.Payload, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["id"]) != "2" || string(raw["role"]) != `"follower"` {
		t.Errorf("node.detail = %s", env.Payload)
	}
	if _, ok := raw["progress"]; ok {
		t.Errorf("follower node.detail has progress: %s", env.Payload)
	}
	checkDetailEvery500ms(t, c, lt, 1)
}

// EN-3: cmdId 付きの成功は ack。誤りは bad_request で、前の購読が続く。
func TestEngineerErrors(t *testing.T) {
	t.Parallel()
	_, _, c, lt, sess := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
	var n int
	probe(t, sess, func(s *session) { n = len(s.st.Islands) })

	c.send(protocol.MessageTypeSubEngineer, "e-1", protocol.SubEngineerPayload{Enabled: true, NodeID: 1})
	if ack := c.expectAck("e-1"); !ack.OK || ack.Code != "" {
		t.Fatalf("ack = %+v, want ok", ack)
	}
	c.expectDetail(1)

	bad := []struct {
		payload string
		message string
	}{
		{`{"enabled":true,"nodeId":0}`, "unknown node"},
		{`{"enabled":true,"nodeId":` + itoa(n+1) + `}`, "unknown node"},
		{`{"enabled":true,"nodeId":"1"}`, "bad payload"},
		{`{"enabled":true,"nodeId":1.5}`, "bad payload"},
		{`{"enabled":true,"nodeId":-1}`, "bad payload"},
		{`{"enabled":true}`, "bad payload"},
		{`{"enabled":"true","nodeId":1}`, "bad payload"},
		{`{"nodeId":2}`, "bad payload"},
		{`{}`, "bad payload"},
		{`null`, "bad payload"},
		{`[]`, "bad payload"},
	}
	for _, b := range bad {
		c.sendPayload(protocol.MessageTypeSubEngineer, "e-2", b.payload)
		ack := c.expectAckFail("e-2", protocol.AckCodeBadRequest)
		if ack.Message != b.message {
			t.Errorf("%s: message = %q, want %q", b.payload, ack.Message, b.message)
		}
		c.sendPayload(protocol.MessageTypeSubEngineer, "", b.payload)
		if p := c.expectError(codeBadRequest, false); p.Message != b.message {
			t.Errorf("%s: error message = %q, want %q", b.payload, p.Message, b.message)
		}
	}
	c.pingPong()
	probe(t, sess, func(s *session) {
		if !s.eng.enabled || s.eng.nodeID != 1 {
			t.Errorf("eng = %+v after errors, want node 1", s.eng)
		}
	})
	checkDetailEvery500ms(t, c, lt, 1)

	// enabled: false は nodeId を見ない（範囲外でも成功）。購読していないときも成功する。
	for _, id := range []string{"e-3", "e-4"} {
		c.sendPayload(protocol.MessageTypeSubEngineer, id, `{"enabled":false,"nodeId":99,"extra":1}`)
		if ack := c.expectAck(id); !ack.OK {
			t.Fatalf("ack = %+v, want ok", ack)
		}
	}
	c.pingPong()
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// EN-4: Intro では届かず、cmd.game.start の後のまとめ送りで届く。
func TestEngineerIntro(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeStandalone, nil)
	cr := ts.createSession(t, `{"stageId":"alpha","seed":1}`)
	lt := ts.tickers(t)
	c := ts.dialSession(t, cr)

	// hello の前（snapshot を送っていない）は送らない。
	subEngineer(c, true, 1)
	c.pingPong()
	c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
	c.expect(protocol.MessageTypeSnapshot, nil)
	subEngineer(c, true, 1)
	c.pingPong()
	for range 6 {
		if got := detailCount(t, c, lt, 100*time.Millisecond); got != 0 {
			t.Fatalf("intro: %d node.detail", got)
		}
	}
	c.control(protocol.MessageTypeCmdGameStart, "start", struct{}{})
	c.pingPong()
	if got := detailCount(t, c, lt, 0); got != 1 {
		t.Fatalf("after start: %d node.detail, want 1", got)
	}
}

// EN-5: 接続を置き換える・切ってつなぎ直すと、送り直すまで届かない。
func TestEngineerResetOnReconnect(t *testing.T) {
	t.Parallel()
	t.Run("置き換え", func(t *testing.T) {
		t.Parallel()
		ts, cr, c, lt, _ := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
		subEngineer(c, true, 1)
		c.expectDetail(1)
		c2 := ts.dialSession(t, cr)
		c.expectFatal(codeReplaced, websocket.StatusNormalClosure)
		c2.send(protocol.MessageTypeHello, "", protocol.HelloPayload{Resume: true})
		c2.expect(protocol.MessageTypeSnapshot, nil)
		for range 6 {
			if got := detailCount(t, c2, lt, 100*time.Millisecond); got != 0 {
				t.Fatalf("after replace: %d node.detail", got)
			}
		}
		subEngineer(c2, true, 2)
		c2.expectDetail(2)
	})
	t.Run("切断", func(t *testing.T) {
		t.Parallel()
		ts, cr, c, lt, sess := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
		subEngineer(c, true, 1)
		c.expectDetail(1)
		_ = c.c.Close(websocket.StatusNormalClosure, "")
		waitProbe(t, sess, func(s *session) bool { return s.conn == nil })
		probe(t, sess, func(s *session) {
			if s.eng != (engineerSub{}) {
				t.Errorf("eng = %+v after detach", s.eng)
			}
		})
		c2 := ts.dialSession(t, cr)
		c2.send(protocol.MessageTypeHello, "", protocol.HelloPayload{Resume: true})
		c2.expect(protocol.MessageTypeSnapshot, nil)
		for range 6 {
			if got := detailCount(t, c2, lt, 100*time.Millisecond); got != 0 {
				t.Fatalf("after reconnect: %d node.detail", got)
			}
		}
		subEngineer(c2, true, 1)
		c2.expectDetail(1)
	})
}

// EN-6: 購読したセッションとしないセッションで、トレースのハッシュが一致する。
func TestEngineerDoesNotChangeTrace(t *testing.T) {
	t.Parallel()
	run := func(subscribe bool) ([32]byte, int) {
		_, _, c, lt, sess := startStandalone(t, nil, `{"stageId":"alpha","seed":3}`)
		if subscribe {
			subEngineer(c, true, 1)
			c.expectDetail(1)
		}
		count := 0
		for i := range 40 {
			lt.stepAfter(time.Second)
			lt.flushAfter(0)
			envs, _ := c.collect()
			count += len(details(t, envs))
			switch i {
			case 2:
				c.send(protocol.MessageTypeCmdFerryAutoload, "a-1", protocol.FerryToPayload{FerryID: "f1", To: 2})
				c.expectAck("a-1")
				c.collect()
			case 4:
				c.send(protocol.MessageTypeCmdFerryDepart, "d-1", protocol.FerryToPayload{FerryID: "f1", To: 2})
				c.expectAck("d-1")
				c.collect()
			case 20:
				if subscribe {
					subEngineer(c, true, 2)
					c.expectDetail(2)
				}
			}
		}
		var h [32]byte
		probe(t, sess, func(s *session) { h = s.engine.TraceHash() })
		return h, count
	}
	a, na := run(true)
	b, nb := run(false)
	if na < 30 || nb != 0 {
		t.Fatalf("node.detail counts = %d, %d", na, nb)
	}
	if a != b {
		t.Error("TraceHash differs between the subscribed session and the other")
	}
}

// EN-7: single モードでも届く。
func TestEngineerSingle(t *testing.T) {
	t.Parallel()
	_, c, lt, _ := startSingle(t, "s_en.7", 1)
	subEngineer(c, true, 1)
	c.expectDetail(1)
	c.pingPong()
	checkDetailEvery500ms(t, c, lt, 2)
}

// 送信キューが詰まっているときは送らず、空いた次のまとめ送りで送る。
func TestEngineerBackpressure(t *testing.T) {
	t.Parallel()
	_, _, c, lt, sess := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
	probe(t, sess, func(s *session) { s.highWater = 0 })
	subEngineer(c, true, 1)
	c.pingPong()
	if got := detailCount(t, c, lt, time.Second); got != 0 {
		t.Fatalf("queue full: %d node.detail", got)
	}
	probe(t, sess, func(s *session) { s.highWater = eventsHighWater })
	if got := detailCount(t, c, lt, 0); got != 1 {
		t.Fatalf("queue free: %d node.detail, want 1", got)
	}
}

// ラウンドの終了後も、購読中なら送り続ける。
func TestEngineerAfterRoundEnd(t *testing.T) {
	t.Parallel()
	_, _, c, lt, sess := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
	probe(t, sess, func(s *session) { s.advanceTo(s.durationMs) })
	c.collect()
	subEngineer(c, true, 1)
	c.expectDetail(1)
	checkDetailEvery500ms(t, c, lt, 1)
}
