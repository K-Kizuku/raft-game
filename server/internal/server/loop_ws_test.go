package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// loopTickers は、セッション 1 つのステップとまとめ送りの偽のティッカー。
type loopTickers struct {
	t     *testing.T
	clock *fakeClock
	step  *fakeTicker
	flush *fakeTicker
}

// tickers は、次に作られたセッションのティッカーを待って返す（セッションを作った順に呼ぶ）。
func (ts *testServer) tickers(t *testing.T) *loopTickers {
	t.Helper()
	if ts.clock == nil {
		t.Fatal("the server does not use the fake clock")
	}
	ctx, cancel := contextWithTimeout()
	defer cancel()
	step := ts.clock.waitTicker(ctx, stepInterval)
	flush := ts.clock.waitTicker(ctx, flushInterval)
	if step == nil || flush == nil {
		t.Fatal("the session did not create its tickers")
	}
	return &loopTickers{t: t, clock: ts.clock, step: step, flush: flush}
}

func (lt *loopTickers) fire(tk *fakeTicker) {
	lt.t.Helper()
	ctx, cancel := contextWithTimeout()
	defer cancel()
	if err := tk.Fire(ctx); err != nil {
		lt.t.Fatalf("fire: %v", err)
	}
}

// stepAfter は時計を d 進めてステップを発火する。
func (lt *loopTickers) stepAfter(d time.Duration) {
	lt.t.Helper()
	lt.clock.Advance(d)
	lt.fire(lt.step)
}

// flushAfter は時計を d 進めてまとめ送りを発火する。
func (lt *loopTickers) flushAfter(d time.Duration) {
	lt.t.Helper()
	lt.clock.Advance(d)
	lt.fire(lt.flush)
}

// probe は f を run の goroutine で実行し、終わるのを待つ。
func probe(t *testing.T, s *session, f func(*session)) {
	t.Helper()
	ctx, cancel := contextWithTimeout()
	defer cancel()
	finished := make(chan struct{})
	select {
	case s.probe <- func(s *session) { defer close(finished); f(s) }:
	case <-s.done:
		t.Fatal("session stopped")
	case <-ctx.Done():
		t.Fatal("probe: timeout")
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("probe: timeout")
	}
}

// waitProbe は cond が真になるまで probe を繰り返す（上限は testTimeout）。
func waitProbe(t *testing.T, s *session, cond func(*session) bool) {
	t.Helper()
	ctx, cancel := contextWithTimeout()
	defer cancel()
	for ctx.Err() == nil {
		ok := false
		probe(t, s, func(s *session) { ok = cond(s) })
		if ok {
			return
		}
	}
	t.Fatal("waitProbe: timeout")
}

// simNow は run の goroutine で Engine の Now() を読む。
func simNow(t *testing.T, s *session) sim.SimTime {
	t.Helper()
	var now sim.SimTime
	probe(t, s, func(s *session) { now = s.engine.Now() })
	return now
}

// simTime は ping を送り、pong の simTime を返す（その間に何も届かないことも確かめる）。
func (c *client) simTime() int64 {
	c.t.Helper()
	c.send(protocol.MessageTypePing, "", protocol.PingPayload{ClientTime: 1})
	var p protocol.PongPayload
	c.expect(protocol.MessageTypePong, &p)
	return p.SimTime
}

// collect は ping を送り、pong の前に届いた電文と pong の simTime を返す。
func (c *client) collect() ([]protocol.Envelope, int64) {
	c.t.Helper()
	c.send(protocol.MessageTypePing, "", protocol.PingPayload{ClientTime: 1})
	var out []protocol.Envelope
	for {
		env := c.recv()
		if env.Type == protocol.MessageTypePong {
			var p protocol.PongPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				c.t.Fatal(err)
			}
			return out, p.SimTime
		}
		out = append(out, env)
	}
}

// expectAck は次の電文が cmdID の ack であることを確かめる。
func (c *client) expectAck(cmdID string) protocol.Ack {
	c.t.Helper()
	var ack protocol.Ack
	c.expect(protocol.MessageTypeAck, &ack)
	if ack.CmdID != cmdID {
		c.t.Fatalf("ack = %+v, want cmdId %s", ack, cmdID)
	}
	return ack
}

// expectAckFail は次の電文が ok: false の ack で、code が一致することを確かめる。
func (c *client) expectAckFail(cmdID string, code protocol.AckCode) protocol.Ack {
	c.t.Helper()
	ack := c.expectAck(cmdID)
	if ack.OK || ack.Code != code {
		c.t.Fatalf("ack = %+v, want ok: false code %s", ack, code)
	}
	return ack
}

// control は電文を送り、ok: true の ack と、続く events を返す。
func (c *client) control(typ protocol.MessageType, cmdID string, payload any) (protocol.Ack, protocol.EventsPayload) {
	c.t.Helper()
	c.send(typ, cmdID, payload)
	ack := c.expectAck(cmdID)
	if !ack.OK {
		c.t.Fatalf("%s: ack = %+v, want ok", typ, ack)
	}
	var ev protocol.EventsPayload
	env := c.expect(protocol.MessageTypeEvents, &ev)
	if ev.Events == nil {
		c.t.Errorf("events is null: %s", env.Payload)
	}
	return ack, ev
}

// sendPayload は payload をそのまま（JSON の文字列で）書いた電文を送る。
func (c *client) sendPayload(typ protocol.MessageType, cmdID, payload string) {
	c.t.Helper()
	c.sendRaw(websocket.MessageText,
		fmt.Appendf(nil, `{"v":1,"type":%q,"cmdId":%q,"payload":%s}`, typ, cmdID, payload))
}

// eventHead はイベントの seq・t・type。
type eventHead struct {
	Seq  uint64 `json:"seq"`
	T    int64  `json:"t"`
	Type string `json:"type"`
}

