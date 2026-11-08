package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// SE-1: advanceTo(DurationMs) で round.result を 1 回送る（single は internal.result も）。
// つなぎ直した接続には snapshot の後にもう一度送る。
func TestSessionRoundResult(t *testing.T) {
	t.Parallel()
	t.Run("single", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t, ModeSingle, nil)
		c := ts.dialSingle(t)
		c.initSingle("s_se.1", 3)
		c.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
		c.expect(protocol.MessageTypeAck, nil)
		c.expect(protocol.MessageTypeEvents, nil)
		sess := ts.singleSession(t)
		sess.do(func() { sess.advanceTo(sess.durationMs) })
		var ev protocol.EventsPayload
		c.expect(protocol.MessageTypeEvents, &ev) // phase.changed（ended）は round.result より先
		if ev.Clock.T != 180000 {
			t.Errorf("clock = %+v", ev.Clock)
		}
		r1 := c.expect(protocol.MessageTypeRoundResult, nil)
		r2 := c.expect(protocol.MessageTypeInternalResult, nil)
		if string(r1.Payload) != string(r2.Payload) {
			t.Errorf("payloads differ: %s / %s", r1.Payload, r2.Payload)
		}
		sess.do(func() { sess.advanceTo(sess.durationMs + 1000) })
		c.pingPong() // 2 回目は送らない

		c2 := ts.dialSingle(t)
		var snap protocol.Snapshot
		c2.send(protocol.MessageTypeInternalInit, "", initPayload("s_se.1", "alpha", 3))
		c2.expect(protocol.MessageTypeSnapshot, &snap)
		if snap.Phase != protocol.PhaseEnded {
			t.Errorf("phase = %s", snap.Phase)
		}
		c2.expect(protocol.MessageTypeRoundResult, nil)
		c2.expect(protocol.MessageTypeInternalResult, nil)
		c2.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
		c2.expect(protocol.MessageTypeSnapshot, nil)
		c2.pingPong() // hello の snapshot の後には送らない
	})
	t.Run("standalone", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t, ModeStandalone, nil)
		cr := ts.createSession(t, `{"stageId":"alpha","seed":3}`)
		c := ts.dialSession(t, cr)
		c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{}) // standalone は hello の snapshot の後だけ events を送る（T-303）
		c.expect(protocol.MessageTypeSnapshot, nil)
		c.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
		c.expect(protocol.MessageTypeAck, nil)
		c.expect(protocol.MessageTypeEvents, nil)
		sess := ts.standaloneSession(t, cr.SessionID)
		sess.do(func() { sess.advanceTo(sess.durationMs) })
		c.expect(protocol.MessageTypeEvents, nil)
		var res protocol.RoundResult
		c.expect(protocol.MessageTypeRoundResult, &res)
		if res.InputLogAvailable {
			t.Error("inputLogAvailable is true in standalone")
		}
		c.pingPong() // internal.result は送らない

		c2 := ts.dialSession(t, cr)
		c.expectFatal(codeReplaced, websocket.StatusNormalClosure)
		c2.send(protocol.MessageTypeHello, "", protocol.HelloPayload{Resume: true})
		c2.expect(protocol.MessageTypeSnapshot, nil)
		c2.expect(protocol.MessageTypeRoundResult, nil)
		c2.pingPong()
	})
}

