package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

// ST-1: POST /api/sessions → WebSocket → hello → snapshot → cmd.game.start → ack と events。
func TestStandaloneFlow(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeStandalone, nil)
	cr := ts.createSession(t, `{"stageId":"alpha","seed":5}`)
	c := ts.dialSession(t, cr)
	c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{Client: "web/0.1.0"})
	var snap protocol.Snapshot
	env := c.expect(protocol.MessageTypeSnapshot, &snap)
	if env.Seq != 1 || snap.Session.ID != cr.SessionID || snap.Session.Restored || snap.Session.Seed != 5 ||
		snap.Session.Build != testBuild || snap.Phase != protocol.PhaseIntro {
		t.Errorf("seq %d snapshot session %+v phase %s", env.Seq, snap.Session, snap.Phase)
	}
	assertSnapshotIslands(t, env.Payload) // SV-1
	c.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
	var ack protocol.Ack
	c.expect(protocol.MessageTypeAck, &ack)
	if !ack.OK || ack.CmdID != "c-1" {
		t.Errorf("ack = %+v", ack)
	}
	c.expect(protocol.MessageTypeEvents, nil)
}

// ST-2: 2 つのセッションが別々に動く。
func TestStandaloneIsolation(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeStandalone, nil)
	a := ts.dialSession(t, ts.createSession(t, `{"stageId":"alpha"}`))
	b := ts.dialSession(t, ts.createSession(t, `{"stageId":"omega"}`))
	a.send(protocol.MessageTypeHello, "", protocol.HelloPayload{}) // standalone は hello の snapshot の後だけ events を送る（T-303）
	a.expect(protocol.MessageTypeSnapshot, nil)
	a.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
	a.expect(protocol.MessageTypeAck, nil)
	a.expect(protocol.MessageTypeEvents, nil)
	b.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
	var snap protocol.Snapshot
	b.expect(protocol.MessageTypeSnapshot, &snap)
	if snap.Phase != protocol.PhaseIntro || snap.Session.StageID != "omega" {
		t.Errorf("b: phase %s stage %s", snap.Phase, snap.Session.StageID)
	}
	a.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
	a.expect(protocol.MessageTypeSnapshot, &snap)
	if snap.Phase != protocol.PhaseSailing {
		t.Errorf("a: phase %s", snap.Phase)
	}
}

// ST-3: standalone は internal.* を破棄する（何も返さない）。
func TestStandaloneDropsInternal(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeStandalone, nil)
	c := ts.dialSession(t, ts.createSession(t, `{"stageId":"alpha"}`))
	c.send(protocol.MessageTypeInternalInit, "", initPayload("s_x", "alpha", 1))
	c.send(protocol.MessageTypeInternalRestore, "", protocol.InternalRestorePayload{InternalInitPayload: initPayload("s_x", "alpha", 1)})
	c.send(protocol.MessageTypeInternalPing, "", struct{}{})
	c.send(protocol.MessageTypeInternalResult, "i-1", protocol.RoundResult{})
	c.pingPong()
}

// ST-3: standalone の受信の上限は 16 KiB。
func TestStandaloneReadLimit(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeStandalone, nil)
	cr := ts.createSession(t, `{"stageId":"alpha"}`)
	c := ts.dialSession(t, cr)
	c.sendRaw(websocket.MessageText, pingWithSize(t, clientMessageLimit))
	c.expect(protocol.MessageTypePong, nil)
	c.sendRaw(websocket.MessageText, pingWithSize(t, clientMessageLimit+1))
	c.expectClose(websocket.StatusMessageTooBig)
	// セッションは残る。
	c2 := ts.dialSession(t, cr)
	c2.pingPong()
}

// waitDetached は、接続がすべて切れて（ハンドラーが終わって）セッションが切断を知るまで待つ。
func waitDetached(t *testing.T, ts *testServer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	ts.srv.waitHandlers(ctx)
	if ctx.Err() != nil {
		t.Fatal("handlers did not finish")
	}
}

// ST-4: 片付け。
func TestStandaloneSweep(t *testing.T) {
	t.Parallel()
	clock := newFakeClock(testStart)
	ts := newTestServer(t, ModeStandalone, func(c *Config, s *Server) {
		c.MaxSessions = 2
		s.now = clock.Now
	})
	h := ts.srv.standalone
	never := ts.createSession(t, `{"stageId":"alpha"}`) // 一度もつながらない
	used := ts.createSession(t, `{"stageId":"alpha"}`)
	res, _ := ts.do(t, http.MethodPost, "/api/sessions",
		http.Header{"Origin": {ts.origin()}, "Content-Type": {"application/json"}}, `{"stageId":"alpha"}`)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("3rd session = %d", res.StatusCode)
	}

	clock.Advance(4 * time.Minute)
	c := ts.dialSession(t, used)
	c.pingPong()
	_ = c.c.Close(websocket.StatusNormalClosure, "")
	waitDetached(t, ts)

	clock.Advance(time.Minute + time.Second) // never は作ってから 5 分 1 秒、used は切断から 1 分 1 秒
	if n := h.sweep(); n != 1 {
		t.Fatalf("sweep = %d, want 1", n)
	}
	assertGone(t, ts, never)
	c = ts.dialSession(t, used)
	c.pingPong()
	_ = c.c.Close(websocket.StatusNormalClosure, "")
	waitDetached(t, ts)

	ts.createSession(t, `{"stageId":"alpha"}`) // 枠が空いた

	clock.Advance(5*time.Minute - time.Second) // used は切断から 5 分より前
	if n := h.sweep(); n != 0 {
		t.Fatalf("sweep before 5 minutes = %d", n)
	}
	clock.Advance(2 * time.Second)
	if n := h.sweep(); n != 2 {
		t.Fatalf("sweep after 5 minutes = %d, want 2", n)
	}
	assertGone(t, ts, used)
	ts.createSession(t, `{"stageId":"alpha"}`)
	ts.createSession(t, `{"stageId":"alpha"}`)
}

func assertGone(t *testing.T, ts *testServer, cr protocol.CreateSessionResponse) {
	t.Helper()
	ctx, cancel := contextWithTimeout()
	defer cancel()
	_, res, err := websocket.Dial(ctx, ts.wsURL(cr.WSPath+"?token="+cr.Token),
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {ts.origin()}}})
	if err == nil || res == nil || res.StatusCode != http.StatusNotFound {
		t.Errorf("session %s is still there (%v)", cr.SessionID, err)
	}
}