func heads(t *testing.T, raws []json.RawMessage) []eventHead {
	t.Helper()
	out := make([]eventHead, len(raws))
	for i, raw := range raws {
		if err := json.Unmarshal(raw, &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// eventsOf は電文の列から events の payload を取り出す。
func eventsOf(t *testing.T, envs []protocol.Envelope) []protocol.EventsPayload {
	t.Helper()
	var out []protocol.EventsPayload
	for _, env := range envs {
		if env.Type != protocol.MessageTypeEvents {
			continue
		}
		var p protocol.EventsPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func types(envs []protocol.Envelope) []protocol.MessageType {
	out := make([]protocol.MessageType, len(envs))
	for i, env := range envs {
		out[i] = env.Type
	}
	return out
}

// startStandalone は standalone のセッションを作り、hello → snapshot → cmd.game.start → ack・events まで進める。
func startStandalone(t *testing.T, edit func(*Config, *Server), body string) (*testServer, protocol.CreateSessionResponse, *client, *loopTickers, *session) {
	t.Helper()
	ts := newTestServer(t, ModeStandalone, edit)
	cr := ts.createSession(t, body)
	lt := ts.tickers(t)
	c := ts.dialSession(t, cr)
	c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
	c.expect(protocol.MessageTypeSnapshot, nil)
	c.control(protocol.MessageTypeCmdGameStart, "start", struct{}{})
	return ts, cr, c, lt, ts.standaloneSession(t, cr.SessionID)
}

// startSingle は single のセッションを internal.init で作り、cmd.game.start → ack・events まで進める。
func startSingle(t *testing.T, id string, seed uint64) (*testServer, *client, *loopTickers, *session) {
	t.Helper()
	ts := newTestServer(t, ModeSingle, nil)
	c := ts.dialSingle(t)
	c.initSingle(id, seed)
	lt := ts.tickers(t)
	c.control(protocol.MessageTypeCmdGameStart, "start", struct{}{})
	return ts, c, lt, ts.singleSession(t)
}

func demoEdit(c *Config, _ *Server) { c.DemoControls = true }

// SL-1: 進み方。
func TestLoopAdvance(t *testing.T) {
	t.Parallel()
	t.Run("standalone", func(t *testing.T) {
		t.Parallel()
		_, _, c, lt, _ := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
		lt.stepAfter(50 * time.Millisecond)
		if got := c.simTime(); got != 50 {
			t.Fatalf("1x 50ms: simTime = %d, want 50", got)
		}
		steps := []struct {
			speed   float64
			elapsed time.Duration
			want    int64
		}{
			{2, 50 * time.Millisecond, 150},
			{0.5, 50 * time.Millisecond, 175},
			{4, 5 * time.Second, 1175},
			{1, 0, 1175}, // 同じ時刻で発火 → 進めない
		}
		for i, s := range steps {
			_, ev := c.control(protocol.MessageTypeCmdGameSpeed, fmt.Sprintf("s-%d", i), protocol.GameSpeedPayload{Speed: s.speed})
			if ev.Clock.Speed != s.speed {
				t.Errorf("speed %v: clock = %+v", s.speed, ev.Clock)
			}
			lt.stepAfter(s.elapsed)
			if got := c.simTime(); got != s.want {
				t.Fatalf("speed %v after %v: simTime = %d, want %d", s.speed, s.elapsed, got, s.want)
			}
		}
		// 時計が戻ったら進めない。
		lt.stepAfter(-time.Second)
		if got := c.simTime(); got != 1175 {
			t.Fatalf("clock went back: simTime = %d", got)
		}
	})
	t.Run("single", func(t *testing.T) {
		t.Parallel()
		_, c, lt, _ := startSingle(t, "s_sl.1", 1)
		lt.stepAfter(50 * time.Millisecond)
		if got := c.simTime(); got != 50 {
			t.Fatalf("simTime = %d, want 50", got)
		}
	})
	t.Run("Intro では進まない", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t, ModeSingle, nil)
		c := ts.dialSingle(t)
		c.initSingle("s_sl.1b", 1)
		lt := ts.tickers(t)
		lt.stepAfter(time.Second)
		if got := c.simTime(); got != 0 {
			t.Fatalf("simTime in intro = %d", got)
		}
		// Intro で止まっていた 1 秒は、start の後のステップに持ち越さない（上限の 1 秒以内で、前のステップから数える）。
		c.control(protocol.MessageTypeCmdGameStart, "start", struct{}{})
		lt.stepAfter(50 * time.Millisecond)
		if got := c.simTime(); got != 50 {
			t.Fatalf("simTime = %d, want 50", got)
		}
	})
}

// SL-2: 一時停止。
func TestLoopPause(t *testing.T) {
	t.Parallel()
	_, _, c, lt, _ := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
	lt.stepAfter(50 * time.Millisecond)
	ack, ev := c.control(protocol.MessageTypeCmdGamePause, "p-1", protocol.GamePausePayload{Paused: true})
	if ack != (protocol.Ack{CmdID: "p-1", OK: true, T: 50}) {
		t.Errorf("ack = %+v", ack)
	}
	if ev.Clock != (protocol.Clock{T: 50, Speed: 1, Paused: true, EndsAt: 180000}) || len(ev.Events) != 0 {
		t.Errorf("events = %+v", ev)
	}
	lt.stepAfter(10 * time.Second)
	if got := c.simTime(); got != 50 {
		t.Fatalf("paused: simTime = %d, want 50", got)
	}
	// 止まっているときに paused: true → 成功（変わらない）。
	if _, ev := c.control(protocol.MessageTypeCmdGamePause, "p-2", protocol.GamePausePayload{Paused: true}); !ev.Clock.Paused {
		t.Errorf("clock = %+v", ev.Clock)
	}
	lt.clock.Advance(10 * time.Second) // 止まっていた間の実時間は数えない
	ack, ev = c.control(protocol.MessageTypeCmdGamePause, "p-3", protocol.GamePausePayload{Paused: false})
	if ack.T != 50 || ev.Clock.Paused {
		t.Errorf("ack %+v clock %+v", ack, ev.Clock)
	}
	lt.stepAfter(50 * time.Millisecond)
	if got := c.simTime(); got != 100 {
		t.Fatalf("resumed: simTime = %d, want 100", got)
	}

	// 読めない payload は bad_request で、状態を変えない。
	for i, p := range []string{`{}`, `{"paused":"true"}`, `[]`, `{"paused":null}`, `null`} {
		id := fmt.Sprintf("b-%d", i)
		c.sendPayload(protocol.MessageTypeCmdGamePause, id, p)
		if ack := c.expectAckFail(id, protocol.AckCodeBadRequest); ack.T != 100 {
			t.Errorf("%s: ack = %+v", p, ack)
		}
	}
	// 知らないキーは無視する。
	c.sendPayload(protocol.MessageTypeCmdGamePause, "x-1", `{"paused":false,"extra":1}`)
	if ack := c.expectAck("x-1"); !ack.OK {
		t.Errorf("ack = %+v", ack)
	}
	c.expect(protocol.MessageTypeEvents, nil)
	lt.stepAfter(50 * time.Millisecond)
	if got := c.simTime(); got != 150 {
		t.Fatalf("simTime = %d, want 150", got)
	}
}

// SL-3: 速度の検査。
func TestLoopSpeed(t *testing.T) {
	t.Parallel()
	t.Run("デモでない", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t, ModeStandalone, nil)
		cr := ts.createSession(t, `{"stageId":"alpha","seed":1}`)
		lt := ts.tickers(t)
		c := ts.dialSession(t, cr)
		c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
		var snap protocol.Snapshot
		c.expect(protocol.MessageTypeSnapshot, &snap)
		if got := mustJSON(t, snap.Clock.AllowedSpeeds); got != "[0.5,1,2,4]" {
			t.Errorf("allowedSpeeds = %s", got)
		}
		// Intro でも受け付ける。
		if _, ev := c.control(protocol.MessageTypeCmdGameSpeed, "i-1", protocol.GameSpeedPayload{Speed: 2}); ev.Clock.Speed != 2 {
			t.Errorf("clock = %+v", ev.Clock)
		}
		for i, p := range []string{`{"speed":3}`, `{"speed":0}`, `{"speed":-1}`, `{"speed":0.5004}`, `{"speed":"2"}`, `{}`, `{"speed":8}`, `[]`} {
			id := fmt.Sprintf("b-%d", i)
			c.sendPayload(protocol.MessageTypeCmdGameSpeed, id, p)
			c.expectAckFail(id, protocol.AckCodeBadRequest)
		}
		c.sendPayload(protocol.MessageTypeCmdGameSpeed, "m-1", `{"speed":8}`)
		if ack := c.expectAckFail("m-1", protocol.AckCodeBadRequest); ack.Message != "speed not allowed" {
			t.Errorf("message = %q", ack.Message)
		}
		c.control(protocol.MessageTypeCmdGameStart, "start", struct{}{})
		lt.stepAfter(50 * time.Millisecond)
		if got := c.simTime(); got != 100 {
			t.Fatalf("2x after rejected speeds: simTime = %d, want 100", got)
		}

		// 一時停止中に速度を変える → 止まったまま。再開したら新しい速度で進む。
		c.control(protocol.MessageTypeCmdGamePause, "p-1", protocol.GamePausePayload{Paused: true})
		if _, ev := c.control(protocol.MessageTypeCmdGameSpeed, "s-4", protocol.GameSpeedPayload{Speed: 4}); !ev.Clock.Paused || ev.Clock.Speed != 4 {
			t.Errorf("clock = %+v", ev.Clock)
		}
		lt.stepAfter(time.Second)
		if got := c.simTime(); got != 100 {
			t.Fatalf("paused: simTime = %d", got)
		}
		c.control(protocol.MessageTypeCmdGamePause, "p-2", protocol.GamePausePayload{Paused: false})
		lt.stepAfter(50 * time.Millisecond)
		if got := c.simTime(); got != 300 {
			t.Fatalf("4x: simTime = %d, want 300", got)
		}

		// ラウンドの終了後は phase_invalid。
		sess := ts.standaloneSession(t, cr.SessionID)
		probe(t, sess, func(s *session) { s.advanceTo(s.durationMs) })
		envs, _ := c.collect()
		if got := types(envs); len(got) != 2 || got[1] != protocol.MessageTypeRoundResult {
			t.Fatalf("after the end: %v", got)
		}
		c.send(protocol.MessageTypeCmdGameSpeed, "e-1", protocol.GameSpeedPayload{Speed: 1})
		c.expectAckFail("e-1", protocol.AckCodePhaseInvalid)
		c.pingPong()
	})
	t.Run("デモ", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t, ModeStandalone, demoEdit)
		cr := ts.createSession(t, `{"stageId":"alpha","seed":1,"demo":true}`)
		lt := ts.tickers(t)
		c := ts.dialSession(t, cr)
		c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
		var snap protocol.Snapshot
		c.expect(protocol.MessageTypeSnapshot, &snap)
		if got := mustJSON(t, snap.Clock.AllowedSpeeds); got != "[0.5,1,2,4,8]" || !snap.Session.Demo {
			t.Errorf("allowedSpeeds = %s demo %v", got, snap.Session.Demo)
		}
		c.control(protocol.MessageTypeCmdGameStart, "start", struct{}{})
		if _, ev := c.control(protocol.MessageTypeCmdGameSpeed, "s-8", protocol.GameSpeedPayload{Speed: 8}); ev.Clock.Speed != 8 {
			t.Errorf("clock = %+v", ev.Clock)
		}
		lt.stepAfter(50 * time.Millisecond)
		if got := c.simTime(); got != 400 {
			t.Fatalf("8x: simTime = %d, want 400", got)
		}
		c.send(protocol.MessageTypeCmdSync, "", struct{}{})
		c.expect(protocol.MessageTypeSnapshot, &snap)
		if snap.Clock.Speed != 8 {
			t.Errorf("snapshot clock = %+v", snap.Clock)
		}
	})
}

