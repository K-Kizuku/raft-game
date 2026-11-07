package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// demoStormMs はテストのステージ stormy の demo.storm.durationMs。
const demoStormMs = 20000

// demoStormYAML は alpha.yaml の末尾に足すデモの設定（航路 2-3 を 20 秒）。
const demoStormYAML = `
demo:
  endOnAllCriticalSecured: false
  storm:
    routes: [[hama-tsukikage, hama-todai]]
    durationMs: 20000
`

// demoCatalog は testdata の alpha と、alpha に demo.storm を足したステージ stormy を持つカタログ。
// testdata/ は変えない（ほかのテストのステージのハッシュ・一覧が変わるため）。
func demoCatalog(t *testing.T) *Catalog {
	t.Helper()
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	alpha := string(read("stages/alpha.yaml"))
	if strings.Count(alpha, "\nid: alpha\n") != 1 {
		t.Fatal("alpha.yaml の id の行が見つからない")
	}
	stormy := strings.Replace(alpha, "\nid: alpha\n", "\nid: stormy\n", 1) + demoStormYAML
	cat, err := LoadCatalog(fstest.MapFS{
		"islands.json":       {Data: read("islands.json")},
		"stages/alpha.yaml":  {Data: []byte(alpha)},
		"stages/stormy.yaml": {Data: []byte(stormy)},
	})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	return cat
}

// demoStormEdit はデモ操作を有効にし、カタログを demoCatalog に差し替える。
func demoStormEdit(t *testing.T) func(*Config, *Server) {
	cat := demoCatalog(t)
	return func(c *Config, s *Server) {
		c.DemoControls = true
		s.cat = cat
	}
}

var stormPayload = protocol.DemoTriggerPayload{Event: "storm"}

// routeEvent は route.blocked・route.unblocked の JSON。
type routeEvent struct {
	T        int64    `json:"t"`
	Type     string   `json:"type"`
	RouteIDs []string `json:"routeIds"`
	Until    int64    `json:"until"`
}

// routeEvents は events のうち route.* だけを返す。
func routeEvents(t *testing.T, raws []json.RawMessage) []routeEvent {
	t.Helper()
	var out []routeEvent
	for _, raw := range raws {
		var ev routeEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(ev.Type, "route.") {
			out = append(out, ev)
		}
	}
	return out
}

// syncSnapshot は cmd.sync を送り、ack の後の snapshot を返す。
func (c *client) syncSnapshot(cmdID string) protocol.Snapshot {
	c.t.Helper()
	c.send(protocol.MessageTypeCmdSync, cmdID, struct{}{})
	c.expectAck(cmdID)
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	return snap
}

// checkBlocked は routesState の 2-3（添字 1）の遮断が until で、1-2 が遮断されていないことを確かめる。
func checkBlocked(t *testing.T, rs []protocol.RouteState, until int64) {
	t.Helper()
	if len(rs) != 2 || rs[0].ID != "1-2" || rs[0].BlockedUntil != nil || rs[1].ID != "2-3" ||
		rs[1].BlockedUntil == nil || *rs[1].BlockedUntil != until {
		t.Errorf("routesState = %s, want 2-3 blocked until %d", mustJSON(t, rs), until)
	}
}

// SV-D1 成功。
func TestDemoTrigger(t *testing.T) {
	t.Parallel()
	_, _, c, _, sess := startStandalone(t, demoStormEdit(t), `{"stageId":"stormy","seed":1,"demo":true}`)
	now := simNow(t, sess)
	ack, ev := c.control(protocol.MessageTypeDemoTrigger, "d-1", stormPayload)
	if ack.T != int64(now) {
		t.Errorf("ack.t = %d, want %d", ack.T, now)
	}
	until := ack.T + demoStormMs
	want := []routeEvent{{T: ack.T, Type: "route.blocked", RouteIDs: []string{"2-3"}, Until: until}}
	if got := routeEvents(t, ev.Events); mustJSON(t, got) != mustJSON(t, want) {
		t.Errorf("route events = %s, want %s", mustJSON(t, got), mustJSON(t, want))
	}
	if ev.Clock.T != ack.T || ev.Clock.EndsAt == 0 {
		t.Errorf("clock = %+v", ev.Clock)
	}
	checkBlocked(t, c.syncSnapshot("s-1").RoutesState, until)

	// Engine の入力ログに残る（Durable Object の入力ログと同じ順・時刻）。
	var log []sim.InputEntry
	probe(t, sess, func(s *session) { log = s.engine.InputLog() })
	if n := len(log); n == 0 || !reflect.DeepEqual(log[n-1], sim.InputEntry{T: now, Cmd: sim.Command{Type: sim.CmdDemoTrigger, Event: "storm"}}) {
		t.Errorf("入力ログ = %+v", log)
	}
}