// SE-2: 電文の処理の表。
func TestSessionHandle(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	c := ts.dialSingle(t)
	c.initSingle("s_se.2", 1)

	expectAck := func(cmdID string, ok bool, code protocol.AckCode, msg string) {
		t.Helper()
		var ack protocol.Ack
		c.expect(protocol.MessageTypeAck, &ack)
		want := protocol.Ack{CmdID: cmdID, OK: ok, T: 0, Code: code, Message: msg}
		if msg == "*" {
			want.Message = ack.Message
		}
		if ack != want {
			t.Errorf("ack = %+v, want %+v", ack, want)
		}
	}

	// cmd.game.pause・speed・skip は T-303 が処理する（payload {} は必須のキーがないので bad_request）。
	for _, typ := range []protocol.MessageType{
		protocol.MessageTypeCmdGamePause, protocol.MessageTypeCmdGameSpeed, protocol.MessageTypeCmdGameSkip,
	} {
		c.send(typ, "n-1", struct{}{})
		expectAck("n-1", false, protocol.AckCodeBadRequest, "bad payload")
		c.send(typ, "", struct{}{})
		c.expectError(codeBadRequest, false)
	}

	// demo.trigger は T-305 が処理する（payload {} は event がないので bad_request。デモの可否より先に調べる）。
	c.send(protocol.MessageTypeDemoTrigger, "n-1", struct{}{})
	expectAck("n-1", false, protocol.AckCodeBadRequest, "bad payload")
	c.send(protocol.MessageTypeDemoTrigger, "", struct{}{})
	c.expectError(codeBadRequest, false)

	for _, typ := range []protocol.MessageType{"foo", protocol.MessageTypeSnapshot, protocol.MessageTypeAck} {
		c.send(typ, "u-1", struct{}{})
		expectAck("u-1", false, protocol.AckCodeBadRequest, "unknown type")
		c.send(typ, "", struct{}{})
		c.expectError(codeBadRequest, false)
	}

	// Engine のコマンドの payload が不正。
	c.sendRaw(websocket.MessageText, []byte(`{"v":1,"type":"cmd.ferry.load","cmdId":"b-1","payload":{"ferryId":1}}`))
	expectAck("b-1", false, protocol.AckCodeBadRequest, "*")
	c.sendRaw(websocket.MessageText, []byte(`{"v":1,"type":"cmd.ferry.load","payload":{"ferryId":1}}`))
	c.expectError(codeBadRequest, false)

	// cmd.sync は ack（ok: true）→ snapshot。cmdId がなければ snapshot だけ。
	c.send(protocol.MessageTypeCmdSync, "s-1", struct{}{})
	expectAck("s-1", true, "", "")
	c.expect(protocol.MessageTypeSnapshot, nil)
	c.send(protocol.MessageTypeCmdSync, "", struct{}{})
	c.expect(protocol.MessageTypeSnapshot, nil)

	// hello・ping は cmdId があっても ack を返さない。
	c.send(protocol.MessageTypeHello, "h-1", protocol.HelloPayload{})
	c.expect(protocol.MessageTypeSnapshot, nil)
	c.send(protocol.MessageTypePing, "p-1", protocol.PingPayload{ClientTime: 123456.5})
	var pong protocol.PongPayload
	c.expect(protocol.MessageTypePong, &pong)
	if pong.ClientTime != 123456.5 || pong.SimTime != 0 {
		t.Errorf("pong = %+v", pong)
	}
	c.sendRaw(websocket.MessageText, []byte(`{"v":1,"type":"ping","payload":{"clientTime":"x"}}`))
	c.expectError(codeBadRequest, false)
	c.pingPong()
}

// SE-3: Engine の panic を回復する。
func TestSessionPanic(t *testing.T) {
	t.Parallel()
	hook := func(_ *Config, s *Server) {
		s.handleHook = func(env protocol.Envelope) {
			if env.CmdID == "panic" {
				panic("boom")
			}
		}
	}
	t.Run("single", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t, ModeSingle, hook)
		c := ts.dialSingle(t)
		c.initSingle("s_se.3", 1)
		c.send(protocol.MessageTypeCmdSync, "panic", struct{}{})
		c.expectFatal(codeInternalError, websocket.StatusInternalError)

		c2 := ts.dialSingle(t)
		c2.send(protocol.MessageTypeInternalInit, "", initPayload("s_se.3", "alpha", 1))
		c2.expectFatal(codeInternalError, websocket.StatusInternalError)

		res, _ := ts.do(t, http.MethodGet, "/healthz", nil, "")
		if res.StatusCode != http.StatusOK {
			t.Errorf("healthz = %d", res.StatusCode)
		}
		if logs := ts.logs.String(); !strings.Contains(logs, `"msg":"session panic"`) {
			t.Errorf("no panic log:\n%s", logs)
		}
	})
	t.Run("standalone", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t, ModeStandalone, hook)
		a := ts.createSession(t, `{"stageId":"alpha"}`)
		b := ts.createSession(t, `{"stageId":"alpha"}`)
		ca, cb := ts.dialSession(t, a), ts.dialSession(t, b)
		ca.send(protocol.MessageTypeHello, "panic", protocol.HelloPayload{})
		ca.expectFatal(codeInternalError, websocket.StatusInternalError)
		cb.pingPong()

		// 一覧から消えている。
		ctx, cancel := contextWithTimeout()
		defer cancel()
		_, res, err := websocket.Dial(ctx, ts.wsURL(a.WSPath+"?token="+a.Token),
			&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {ts.origin()}}})
		if err == nil || res == nil || res.StatusCode != http.StatusNotFound {
			t.Errorf("dial after panic: %v", err)
		}
		res2, _ := ts.do(t, http.MethodGet, "/api/healthz", nil, "")
		if res2.StatusCode != http.StatusOK {
			t.Errorf("healthz = %d", res2.StatusCode)
		}
	})
}