// refStageEngine は alpha の参照の Engine（seed を選べる）。
func refStageEngine(t *testing.T, seed uint64) (*sim.Engine, *stage.Stage) {
	t.Helper()
	st, _ := testCatalog(t).Lookup("alpha")
	cfg, err := stage.SimConfig(st)
	if err != nil {
		t.Fatal(err)
	}
	e, err := sim.New(cfg, seed, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return e, st
}

// quietTime は、船を動かさないときに、次の出来事が 30 秒より先（かない）になる最初の時刻（1 秒刻み）。
func quietTime(t *testing.T, seed uint64) sim.SimTime {
	t.Helper()
	e, st := refStageEngine(t, seed)
	e.Apply(sim.Command{Type: sim.CmdGameStart})
	for at := sim.SimTime(0); at < sim.SimTime(st.DurationMs)-skipHorizon; at += sim.Second {
		e.RunUntil(at)
		if next, ok := e.NextNotableAt(); !ok || next-at > skipHorizon {
			return at
		}
	}
	t.Fatal("no quiet time in alpha")
	return 0
}

// SL-4: 次の出来事まで（デモのセッション）。
func TestLoopSkip(t *testing.T) {
	t.Parallel()
	t.Run("デモ", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t, ModeStandalone, demoEdit)
		cr := ts.createSession(t, `{"stageId":"alpha","seed":1,"demo":true}`)
		lt := ts.tickers(t)
		c := ts.dialSession(t, cr)
		sess := ts.standaloneSession(t, cr.SessionID)
		c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
		c.expect(protocol.MessageTypeSnapshot, nil)
		c.send(protocol.MessageTypeCmdGameSkip, "k-0", protocol.GameSkipPayload{Until: "next_notable"})
		c.expectAckFail("k-0", protocol.AckCodePhaseInvalid) // Intro
		c.control(protocol.MessageTypeCmdGameStart, "start", struct{}{})

		_, ev := c.control(protocol.MessageTypeCmdFerryDepart, "d-1", protocol.FerryToPayload{FerryID: "f1", To: 2})
		var arriveAt int64
		for _, raw := range ev.Events {
			var d struct {
				Type     string `json:"type"`
				ArriveAt int64  `json:"arriveAt"`
			}
			_ = json.Unmarshal(raw, &d)
			if d.Type == "ferry.departed" {
				arriveAt = d.ArriveAt
			}
		}
		if arriveAt <= 0 {
			t.Fatalf("no ferry.departed: %s", mustJSON(t, ev))
		}
		ack, ev := c.control(protocol.MessageTypeCmdGameSkip, "k-1", protocol.GameSkipPayload{Until: "next_notable"})
		if ack.T != arriveAt {
			t.Errorf("ack.t = %d, want %d", ack.T, arriveAt)
		}
		arrived := false
		for _, h := range heads(t, ev.Events) {
			arrived = arrived || h.Type == "ferry.arrived"
		}
		if !arrived || !ev.Clock.Paused || ev.Clock.T != arriveAt {
			t.Errorf("events after skip: %s", mustJSON(t, ev))
		}
		lt.stepAfter(time.Second)
		if got := c.simTime(); got != arriveAt {
			t.Fatalf("after skip: simTime = %d, want %d（止まったまま）", got, arriveAt)
		}

		c.send(protocol.MessageTypeCmdGameSkip, "k-2", protocol.GameSkipPayload{Until: "foo"})
		c.expectAckFail("k-2", protocol.AckCodeBadRequest)
		c.sendPayload(protocol.MessageTypeCmdGameSkip, "k-3", `{}`)
		c.expectAckFail("k-3", protocol.AckCodeBadRequest)

		// 次の出来事が 30 秒より先なら、進めずに止まって成功する。
		quiet := quietTime(t, 1)
		probe(t, sess, func(s *session) { s.advanceTo(quiet) })
		c.collect()
		ack, ev = c.control(protocol.MessageTypeCmdGameSkip, "k-4", protocol.GameSkipPayload{Until: "next_notable"})
		if ack.T != int64(quiet) || !ev.Clock.Paused || ev.Clock.T != int64(quiet) {
			t.Errorf("skip with nothing ahead: ack %+v clock %+v (quiet %d)", ack, ev.Clock, quiet)
		}
	})
	t.Run("デモでない", func(t *testing.T) {
		t.Parallel()
		_, _, c, _, _ := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
		c.send(protocol.MessageTypeCmdGameSkip, "k-1", protocol.GameSkipPayload{Until: "next_notable"})
		if ack := c.expectAckFail("k-1", protocol.AckCodeBadRequest); ack.Message != "demo controls disabled" {
			t.Errorf("message = %q", ack.Message)
		}
		c.pingPong()
	})
	t.Run("DurationMs ちょうどの到着", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t, ModeStandalone, demoEdit)
		cr := ts.createSession(t, `{"stageId":"alpha","seed":1,"demo":true}`)
		ts.tickers(t)
		c := ts.dialSession(t, cr)
		sess := ts.standaloneSession(t, cr.SessionID)
		c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
		c.expect(protocol.MessageTypeSnapshot, nil)
		c.control(protocol.MessageTypeCmdGameStart, "start", struct{}{})
		const travel = 1500 // alpha の 1-2 の航路
		probe(t, sess, func(s *session) { s.advanceTo(s.durationMs - travel) })
		c.collect()
		_, ev := c.control(protocol.MessageTypeCmdFerryDepart, "d-1", protocol.FerryToPayload{FerryID: "f1", To: 2})
		if !strings.Contains(mustJSON(t, ev.Events), `"arriveAt":180000`) {
			t.Fatalf("departure does not arrive at the end: %s", mustJSON(t, ev.Events))
		}
		ack, ev := c.control(protocol.MessageTypeCmdGameSkip, "k-1", protocol.GameSkipPayload{Until: "next_notable"})
		if ack.T != 180000 || !strings.Contains(mustJSON(t, ev.Events), `"type":"phase.changed","phase":"ended"`) {
			t.Errorf("ack %+v events %s", ack, mustJSON(t, ev.Events))
		}
		c.expect(protocol.MessageTypeRoundResult, nil)
		c.pingPong()
		c.send(protocol.MessageTypeCmdGameSkip, "k-2", protocol.GameSkipPayload{Until: "next_notable"})
		c.expectAckFail("k-2", protocol.AckCodePhaseInvalid)
	})
}