// SV-D2 デモでないセッション。
func TestDemoTriggerDisabled(t *testing.T) {
	t.Parallel()
	_, _, c, _, sess := startStandalone(t, demoStormEdit(t), `{"stageId":"stormy","seed":1}`)
	c.send(protocol.MessageTypeDemoTrigger, "d-1", stormPayload)
	if ack := c.expectAckFail("d-1", protocol.AckCodeBadRequest); ack.Message != "demo controls disabled" {
		t.Errorf("message = %q", ack.Message)
	}
	c.pingPong() // events は来ない
	probe(t, sess, func(s *session) {
		if n := len(s.engine.InputLog()); n != 1 {
			t.Errorf("入力ログ %d 件, want 1（cmd.game.start だけ）", n)
		}
	})
}

// SV-D3 payload の誤りと cmdId なし。
func TestDemoTriggerBadRequest(t *testing.T) {
	t.Parallel()
	_, _, c, _, _ := startStandalone(t, demoStormEdit(t), `{"stageId":"stormy","seed":1,"demo":true}`)
	for i, payload := range []string{`{}`, `{"event":"rush"}`, `{"event":1}`, `[]`, `null`} {
		id := "b-" + strconv.Itoa(i)
		c.sendPayload(protocol.MessageTypeDemoTrigger, id, payload)
		if ack := c.expectAckFail(id, protocol.AckCodeBadRequest); ack.Message != "bad payload" {
			t.Errorf("%s: message = %q", payload, ack.Message)
		}
	}
	c.send(protocol.MessageTypeDemoTrigger, "", stormPayload)
	c.expectError(codeBadRequest, false)
	c.send(protocol.MessageTypeDemoTrigger, "", struct{}{})
	c.expectError(codeBadRequest, false)
	c.pingPong()
	if rs := c.syncSnapshot("s-1").RoutesState; rs[1].BlockedUntil != nil {
		t.Errorf("遮断された: %s", mustJSON(t, rs))
	}
}

// expectNoRouteEvents は、Engine が失敗した ack の後の events（Engine のコマンドと同じく送る）に
// route.* がなく、その後に何も届かないことを確かめる。
func expectNoRouteEvents(t *testing.T, c *client) {
	t.Helper()
	var ev protocol.EventsPayload
	c.expect(protocol.MessageTypeEvents, &ev)
	if len(ev.Events) != 0 {
		t.Errorf("events = %s, want none", mustJSON(t, ev.Events))
	}
	c.pingPong()
}

// SV-D4 Intro と demo.storm のないステージ。
func TestDemoTriggerEngineFail(t *testing.T) {
	t.Parallel()
	t.Run("Intro", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t, ModeStandalone, demoStormEdit(t))
		cr := ts.createSession(t, `{"stageId":"stormy","seed":1,"demo":true}`)
		c := ts.dialSession(t, cr)
		c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
		c.expect(protocol.MessageTypeSnapshot, nil)
		c.send(protocol.MessageTypeDemoTrigger, "d-1", stormPayload)
		c.expectAckFail("d-1", protocol.AckCodePhaseInvalid)
		expectNoRouteEvents(t, c)
	})
	t.Run("demo.storm がない", func(t *testing.T) {
		t.Parallel()
		_, _, c, _, _ := startStandalone(t, demoStormEdit(t), `{"stageId":"alpha","seed":1,"demo":true}`)
		c.send(protocol.MessageTypeDemoTrigger, "d-1", stormPayload)
		if ack := c.expectAckFail("d-1", protocol.AckCodeBadRequest); !strings.Contains(ack.Message, "stage has no demo.storm") {
			t.Errorf("message = %q", ack.Message)
		}
		expectNoRouteEvents(t, c)
	})
	t.Run("終了後", func(t *testing.T) {
		t.Parallel()
		_, _, c, _, sess := startStandalone(t, demoStormEdit(t), `{"stageId":"stormy","seed":1,"demo":true}`)
		probe(t, sess, func(s *session) { s.advanceTo(s.durationMs) })
		c.expect(protocol.MessageTypeEvents, nil)
		c.expect(protocol.MessageTypeRoundResult, nil)
		c.send(protocol.MessageTypeDemoTrigger, "d-1", stormPayload)
		c.expectAckFail("d-1", protocol.AckCodePhaseInvalid)
		expectNoRouteEvents(t, c)
	})
}

