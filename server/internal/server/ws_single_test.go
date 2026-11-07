package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

// WS-1: internal.init → snapshot（seq 1）→ hello → snapshot（seq 2）。
func TestSingleInitSnapshot(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	c := ts.dialSingle(t)
	c.send(protocol.MessageTypeInternalInit, "", initPayload("s_test.1", "alpha", 7))
	env := c.expect(protocol.MessageTypeSnapshot, nil)
	if env.Seq != 1 {
		t.Errorf("seq = %d, want 1", env.Seq)
	}
	assertNoNullArrays(t, env.Payload)
	var snap protocol.Snapshot
	if err := json.Unmarshal(env.Payload, &snap); err != nil {
		t.Fatal(err)
	}
	st, _ := ts.srv.cat.Lookup("alpha")
	wantSession := protocol.Session{ID: "s_test.1", StageID: "alpha", Seed: 7, Demo: false, Restored: false,
		StageHash: st.Hash, Build: testBuild}
	if snap.Protocol != 1 || snap.Session != wantSession {
		t.Errorf("protocol %d session %+v, want %+v", snap.Protocol, snap.Session, wantSession)
	}
	if !strings.HasPrefix(snap.Session.StageHash, "sha256:") {
		t.Errorf("stageHash = %q", snap.Session.StageHash)
	}
	if snap.Phase != protocol.PhaseIntro || snap.Clock.T != 0 || snap.Clock.Paused || snap.Clock.Speed != 1 || snap.Clock.EndsAt != 180000 {
		t.Errorf("phase %s clock %+v", snap.Phase, snap.Clock)
	}
	if got := mustJSON(t, snap.Clock.AllowedSpeeds); got != "[0.5,1,2,4]" {
		t.Errorf("allowedSpeeds = %s", got)
	}
	if len(snap.Stage.Islands) != 3 || snap.Stage.Title != "テストの海" {
		t.Fatalf("stage = %+v", snap.Stage)
	}
	for i, is := range snap.Stage.Islands {
		if is.ID != uint64(i+1) || is.Name == "" || is.Color == "" || is.Population.Value <= 0 {
			t.Errorf("islands[%d] = %+v", i, is)
		}
	}
	if got := mustJSON(t, snap.Stage.Routes); got !=
		`[{"id":"1-2","a":1,"b":2,"travelMs":1500,"provenance":"derived"},{"id":"2-3","a":2,"b":3,"travelMs":4000,"provenance":"creative"}]` {
		t.Errorf("routes = %s", got)
	}
	if got := mustJSON(t, snap.Stage.Resources); got !=
		`[{"id":"firework:a","kind":"firework","label":"大花火","critical":true},{"id":"stall:main","kind":"stall","label":"屋台の出店枠","critical":false}]` {
		t.Errorf("resources = %s", got)
	}
	if snap.Score.Target != 2 || snap.Score.Total != 0 || len(snap.Requests) != 0 || len(snap.IslandsState) != 0 {
		t.Errorf("score %+v requests %v islandsState %v", snap.Score, snap.Requests, snap.IslandsState)
	}
	if len(snap.Nodes) != 3 || len(snap.Ferries) != 1 || len(snap.RoutesState) != 2 {
		t.Errorf("nodes %v ferries %v routesState %v", snap.Nodes, snap.Ferries, snap.RoutesState)
	}

	c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{Client: "test", Resume: false})
	if env := c.expect(protocol.MessageTypeSnapshot, nil); env.Seq != 2 {
		t.Errorf("seq = %d, want 2", env.Seq)
	}
}