// SL-5: まとめ送り。
func TestLoopFlush(t *testing.T) {
	t.Parallel()
	_, _, c, lt, sess := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
	probe(t, sess, func(s *session) { s.advanceTo(4500) })
	lt.flushAfter(0)
	c.collect()

	lt.stepAfter(time.Second) // 5500（依頼 5000）
	lt.stepAfter(time.Second) // 6500（依頼 6000）
	if got := c.simTime(); got != 6500 {
		t.Fatalf("simTime = %d", got) // まとめ送りの前なので events は届かない（pong が先）
	}
	lt.flushAfter(0)
	envs, _ := c.collect()
	if len(envs) != 1 || envs[0].Type != protocol.MessageTypeEvents {
		t.Fatalf("after flush: %v", types(envs))
	}
	ev := eventsOf(t, envs)[0]
	hs := heads(t, ev.Events)
	var first, second bool
	for i, h := range hs {
		if i > 0 && h.Seq != hs[i-1].Seq+1 {
			t.Errorf("seq is not contiguous: %v", hs)
		}
		first = first || (h.T > 4500 && h.T <= 5500)
		second = second || (h.T > 5500 && h.T <= 6500)
	}
	if !first || !second || ev.Clock.T != 6500 {
		t.Errorf("events do not cover both steps: %v clock %+v", hs, ev.Clock)
	}

	// イベントがない 500ms → 送らない。1 秒たったら events: []。
	lt.flushAfter(500 * time.Millisecond)
	c.pingPong()
	lt.flushAfter(499 * time.Millisecond)
	c.pingPong()
	lt.flushAfter(time.Millisecond)
	envs, _ = c.collect()
	if len(envs) != 1 {
		t.Fatalf("heartbeat: %v", types(envs))
	}
	if ev := eventsOf(t, envs)[0]; len(ev.Events) != 0 || ev.Clock.T != 6500 {
		t.Errorf("heartbeat = %s", envs[0].Payload)
	}
	// 止まっている間も 1 秒ごとに届く。
	c.control(protocol.MessageTypeCmdGamePause, "p-1", protocol.GamePausePayload{Paused: true})
	lt.flushAfter(time.Second)
	envs, _ = c.collect()
	if len(envs) != 1 || !eventsOf(t, envs)[0].Clock.Paused {
		t.Errorf("heartbeat while paused: %v", types(envs))
	}
}