// SE-4: buildSnapshot の stage の変換。
func TestBuildSnapshotStage(t *testing.T) {
	t.Parallel()
	st := &stage.Stage{
		ID:    "x",
		Title: "題",
		Islands: []stage.Island{
			{NodeID: 1, Name: "一", X: 10, Y: 20, Color: "#000000", Population: 100},
			{NodeID: 2, Name: "二", X: 30, Y: 40, Color: "#FFFFFF", Population: 200},
			{NodeID: 3, Name: "三", X: 50, Y: 60, Color: "#123456", Population: 300},
		},
		Routes: []stage.Route{
			{A: 3, B: 1, TravelMs: 1000, Provenance: stage.Fact},
			{A: 1, B: 2, TravelMs: 2000, Provenance: stage.Creative},
		},
		Resources:            []stage.Resource{{ID: "stall:a", Kind: stage.ResourceStall, Label: "屋台", Capacity: 1}},
		SpeedAllowedPermille: []int64{500, 1000, 2000, 4000},
	}
	v := stageView(st, nil) // 表示用の値がない（長さが合わない）ときは空の文字列
	if got := mustJSON(t, v.Routes); got !=
		`[{"id":"1-3","a":3,"b":1,"travelMs":1000,"provenance":"fact"},{"id":"1-2","a":1,"b":2,"travelMs":2000,"provenance":"creative"}]` {
		t.Errorf("routes = %s", got)
	}
	if got := mustJSON(t, v.Islands[1]); got !=
		`{"id":2,"name":"二","municipality":"","layout":{"x":30,"y":40},"population":{"value":200,"asOf":"","provenance":"","source":""},"color":"#FFFFFF"}` {
		t.Errorf("islands[1] = %s", got)
	}
	if got := mustJSON(t, v.Resources); got != `[{"id":"stall:a","kind":"stall","label":"屋台","critical":false}]` {
		t.Errorf("resources = %s", got)
	}
	if got := mustJSON(t, allowedSpeeds(allowedSpeedsPermille(st, false))); got != "[0.5,1,2,4]" {
		t.Errorf("allowedSpeeds = %s", got)
	}

	// unlock がないステージ（alpha）で score.target。
	cat := testCatalog(t)
	alpha, _ := cat.Lookup("alpha")
	cfg, err := stage.SimConfig(alpha)
	if err != nil {
		t.Fatal(err)
	}
	e, err := sim.New(cfg, 1, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	p := sessionParams{id: "s_x", st: alpha, seed: 1, mode: ModeStandalone, build: "b", engine: e,
		log: discardLogger(), now: time.Now}
	s := newSession(t.Context(), p) // run を始めないので、このテストの goroutine が Engine に触れてよい
	snap := s.buildSnapshot()
	if snap.Score.Target != 2 || snap.Score.CriticalSecured == nil || snap.Session.Build != "b" || snap.Session.StageHash != alpha.Hash {
		t.Errorf("snapshot score %+v session %+v", snap.Score, snap.Session)
	}
	assertNoNullArrays(t, []byte(mustJSON(t, snap)))
	r := s.buildResult()
	if r.InputLogAvailable || len(r.Stats.FinalLogs) != 3 {
		t.Errorf("result = %+v", r)
	}
	assertNoNullArrays(t, []byte(mustJSON(t, r)))
}

// wantSnapshotIsland0 は testdata の alpha の島 1（hama-hoshizuna）の snapshot の値（SV-1）。
const wantSnapshotIsland0 = `{"id":1,"name":"星砂島","municipality":"星砂町・月影町","layout":{"x":600,"y":200},` +
	`"population":{"value":19134,"asOf":"令和8年4月1日","provenance":"derived","note":"星砂町 12,345 人と月影町 6,789 人の合計","source":"fixture-2026"},` +
	`"color":"#E07A5F"}`

// assertSnapshotIslands は、testdata の alpha の snapshot の payload の stage.islands[] が
// islands.json の表示用の値になっていることを JSON で確かめる（SV-1・SV-3）。
func assertSnapshotIslands(t *testing.T, payload []byte) {
	t.Helper()
	var raw struct {
		Stage struct {
			Islands []json.RawMessage `json:"islands"`
		} `json:"stage"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Stage.Islands) != 3 {
		t.Fatalf("stage.islands = %s", payload)
	}
	var got bytes.Buffer
	if err := json.Compact(&got, raw.Stage.Islands[0]); err != nil {
		t.Fatal(err)
	}
	if got.String() != wantSnapshotIsland0 {
		t.Errorf("stage.islands[0] = %s\nwant %s", got.String(), wantSnapshotIsland0)
	}
	var is1 struct {
		Municipality string                     `json:"municipality"`
		Population   map[string]json.RawMessage `json:"population"`
	}
	if err := json.Unmarshal(raw.Stage.Islands[1], &is1); err != nil {
		t.Fatal(err)
	}
	if _, has := is1.Population["note"]; has {
		t.Errorf("stage.islands[1].population に note がある: %s", raw.Stage.Islands[1])
	}
	if is1.Municipality != "月影町" || string(is1.Population["asOf"]) != `"令和8年4月1日"` ||
		string(is1.Population["provenance"]) != `"fact"` || string(is1.Population["source"]) != `"fixture-2026"` {
		t.Errorf("stage.islands[1] = %s", raw.Stage.Islands[1])
	}
}

// SV-1: Catalog の表示用の値で作ったセッションの snapshot。長さが合わない表示用の値は空の文字列で送る。
func TestBuildSnapshotIslandDisplay(t *testing.T) {
	t.Parallel()
	cat := testCatalog(t)
	alpha, _ := cat.Lookup("alpha")
	islands, ok := cat.islands("alpha")
	if !ok || len(islands) != 3 {
		t.Fatalf("islands(alpha) = %v, %v", islands, ok)
	}
	if _, ok := cat.islands("x"); ok {
		t.Error("islands(x) found")
	}
	// 返すたびに複製する。
	islands[0].Municipalities[0] = "変更"
	islands[0].Plan.Texts[0].Text = "変更"
	again, _ := cat.islands("alpha")
	if again[0].Municipalities[0] != "星砂町" || again[0].Plan.Texts[0].Text == "変更" {
		t.Errorf("islands(alpha) shares the values: %+v", again[0])
	}

	cfg, err := stage.SimConfig(alpha)
	if err != nil {
		t.Fatal(err)
	}
	e, err := sim.New(cfg, 1, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	p := sessionParams{id: "s_x", st: alpha, islands: again, seed: 1, mode: ModeStandalone, build: "b", engine: e,
		log: discardLogger(), now: time.Now}
	s := newSession(t.Context(), p) // run を始めないので、このテストの goroutine が Engine に触れてよい
	assertSnapshotIslands(t, []byte(mustJSON(t, s.buildSnapshot())))

	v := stageView(alpha, again[:2])
	if got := mustJSON(t, v.Islands[0].Population); v.Islands[0].Municipality != "" ||
		got != `{"value":19134,"asOf":"","provenance":"","source":""}` {
		t.Errorf("islands[0] = %+v %s", v.Islands[0], got)
	}
}