// WS-2: cmd.game.start → ack（t: 0）→ events。
func TestSingleStartEvents(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	c := ts.dialSingle(t)
	c.initSingle("s_test.2", 7)
	c.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
	var ack protocol.Ack
	env := c.expect(protocol.MessageTypeAck, &ack)
	if env.Seq != 2 || ack != (protocol.Ack{CmdID: "c-1", OK: true, T: 0}) {
		t.Errorf("seq %d ack %+v", env.Seq, ack)
	}
	var ev protocol.EventsPayload
	env = c.expect(protocol.MessageTypeEvents, &ev)
	if env.Seq != 3 {
		t.Errorf("events seq = %d, want 3", env.Seq)
	}
	if ev.Clock != (protocol.Clock{T: 0, Speed: 1, Paused: false, EndsAt: 180000}) {
		t.Errorf("clock = %+v", ev.Clock)
	}
	types := map[string]bool{}
	var lastSeq uint64
	for i, raw := range ev.Events {
		if !bytes.HasPrefix(raw, []byte(`{"seq":`)) || !bytes.Contains(raw, []byte(`,"t":`)) || !bytes.Contains(raw, []byte(`,"type":`)) {
			t.Errorf("events[%d] = %s", i, raw)
		}
		var h struct {
			Seq  uint64 `json:"seq"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &h); err != nil {
			t.Fatal(err)
		}
		if h.Seq != lastSeq+1 {
			t.Errorf("events[%d].seq = %d, want %d", i, h.Seq, lastSeq+1)
		}
		lastSeq = h.Seq
		types[h.Type] = true
	}
	for _, typ := range []string{"phase.changed", "node.state", "node.leader_elected"} {
		if !types[typ] {
			t.Errorf("events has no %s: %v", typ, types)
		}
	}
	c.send(protocol.MessageTypePing, "", protocol.PingPayload{ClientTime: 5})
	if env := c.expect(protocol.MessageTypePong, nil); env.Seq != 4 {
		t.Errorf("pong seq = %d, want 4", env.Seq)
	}
}

// pingWithSize は、大きさがちょうど n バイトの ping の電文を作る（知らないキーで埋める）。
func pingWithSize(t *testing.T, n int) []byte {
	t.Helper()
	head := `{"v":1,"type":"ping","payload":{"clientTime":1,"pad":"`
	tail := `"}}`
	b := []byte(head + strings.Repeat("x", n-len(head)-len(tail)) + tail)
	if len(b) != n {
		t.Fatalf("len = %d", len(b))
	}
	return b
}

// inputLogLen は、セッションの Engine の入力ログの長さ（run の goroutine で読む）。
func inputLogLen(t *testing.T, s *session) int {
	t.Helper()
	var n int
	if !s.do(func() { n = len(s.engine.InputLog()) }) {
		t.Fatal("session stopped")
	}
	return n
}

// WS-3: 電文の規則。
func TestSingleMessages(t *testing.T) {
	t.Parallel()
	started := func(t *testing.T) (*testServer, *client) {
		ts := newTestServer(t, ModeSingle, nil)
		c := ts.dialSingle(t)
		c.initSingle("s_test.3", 7)
		c.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
		c.expect(protocol.MessageTypeAck, nil)
		c.expect(protocol.MessageTypeEvents, nil)
		return ts, c
	}

	t.Run("cmdId のない depart", func(t *testing.T) {
		t.Parallel()
		ts, c := started(t)
		before := inputLogLen(t, ts.singleSession(t))
		c.send(protocol.MessageTypeCmdFerryDepart, "", protocol.FerryToPayload{FerryID: "f1", To: 2})
		p := c.expectError(codeBadRequest, false)
		if p.Message != "cmdId is required" {
			t.Errorf("message = %q", p.Message)
		}
		c.pingPong()
		if after := inputLogLen(t, ts.singleSession(t)); after != before {
			t.Errorf("InputLog %d → %d", before, after)
		}
	})
	t.Run("失敗するコマンド", func(t *testing.T) {
		t.Parallel()
		_, c := started(t)
		c.send(protocol.MessageTypeCmdFerryDepart, "c-2", protocol.FerryToPayload{FerryID: "f1", To: 3})
		var ack protocol.Ack
		c.expect(protocol.MessageTypeAck, &ack)
		if ack.CmdID != "c-2" || ack.OK || ack.Code != protocol.AckCodeNoRoute || ack.T != 0 {
			t.Errorf("ack = %+v", ack)
		}
		expectEmptyEvents(t, c) // コマンドの後は clock のために events を送る（T-303）
		c.pingPong()
	})
	t.Run("2 回目の start", func(t *testing.T) {
		t.Parallel()
		_, c := started(t)
		c.send(protocol.MessageTypeCmdGameStart, "c-2", struct{}{})
		var ack protocol.Ack
		c.expect(protocol.MessageTypeAck, &ack)
		if ack.OK || ack.Code != protocol.AckCodePhaseInvalid {
			t.Errorf("ack = %+v", ack)
		}
		expectEmptyEvents(t, c)
		c.pingPong()
	})
	t.Run("壊れた JSON", func(t *testing.T) {
		t.Parallel()
		_, c := started(t)
		c.sendRaw(websocket.MessageText, []byte(`{"v":1,"type":`))
		c.expectError(codeBadRequest, false)
		c.sendRaw(websocket.MessageText, []byte(`{"v":1}`))
		c.expectError(codeBadRequest, false)
		c.pingPong()
	})
	t.Run("v: 2", func(t *testing.T) {
		t.Parallel()
		_, c := started(t)
		c.sendRaw(websocket.MessageText, []byte(`{"v":2,"type":"ping","payload":{}}`))
		c.expectFatal(codeVersionMismatch, websocket.StatusPolicyViolation)
	})
	t.Run("v がない", func(t *testing.T) {
		t.Parallel()
		_, c := started(t)
		c.sendRaw(websocket.MessageText, []byte(`{"type":"ping","payload":{}}`))
		c.expectFatal(codeVersionMismatch, websocket.StatusPolicyViolation)
	})
	t.Run("バイナリ", func(t *testing.T) {
		t.Parallel()
		_, c := started(t)
		c.sendRaw(websocket.MessageBinary, []byte(`{"v":1,"type":"ping","payload":{}}`))
		c.expectFatal(codeBadRequest, websocket.StatusUnsupportedData)
	})
	t.Run("16 KiB", func(t *testing.T) {
		t.Parallel()
		_, c := started(t)
		c.sendRaw(websocket.MessageText, pingWithSize(t, clientMessageLimit))
		c.expect(protocol.MessageTypePong, nil)
		// single は 1 MiB まで読み、internal.* 以外の 16 KiB を超える電文を bad_request で拒否する（続けられる）。
		c.sendRaw(websocket.MessageText, pingWithSize(t, clientMessageLimit+1))
		c.expectError(codeBadRequest, false)
		c.pingPong()
	})
	t.Run("1 MiB を超える", func(t *testing.T) {
		t.Parallel()
		_, c := started(t)
		c.sendRaw(websocket.MessageText, pingWithSize(t, singleReadLimit+1))
		c.expectClose(websocket.StatusMessageTooBig)
	})
}

// expectEmptyEvents は、次の電文が events: [] であることを確かめる。
func expectEmptyEvents(t *testing.T, c *client) {
	t.Helper()
	var ev protocol.EventsPayload
	env := c.expect(protocol.MessageTypeEvents, &ev)
	if ev.Events == nil || len(ev.Events) != 0 {
		t.Errorf("events = %s, want []", env.Payload)
	}
}

// WS-4: 接続の最初の電文。
func TestSingleFirstMessage(t *testing.T) {
	t.Parallel()
	t.Run("hello", func(t *testing.T) {
		t.Parallel()
		c := newTestServer(t, ModeSingle, nil).dialSingle(t)
		c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{Client: "x"})
		c.expectFatal(codeNotInitialized, websocket.StatusPolicyViolation)
	})
	t.Run("cmd.game.start", func(t *testing.T) {
		t.Parallel()
		c := newTestServer(t, ModeSingle, nil).dialSingle(t)
		c.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
		c.expectFatal(codeNotInitialized, websocket.StatusPolicyViolation)
	})
	t.Run("何も送らない", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t, ModeSingle, func(_ *Config, s *Server) { s.initTimeout = 50 * time.Millisecond })
		c := ts.dialSingle(t)
		c.expectFatal(codeNotInitialized, websocket.StatusPolicyViolation)
	})
	t.Run("internal.ping の後に init", func(t *testing.T) {
		t.Parallel()
		c := newTestServer(t, ModeSingle, nil).dialSingle(t)
		c.send(protocol.MessageTypeInternalPing, "", struct{}{})
		env := c.expect(protocol.MessageTypeInternalPong, nil)
		if string(env.Payload) != "{}" {
			t.Errorf("internal.pong payload = %s", env.Payload)
		}
		c.initSingle("s_test.4", 1)
	})
	t.Run("壊れた JSON の後に init", func(t *testing.T) {
		t.Parallel()
		c := newTestServer(t, ModeSingle, nil).dialSingle(t)
		c.sendRaw(websocket.MessageText, []byte(`nope`))
		c.expectError(codeBadRequest, false)
		c.initSingle("s_test.4", 1)
	})
}

// WS-5: つなぎ直し と session_mismatch。
func TestSingleReconnect(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	c1 := ts.dialSingle(t)
	c1.initSingle("s_test.5", 9)
	c1.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
	c1.expect(protocol.MessageTypeAck, nil)
	c1.expect(protocol.MessageTypeEvents, nil)
	sess := ts.singleSession(t)
	sess.do(func() { sess.advanceTo(3000) })

	c2 := ts.dialSingle(t)
	c2.send(protocol.MessageTypeInternalInit, "", initPayload("s_test.5", "alpha", 9))
	var snap protocol.Snapshot
	env := c2.expect(protocol.MessageTypeSnapshot, &snap)
	if env.Seq != 1 {
		t.Errorf("seq = %d, want 1（接続ごとに 1 から）", env.Seq)
	}
	if snap.Phase != protocol.PhaseSailing || snap.Clock.T != 3000 || snap.Session.Restored {
		t.Errorf("phase %s t %d restored %v（Engine を作り直していない）", snap.Phase, snap.Clock.T, snap.Session.Restored)
	}
	c1.expectFatal(codeReplaced, websocket.StatusNormalClosure)
	if ts.singleSession(t) != sess {
		t.Error("session was recreated")
	}

	c3 := ts.dialSingle(t)
	c3.send(protocol.MessageTypeInternalInit, "", initPayload("s_other", "alpha", 9))
	c3.expectFatal(codeSessionMismatch, websocket.StatusPolicyViolation)
	c2.pingPong()
}

// WS-5: 2 本の接続が同時に internal.init を送っても、セッションは 1 つだけできる。
func TestSingleConcurrentInit(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	c1, c2 := ts.dialSingle(t), ts.dialSingle(t)
	c1.send(protocol.MessageTypeInternalInit, "", initPayload("s_a", "alpha", 1))
	c2.send(protocol.MessageTypeInternalInit, "", initPayload("s_b", "alpha", 1))
	var oks, mismatches int
	for _, c := range []*client{c1, c2} {
		env := c.recv()
		switch env.Type {
		case protocol.MessageTypeSnapshot:
			oks++
		case protocol.MessageTypeError:
			var p protocol.ErrorPayload
			_ = json.Unmarshal(env.Payload, &p)
			if p.Code == codeSessionMismatch {
				mismatches++
			}
		}
	}
	if oks != 1 || mismatches != 1 {
		t.Errorf("snapshots %d, mismatches %d, want 1 and 1", oks, mismatches)
	}
}

// WS-6: 初期化した後の internal.init は already_initialized（続けられる）。
func TestSingleAlreadyInitialized(t *testing.T) {
	t.Parallel()
	c := newTestServer(t, ModeSingle, nil).dialSingle(t)
	c.initSingle("s_test.6", 1)
	c.send(protocol.MessageTypeInternalInit, "", initPayload("s_test.6", "alpha", 1))
	c.expectError(codeAlreadyInitialized, false)
	c.send(protocol.MessageTypeInternalRestore, "", protocol.InternalRestorePayload{InternalInitPayload: initPayload("s_test.6", "alpha", 1)})
	c.expectError(codeAlreadyInitialized, false)
	c.pingPong()
}

// WS-7: internal.ping・internal.result・internal.pong。
func TestSingleInternalMessages(t *testing.T) {
	t.Parallel()
	c := newTestServer(t, ModeSingle, nil).dialSingle(t)
	c.initSingle("s_test.7", 1)
	c.send(protocol.MessageTypeInternalPing, "", struct{}{})
	if env := c.expect(protocol.MessageTypeInternalPong, nil); string(env.Payload) != "{}" {
		t.Errorf("internal.pong payload = %s", env.Payload)
	}
	c.send(protocol.MessageTypeInternalResult, "", protocol.RoundResult{})
	c.send(protocol.MessageTypeInternalPong, "", struct{}{})
	c.pingPong()
}

// WS-8: stage_not_found と sessionId の形の誤り。
func TestSingleInitErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload any
		code    string
		close   websocket.StatusCode
	}{
		{"stage_not_found", initPayload("s_x", "nope", 1), codeStageNotFound, websocket.StatusInternalError},
		{"空の sessionId", initPayload("", "alpha", 1), codeBadRequest, websocket.StatusPolicyViolation},
		{"257 文字", initPayload(strings.Repeat("a", 257), "alpha", 1), codeBadRequest, websocket.StatusPolicyViolation},
		{"/ を含む", initPayload("s_a/b", "alpha", 1), codeBadRequest, websocket.StatusPolicyViolation},
		{"payload が配列", []int{}, codeBadRequest, websocket.StatusPolicyViolation},
		{"seed が文字列", map[string]any{"sessionId": "s_x", "stageId": "alpha", "seed": "1"}, codeBadRequest, websocket.StatusPolicyViolation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := newTestServer(t, ModeSingle, nil)
			c := ts.dialSingle(t)
			b, err := json.Marshal(map[string]any{"v": 1, "type": "internal.init", "payload": tt.payload})
			if err != nil {
				t.Fatal(err)
			}
			c.sendRaw(websocket.MessageText, b)
			c.expectFatal(tt.code, tt.close)
			// 枠は空のまま。次の接続で作れる。
			c2 := ts.dialSingle(t)
			c2.initSingle("s_ok", 1)
		})
	}
	// 256 文字はよい。
	c := newTestServer(t, ModeSingle, nil).dialSingle(t)
	c.initSingle(strings.Repeat("a", 256), 1)
}