// SL-6: 自動一時停止（standalone）。
func TestLoopAutoPauseStandalone(t *testing.T) {
	t.Parallel()
	t.Run("切断で止まり、つなぎ直すと動く", func(t *testing.T) {
		t.Parallel()
		ts, cr, c, lt, sess := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
		lt.stepAfter(50 * time.Millisecond)
		_ = c.c.Close(websocket.StatusNormalClosure, "")
		waitProbe(t, sess, func(s *session) bool { return s.pause == pauseState{paused: true, auto: true} })
		lt.stepAfter(time.Second)
		if got := simNow(t, sess); got != 50 {
			t.Fatalf("disconnected: Now = %d, want 50", got)
		}
		lt.clock.Advance(10 * time.Second)
		c2 := ts.dialSession(t, cr)
		c2.send(protocol.MessageTypeHello, "", protocol.HelloPayload{Resume: true})
		var snap protocol.Snapshot
		c2.expect(protocol.MessageTypeSnapshot, &snap)
		if snap.Clock.Paused || snap.Clock.T != 50 {
			t.Errorf("snapshot clock = %+v", snap.Clock)
		}
		lt.stepAfter(50 * time.Millisecond)
		if got := c2.simTime(); got != 100 {
			t.Fatalf("reconnected: simTime = %d, want 100", got)
		}
		if logs := ts.logs.String(); !strings.Contains(logs, `"msg":"auto pause"`) || !strings.Contains(logs, `"msg":"auto resume"`) {
			t.Errorf("logs do not have auto pause/resume:\n%s", logs)
		}
	})
	t.Run("プレイヤーが止めていたら止まったまま", func(t *testing.T) {
		t.Parallel()
		ts, cr, c, lt, sess := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
		c.control(protocol.MessageTypeCmdGamePause, "p-1", protocol.GamePausePayload{Paused: true})
		_ = c.c.Close(websocket.StatusNormalClosure, "")
		waitProbe(t, sess, func(s *session) bool { return s.conn == nil })
		probe(t, sess, func(s *session) {
			if s.pause != (pauseState{paused: true}) {
				t.Errorf("pause = %+v", s.pause)
			}
		})
		c2 := ts.dialSession(t, cr)
		c2.send(protocol.MessageTypeHello, "", protocol.HelloPayload{Resume: true})
		var snap protocol.Snapshot
		c2.expect(protocol.MessageTypeSnapshot, &snap)
		if !snap.Clock.Paused {
			t.Errorf("snapshot clock = %+v", snap.Clock)
		}
		lt.stepAfter(time.Second)
		if got := c2.simTime(); got != 0 {
			t.Fatalf("simTime = %d", got)
		}
	})
	t.Run("置き換えでは止めない", func(t *testing.T) {
		t.Parallel()
		ts, cr, c, lt, _ := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
		c2 := ts.dialSession(t, cr)
		c.expectFatal(codeReplaced, websocket.StatusNormalClosure)
		c2.send(protocol.MessageTypeHello, "", protocol.HelloPayload{Resume: true})
		var snap protocol.Snapshot
		c2.expect(protocol.MessageTypeSnapshot, &snap)
		if snap.Clock.Paused {
			t.Errorf("snapshot clock = %+v", snap.Clock)
		}
		lt.stepAfter(50 * time.Millisecond)
		if got := c2.simTime(); got != 50 {
			t.Fatalf("simTime = %d", got)
		}
	})
	t.Run("Ended の後は何もしない", func(t *testing.T) {
		t.Parallel()
		_, _, c, _, sess := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
		probe(t, sess, func(s *session) { s.advanceTo(s.durationMs) })
		c.collect()
		_ = c.c.Close(websocket.StatusNormalClosure, "")
		waitProbe(t, sess, func(s *session) bool { return s.conn == nil })
		probe(t, sess, func(s *session) {
			if s.pause.isPaused() {
				t.Errorf("pause = %+v", s.pause)
			}
		})
	})
}