// SV-D5 一時停止中。
func TestDemoTriggerPaused(t *testing.T) {
	t.Parallel()
	_, _, c, lt, _ := startStandalone(t, demoStormEdit(t), `{"stageId":"stormy","seed":1,"demo":true}`)
	lt.stepAfter(stepInterval)
	c.collect()
	_, pev := c.control(protocol.MessageTypeCmdGamePause, "p-1", protocol.GamePausePayload{Paused: true})
	ack, ev := c.control(protocol.MessageTypeDemoTrigger, "d-1", stormPayload)
	if ack.T != pev.Clock.T || !ev.Clock.Paused || ev.Clock.T != ack.T {
		t.Errorf("ack %+v clock %+v (paused at %d)", ack, ev.Clock, pev.Clock.T)
	}
	if got := routeEvents(t, ev.Events); len(got) != 1 || got[0].Type != "route.blocked" || got[0].Until != ack.T+demoStormMs {
		t.Errorf("route events = %s", mustJSON(t, got))
	}
	lt.stepAfter(stepInterval)
	if got := c.simTime(); got != ack.T {
		t.Errorf("simTime = %d, want %d（止まったまま）", got, ack.T)
	}
}

// SV-D6 復元（single）で同じ嵐が起きる。
func TestDemoTriggerRestore(t *testing.T) {
	t.Parallel()
	edit := demoStormEdit(t)

	// SV-D1 と同じ操作をしたセッションの routesState。
	_, _, c1, _, _ := startStandalone(t, edit, `{"stageId":"stormy","seed":1,"demo":true}`)
	ack, _ := c1.control(protocol.MessageTypeDemoTrigger, "d-1", stormPayload)
	if ack.T != 0 {
		t.Fatalf("ack.t = %d, want 0", ack.T)
	}
	want := c1.syncSnapshot("s-1").RoutesState

	ts := newTestServer(t, ModeSingle, edit)
	st, _ := ts.srv.cat.Lookup("stormy")
	init := initPayload("s_demo.6", "stormy", 1)
	init.Demo = true
	c := ts.dialSingle(t)
	c.send(protocol.MessageTypeInternalRestore, "", protocol.InternalRestorePayload{
		InternalInitPayload: init,
		InputLog: protocol.InputLog{
			Format: sim.ReplayFormat, Build: "git:old0000", StageID: "stormy", StageHash: st.Hash, Seed: 1,
			Commands: []protocol.InputCommand{
				{T: 0, Cmd: protocol.ClientCommand{Type: protocol.MessageTypeCmdGameStart, Payload: json.RawMessage(`{}`)}},
				{T: 0, Cmd: protocol.ClientCommand{Type: protocol.MessageTypeDemoTrigger, Payload: json.RawMessage(`{"event":"storm"}`)}},
			},
		},
		Until: 1000,
	})
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	if !snap.Session.Restored || snap.Clock.T != 1000 {
		t.Errorf("session %+v clock %+v", snap.Session, snap.Clock)
	}
	checkBlocked(t, snap.RoutesState, demoStormMs)
	if got := mustJSON(t, snap.RoutesState); got != mustJSON(t, want) {
		t.Errorf("routesState = %s, want %s（SV-D1 と同じ操作のセッション）", got, mustJSON(t, want))
	}
}