// SL-7: single の自動一時停止。
func TestLoopAutoPauseSingle(t *testing.T) {
	t.Parallel()
	t.Run("init", func(t *testing.T) {
		t.Parallel()
		ts, c, lt, sess := startSingle(t, "s_sl.7", 1)
		lt.stepAfter(50 * time.Millisecond)
		_ = c.c.Close(websocket.StatusNormalClosure, "")
		waitProbe(t, sess, func(s *session) bool { return s.pause == pauseState{paused: true, auto: true} })
		c2 := ts.dialSingle(t)
		snap := c2.initSingle("s_sl.7", 1)
		if snap.Clock.Paused || snap.Clock.T != 50 {
			t.Errorf("snapshot clock = %+v", snap.Clock)
		}
		lt.stepAfter(50 * time.Millisecond)
		if got := c2.simTime(); got != 100 {
			t.Fatalf("simTime = %d", got)
		}
	})
	t.Run("restore", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t, ModeSingle, nil)
		ref := newRefEngine(t, ts.srv.cat)
		ref.play()
		c := ts.dialSingle(t)
		c.send(protocol.MessageTypeInternalRestore, "", restorePayload("s_sl.7r", ref.inputLog(testBuild), 2500))
		var snap protocol.Snapshot
		c.expect(protocol.MessageTypeSnapshot, &snap)
		if !snap.Clock.Paused {
			t.Fatalf("restored clock = %+v", snap.Clock)
		}
		lt := ts.tickers(t)
		sess := ts.singleSession(t)
		_ = c.c.Close(websocket.StatusNormalClosure, "")
		waitProbe(t, sess, func(s *session) bool { return s.conn == nil })
		c2 := ts.dialSingle(t)
		c2.send(protocol.MessageTypeInternalInit, "", initPayload("s_sl.7r", "alpha", restoreSeed))
		c2.expect(protocol.MessageTypeSnapshot, &snap)
		if !snap.Clock.Paused || !snap.Session.Restored {
			t.Errorf("snapshot clock %+v session %+v", snap.Clock, snap.Session)
		}
		lt.stepAfter(time.Second)
		if got := c2.simTime(); got != 2500 {
			t.Fatalf("simTime = %d", got)
		}
		// ▶ で動く。
		c2.control(protocol.MessageTypeCmdGamePause, "p-1", protocol.GamePausePayload{Paused: false})
		lt.stepAfter(50 * time.Millisecond)
		if got := c2.simTime(); got != 2550 {
			t.Fatalf("simTime = %d", got)
		}
	})
}

// checkAfter は、events の列のイベントの seq がすべて lastSeq より大きく、最初が lastSeq+1 であることを確かめる。
// イベントが 1 つもなければ false。
func checkAfter(t *testing.T, evs []protocol.EventsPayload, lastSeq uint64) bool {
	t.Helper()
	var hs []eventHead
	for _, ev := range evs {
		hs = append(hs, heads(t, ev.Events)...)
	}
	if len(hs) == 0 {
		return false
	}
	if hs[0].Seq != lastSeq+1 {
		t.Errorf("first seq = %d, want %d", hs[0].Seq, lastSeq+1)
	}
	for _, h := range hs {
		if h.Seq <= lastSeq {
			t.Errorf("event seq %d <= snapshot lastSeq %d", h.Seq, lastSeq)
		}
	}
	return true
}

// SL-8: snapshot と保留。
func TestLoopSnapshotPending(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeStandalone, nil)
	cr := ts.createSession(t, `{"stageId":"alpha","seed":1}`)
	lt := ts.tickers(t)
	c := ts.dialSession(t, cr)
	sess := ts.standaloneSession(t, cr.SessionID)
	// hello の前: コマンドの ack は届くが、events は届かない。
	c.send(protocol.MessageTypeCmdGameStart, "start", struct{}{})
	c.expectAck("start")
	c.pingPong()
	lt.stepAfter(time.Second)
	lt.flushAfter(2 * time.Second)
	c.pingPong()

	c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	if snap.LastSeq == 0 || snap.Clock.T != 1000 {
		t.Fatalf("snapshot lastSeq %d clock %+v", snap.LastSeq, snap.Clock)
	}
	probe(t, sess, func(s *session) { s.advanceTo(5500) })
	lt.flushAfter(0)
	envs, _ := c.collect()
	if !checkAfter(t, eventsOf(t, envs), snap.LastSeq) {
		t.Fatalf("no events after hello: %v", types(envs))
	}

	// cmd.sync でも同じ。
	probe(t, sess, func(s *session) { s.advanceTo(6500) })
	c.send(protocol.MessageTypeCmdSync, "s-1", struct{}{})
	c.expectAck("s-1")
	c.expect(protocol.MessageTypeSnapshot, &snap)
	lt.flushAfter(0)
	c.pingPong() // snapshot に含まれたイベントは送らない
	probe(t, sess, func(s *session) { s.advanceTo(20000) })
	lt.flushAfter(0)
	envs, _ = c.collect()
	if !checkAfter(t, eventsOf(t, envs), snap.LastSeq) {
		t.Fatalf("no events after cmd.sync: %v", types(envs))
	}
}

// SL-9: 送信の詰まり。
func TestLoopBackpressure(t *testing.T) {
	t.Parallel()
	ts, _, c, lt, sess := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
	probe(t, sess, func(s *session) { s.highWater = 0; s.pendingLimit = 5 })
	dropped := false
	for range 60 {
		lt.stepAfter(time.Second)
		lt.flushAfter(0)
		probe(t, sess, func(s *session) {
			if s.needSnapshot {
				dropped = true
				if len(s.pending) != 0 {
					t.Errorf("pending = %d after the drop", len(s.pending))
				}
			}
		})
		if dropped {
			break
		}
	}
	if !dropped {
		t.Fatal("pending events were not dropped")
	}
	c.pingPong() // events は届いていない
	if !strings.Contains(ts.logs.String(), `"msg":"pending events dropped"`) {
		t.Error("no drop log")
	}

	probe(t, sess, func(s *session) { s.highWater = eventsHighWater })
	lt.flushAfter(0)
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	c.pingPong()
	for range 30 {
		lt.stepAfter(time.Second)
		lt.flushAfter(0)
		envs, _ := c.collect()
		if checkAfter(t, eventsOf(t, envs), snap.LastSeq) {
			return
		}
	}
	t.Fatal("no events after the snapshot")
}

// SL-10: ラウンドの終わり。
func TestLoopRoundEnd(t *testing.T) {
	t.Parallel()
	_, c, lt, sess := startSingle(t, "s_sl.10", 1)
	var all []protocol.MessageType
	for range sess.durationMs / sim.Second {
		lt.stepAfter(time.Second)
		lt.flushAfter(0)
		envs, _ := c.collect()
		all = append(all, types(envs)...)
		for _, ev := range eventsOf(t, envs) {
			if ev.Clock.T > int64(sess.durationMs) {
				t.Fatalf("clock = %+v", ev.Clock)
			}
		}
	}
	n := len(all)
	if n < 3 || all[n-3] != protocol.MessageTypeEvents || all[n-2] != protocol.MessageTypeRoundResult ||
		all[n-1] != protocol.MessageTypeInternalResult {
		t.Fatalf("last messages = %v", all[max(0, n-5):])
	}
	if got := c.simTime(); got != int64(sess.durationMs) {
		t.Errorf("simTime = %d", got)
	}
	// 以後のステップは何もしない。
	lt.stepAfter(time.Second)
	if got := c.simTime(); got != int64(sess.durationMs) {
		t.Errorf("simTime after the end = %d", got)
	}
	c.send(protocol.MessageTypeCmdGamePause, "p-1", protocol.GamePausePayload{Paused: true})
	c.expectAckFail("p-1", protocol.AckCodePhaseInvalid)
	c.send(protocol.MessageTypeCmdSync, "", struct{}{})
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	if snap.Phase != protocol.PhaseEnded {
		t.Errorf("phase = %s", snap.Phase)
	}
}

// SL-10: ラウンドの最後の events に phase.changed（ended）が入る。
func TestLoopRoundEndEvents(t *testing.T) {
	t.Parallel()
	_, _, c, lt, sess := startStandalone(t, nil, `{"stageId":"alpha","seed":1}`)
	probe(t, sess, func(s *session) { s.advanceTo(s.durationMs - 500) })
	lt.flushAfter(0)
	c.collect()
	lt.stepAfter(time.Second)
	var ev protocol.EventsPayload
	c.expect(protocol.MessageTypeEvents, &ev)
	if !strings.Contains(mustJSON(t, ev.Events), `"type":"phase.changed","phase":"ended"`) || ev.Clock.T != 180000 {
		t.Errorf("events = %s clock %+v", mustJSON(t, ev.Events), ev.Clock)
	}
	c.expect(protocol.MessageTypeRoundResult, nil)
	c.pingPong() // standalone は internal.result を送らない
}

// SL-11: レート制限。
func TestLoopRateLimit(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	c := ts.dialSingle(t)
	c.initSingle("s_sl.11", 1)
	lt := ts.tickers(t)
	unload := protocol.FerryLettersPayload{FerryID: "f1", LetterIDs: []uint64{999999}}
	for i := range commandRatePerSec {
		id := fmt.Sprintf("u-%d", i)
		c.send(protocol.MessageTypeCmdFerryUnload, id, unload)
		if ack := c.expectAck(id); ack.OK || ack.Code == protocol.AckCodeRateLimited {
			t.Fatalf("#%d: ack = %+v", i+1, ack)
		}
		expectEmptyEvents(t, c)
		c.send(protocol.MessageTypePing, "", protocol.PingPayload{}) // ping は数えない
		c.expect(protocol.MessageTypePong, nil)
	}
	c.send(protocol.MessageTypeCmdFerryUnload, "u-31", unload)
	if ack := c.expectAckFail("u-31", protocol.AckCodeRateLimited); ack.T != 0 {
		t.Errorf("ack = %+v", ack)
	}
	c.send(protocol.MessageTypeCmdGamePause, "", protocol.GamePausePayload{Paused: true}) // cmdId がない → 捨てる
	c.pingPong()
	c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{}) // hello も数える
	c.pingPong()

	lt.clock.Advance(time.Second)
	c.send(protocol.MessageTypeCmdFerryUnload, "u-32", unload)
	if ack := c.expectAck("u-32"); ack.Code == protocol.AckCodeRateLimited {
		t.Fatalf("after 1s: ack = %+v", ack)
	}
	expectEmptyEvents(t, c)
	c.send(protocol.MessageTypeCmdSync, "", struct{}{})
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	if snap.Clock.Paused {
		t.Error("the dropped pause was applied")
	}
}

// slRun は SL-12 の 1 回分の記録。
type slRun struct {
	acks   []protocol.Ack
	events []string
	snap   protocol.Snapshot
	sync   protocol.Ack
}

func runAtSpeed(t *testing.T, id string, speed float64) slRun {
	t.Helper()
	ts := newTestServer(t, ModeSingle, nil)
	c := ts.dialSingle(t)
	c.initSingle(id, 7)
	lt := ts.tickers(t)
	var r slRun
	record := func(envs []protocol.Envelope) {
		for _, ev := range eventsOf(t, envs) {
			for _, raw := range ev.Events {
				r.events = append(r.events, string(raw))
			}
		}
	}
	cmd := func(typ protocol.MessageType, cmdID string, payload any) {
		c.send(typ, cmdID, payload)
		r.acks = append(r.acks, c.expectAck(cmdID))
		envs, _ := c.collect()
		record(envs)
	}
	if speed != 1 {
		c.control(protocol.MessageTypeCmdGameSpeed, "speed", protocol.GameSpeedPayload{Speed: speed})
	}
	cmd(protocol.MessageTypeCmdGameStart, "start", struct{}{})
	var now int64
	for now < 10000 {
		lt.stepAfter(50 * time.Millisecond)
		lt.flushAfter(0)
		var envs []protocol.Envelope
		envs, now = c.collect()
		record(envs)
		switch now {
		case 1000:
			cmd(protocol.MessageTypeCmdFerryAutoload, "a-1", protocol.FerryToPayload{FerryID: "f1", To: 2})
		case 3000:
			cmd(protocol.MessageTypeCmdFerryDepart, "d-1", protocol.FerryToPayload{FerryID: "f1", To: 2})
		}
	}
	if now != 10000 {
		t.Fatalf("now = %d", now)
	}
	c.send(protocol.MessageTypeCmdSync, "sync", struct{}{})
	r.sync = c.expectAck("sync")
	c.expect(protocol.MessageTypeSnapshot, &r.snap)
	if r.snap.Clock.Speed != speed {
		t.Errorf("snapshot speed = %v, want %v", r.snap.Clock.Speed, speed)
	}
	r.snap.Clock.Speed = 0
	return r
}

// SL-12: 速度を変えても、同じゲーム内時刻に同じコマンドを送れば結果は同じ（FR-GAME-05）。
func TestLoopSpeedDeterminism(t *testing.T) {
	t.Parallel()
	a := runAtSpeed(t, "s_sl.12a", 1)
	b := runAtSpeed(t, "s_sl.12b", 4)
	if got, want := mustJSON(t, b.acks), mustJSON(t, a.acks); got != want {
		t.Errorf("acks differ:\n4x %s\n1x %s", got, want)
	}
	if len(a.acks) != 3 || a.acks[1].T != 1000 || a.acks[2].T != 3000 {
		t.Errorf("acks = %+v", a.acks)
	}
	if got, want := strings.Join(b.events, "\n"), strings.Join(a.events, "\n"); got != want {
		t.Errorf("events differ (%d vs %d)", len(b.events), len(a.events))
	}
	if len(a.events) == 0 {
		t.Error("no events")
	}
	if a.sync != b.sync || a.sync.T != 10000 {
		t.Errorf("sync acks %+v / %+v", a.sync, b.sync)
	}
	a.snap.Session.ID, b.snap.Session.ID = "", ""
	if got, want := mustJSON(t, b.snap), mustJSON(t, a.snap); got != want {
		t.Errorf("snapshots differ:\n4x %s\n1x %s", got, want)
	}
}

// SL-13: ステップの中の panic。
func TestLoopPanic(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeStandalone, nil)
	a := ts.createSession(t, `{"stageId":"alpha"}`)
	lta := ts.tickers(t)
	b := ts.createSession(t, `{"stageId":"alpha"}`)
	ca, cb := ts.dialSession(t, a), ts.dialSession(t, b)
	ca.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
	ca.expect(protocol.MessageTypeSnapshot, nil)
	sa := ts.standaloneSession(t, a.SessionID)
	probe(t, sa, func(s *session) { s.stepHook = func() { panic("boom") } })
	lta.stepAfter(50 * time.Millisecond)
	ca.expectFatal(codeInternalError, websocket.StatusInternalError)
	cb.pingPong()
	assertGone(t, ts, a)
	if logs := ts.logs.String(); !strings.Contains(logs, `"where":"step"`) {
		t.Errorf("no panic log:\n%s", logs)
	}
}

// SL-14: コマンドの ack の t と、cmdId のない pause・speed・skip。
func TestLoopAckTime(t *testing.T) {
	t.Parallel()
	_, _, c, lt, sess := startStandalone(t, demoEdit, `{"stageId":"alpha","seed":1,"demo":true}`)
	lt.stepAfter(time.Second)
	ack, _ := c.control(protocol.MessageTypeCmdFerryDepart, "d-1", protocol.FerryToPayload{FerryID: "f1", To: 2})
	if ack.T != 1000 {
		t.Errorf("ack = %+v", ack)
	}
	c.send(protocol.MessageTypeCmdGamePause, "", protocol.GamePausePayload{Paused: true})
	if p := c.expectError(codeBadRequest, false); p.Message != "cmdId is required" {
		t.Errorf("message = %q", p.Message)
	}
	c.send(protocol.MessageTypeCmdGameSpeed, "", protocol.GameSpeedPayload{Speed: 2})
	c.expectError(codeBadRequest, false)
	c.send(protocol.MessageTypeCmdGameSkip, "", protocol.GameSkipPayload{Until: "next_notable"})
	c.expectError(codeBadRequest, false)
	c.pingPong()
	probe(t, sess, func(s *session) {
		if s.pause.isPaused() || s.speedPermille != 1000 || s.engine.Now() != 1000 {
			t.Errorf("pause %+v speed %d now %d", s.pause, s.speedPermille, s.engine.Now())
		}
	})
}
