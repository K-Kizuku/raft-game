package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

const restoreSeed = 42

// refEngine は、テストの中で動かす参照の Engine。
type refEngine struct {
	t  *testing.T
	st *stage.Stage
	e  *sim.Engine
}

func newRefEngine(t *testing.T, cat *Catalog) *refEngine {
	t.Helper()
	st, _ := cat.Lookup("alpha")
	cfg, err := stage.SimConfig(st)
	if err != nil {
		t.Fatal(err)
	}
	e, err := sim.New(cfg, restoreSeed, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatal(err)
	}
	return &refEngine{t: t, st: st, e: e}
}

func (r *refEngine) apply(c sim.Command) sim.Ack {
	r.t.Helper()
	a := r.e.Apply(c)
	if !a.OK {
		r.t.Fatalf("ref Apply(%+v) = %+v", c, a)
	}
	return a
}

// play は RS-1 の操作をする: start → RunUntil(1000) → 島 1 の港の手紙を load → depart（to: 2）→ RunUntil(2500)。
func (r *refEngine) play() {
	r.t.Helper()
	r.apply(sim.Command{Type: sim.CmdGameStart})
	r.e.RunUntil(1000)
	var letter uint64
	for _, l := range r.e.Snapshot().Letters {
		if l.Loc.Type == "port" && l.Loc.IslandID == 1 {
			letter = l.ID
			break
		}
	}
	if letter == 0 {
		r.t.Fatal("島 1 の港に手紙がない")
	}
	r.apply(sim.Command{Type: sim.CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{letter}})
	r.apply(sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2})
	r.e.RunUntil(2500)
}

// inputLog は参照の Engine の入力ログを protocol の形にする。2 件目の後に cmd.game.pause を混ぜる。
func (r *refEngine) inputLog(build string) protocol.InputLog {
	r.t.Helper()
	var cmds []protocol.InputCommand
	for i, en := range r.e.InputLog() {
		cc, err := simconv.EncodeEngineCommand(en.Cmd)
		if err != nil {
			r.t.Fatal(err)
		}
		cmds = append(cmds, protocol.InputCommand{T: int64(en.T), Cmd: cc})
		if i == 1 {
			cmds = append(cmds, protocol.InputCommand{T: int64(en.T), Cmd: protocol.ClientCommand{
				Type: protocol.MessageTypeCmdGamePause, Payload: json.RawMessage(`{"paused":true}`),
			}})
		}
	}
	if cmds == nil {
		cmds = []protocol.InputCommand{}
	}
	return protocol.InputLog{
		Format: sim.ReplayFormat, Build: build, StageID: "alpha", StageHash: r.st.Hash, Seed: restoreSeed, Commands: cmds,
	}
}

func restorePayload(id string, log protocol.InputLog, until int64) protocol.InternalRestorePayload {
	return protocol.InternalRestorePayload{
		InternalInitPayload: initPayload(id, "alpha", restoreSeed),
		InputLog:            log,
		Until:               until,
	}
}

// snapshotParts は、復元で参照と一致すべき部分（ferries・routesState・letters・nodes・phase・lastSeq）。
func snapshotParts(t *testing.T, s protocol.Snapshot) string {
	t.Helper()
	return mustJSON(t, map[string]any{
		"ferries": s.Ferries, "routesState": s.RoutesState, "letters": s.Letters,
		"nodes": s.Nodes, "phase": s.Phase, "lastSeq": s.LastSeq,
	})
}

// RS-1: 入力ログから復元した状態と、その後の操作の結果が参照の Engine と一致する。
func TestRestoreMatchesReference(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	ref := newRefEngine(t, ts.srv.cat)
	ref.play()
	ref.e.DrainEvents()

	c := ts.dialSingle(t)
	c.send(protocol.MessageTypeInternalRestore, "", restorePayload("s_restore.1", ref.inputLog("git:old0000"), 2500))
	var snap protocol.Snapshot
	env := c.expect(protocol.MessageTypeSnapshot, &snap)
	assertNoNullArrays(t, env.Payload)
	if !snap.Session.Restored || !snap.Clock.Paused || snap.Clock.T != 2500 {
		t.Errorf("restored %v paused %v t %d", snap.Session.Restored, snap.Clock.Paused, snap.Clock.T)
	}
	want := simconv.SnapshotFromSim(ref.e.Snapshot())
	if got, w := snapshotParts(t, snap), snapshotParts(t, want); got != w {
		t.Fatalf("snapshot =\n%s\nwant\n%s", got, w)
	}

	// その後の操作も参照と一致する。時間は T-303 が進めるので、ここでは両方を同じ時刻まで進める。
	sess := ts.singleSession(t)
	sess.do(func() { sess.advanceTo(4500) })
	ref.e.RunUntil(4500)
	cmds := []sim.Command{
		{Type: sim.CmdFerryAutoload, FerryID: "f1", To: 1},
		{Type: sim.CmdFerryDepart, FerryID: "f1", To: 1},
		{Type: sim.CmdFerryDepart, FerryID: "f1", To: 3}, // 船は出た後なので失敗する
	}
	sawEvents := false
	for i, cmd := range cmds {
		cc, err := simconv.EncodeEngineCommand(cmd)
		if err != nil {
			t.Fatal(err)
		}
		cmdID := "r-" + string(rune('1'+i))
		c.send(cc.Type, cmdID, cc.Payload)
		wantAck := simconv.AckFromSim(cmdID, ref.e.Apply(cmd))
		var ack protocol.Ack
		c.expect(protocol.MessageTypeAck, &ack)
		if ack != wantAck {
			t.Errorf("%s: ack = %+v, want %+v", cmd.Type, ack, wantAck)
		}
		evs := ref.e.DrainEvents()
		if len(evs) == 0 {
			continue
		}
		var got protocol.EventsPayload
		c.expect(protocol.MessageTypeEvents, &got)
		if w := mustJSON(t, evs); mustJSON(t, got.Events) != w {
			t.Errorf("%s: events =\n%s\nwant\n%s", cmd.Type, mustJSON(t, got.Events), w)
		}
		if !sawEvents {
			sawEvents = true
			var first struct {
				Seq uint64 `json:"seq"`
			}
			_ = json.Unmarshal(got.Events[0], &first)
			if first.Seq <= snap.LastSeq {
				t.Errorf("first event seq %d <= snapshot lastSeq %d", first.Seq, snap.LastSeq)
			}
		}
	}
	if !sawEvents {
		t.Error("no events after restore")
	}
	c.pingPong()
}

// RS-2: until を省くと、最後のコマンドの t まで進める。
func TestRestoreWithoutUntil(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	ref := newRefEngine(t, ts.srv.cat)
	ref.play()
	log := ref.inputLog(testBuild)
	last := log.Commands[len(log.Commands)-1].T
	if last == 0 {
		t.Fatal("last command at t=0")
	}
	c := ts.dialSingle(t)
	c.send(protocol.MessageTypeInternalRestore, "", restorePayload("s_restore.2", log, 0))
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	if snap.Clock.T != last || !snap.Session.Restored {
		t.Errorf("t = %d restored %v, want t %d", snap.Clock.T, snap.Session.Restored, last)
	}
}

// RS-3: 復元の失敗はすべて restore_failed（fatal、1011）。
func TestRestoreFailures(t *testing.T) {
	t.Parallel()
	cat := testCatalog(t)
	ref := newRefEngine(t, cat)
	ref.play()
	base := ref.inputLog(testBuild)
	clone := func(edit func(*protocol.InternalRestorePayload)) protocol.InternalRestorePayload {
		var p protocol.InternalRestorePayload
		if err := json.Unmarshal([]byte(mustJSON(t, restorePayload("s_restore.3", base, 0))), &p); err != nil {
			t.Fatal(err)
		}
		edit(&p)
		return p
	}
	tests := []struct {
		name string
		p    protocol.InternalRestorePayload
		msg  string
	}{
		{"format: 2", clone(func(p *protocol.InternalRestorePayload) { p.InputLog.Format = 2 }), "format"},
		{"stageId の食い違い", clone(func(p *protocol.InternalRestorePayload) { p.InputLog.StageID = "omega" }), "stageId"},
		{"seed の食い違い", clone(func(p *protocol.InternalRestorePayload) { p.InputLog.Seed = 1 }), "seed"},
		{"stageHash の食い違い", clone(func(p *protocol.InternalRestorePayload) {
			p.InputLog.StageHash = "sha256:" + strings.Repeat("0", 64)
		}), "stage hash"},
		{"stageHash が空", clone(func(p *protocol.InternalRestorePayload) { p.InputLog.StageHash = "" }), "stage hash"},
		{"hello を含む", clone(func(p *protocol.InternalRestorePayload) {
			p.InputLog.Commands = append(p.InputLog.Commands, protocol.InputCommand{T: 3000, Cmd: protocol.ClientCommand{
				Type: protocol.MessageTypeHello, Payload: json.RawMessage(`{}`),
			}})
		}), "hello"},
		{"存在しない手紙の load", clone(func(p *protocol.InternalRestorePayload) {
			p.InputLog.Commands = append(p.InputLog.Commands, protocol.InputCommand{T: 3000, Cmd: protocol.ClientCommand{
				Type: protocol.MessageTypeCmdFerryLoad, Payload: json.RawMessage(`{"ferryId":"f1","letterIds":[999999]}`),
			}})
		}), "does not reproduce"},
		{"until が最後の t より小さい", clone(func(p *protocol.InternalRestorePayload) { p.Until = 500 }), "out of order"},
		{"until が負", clone(func(p *protocol.InternalRestorePayload) { p.Until = -1 }), "out of order"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := newTestServer(t, ModeSingle, nil)
			c := ts.dialSingle(t)
			c.send(protocol.MessageTypeInternalRestore, "", tt.p)
			p := c.expectError(codeRestoreFailed, true)
			if !strings.Contains(p.Message, tt.msg) || len(p.Message) > restoreMessageMax {
				t.Errorf("message = %q, want %q", p.Message, tt.msg)
			}
			c.expectClose(websocket.StatusInternalError)
		})
	}

	// 読めない payload は bad_request、stageId がなければ stage_not_found。
	ts := newTestServer(t, ModeSingle, nil)
	c := ts.dialSingle(t)
	c.sendRaw(websocket.MessageText, []byte(`{"v":1,"type":"internal.restore","payload":{"sessionId":"s_x","inputLog":[]}}`))
	c.expectFatal(codeBadRequest, websocket.StatusPolicyViolation)
	c = ts.dialSingle(t)
	p := restorePayload("s_x", base, 0)
	p.StageID = "nope"
	c.send(protocol.MessageTypeInternalRestore, "", p)
	c.expectFatal(codeStageNotFound, websocket.StatusInternalError)
}

// RS-4: 空の commands。
func TestRestoreEmpty(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	ref := newRefEngine(t, ts.srv.cat)
	c := ts.dialSingle(t)
	c.send(protocol.MessageTypeInternalRestore, "", restorePayload("s_restore.4", ref.inputLog(testBuild), 0))
	var snap protocol.Snapshot
	env := c.expect(protocol.MessageTypeSnapshot, &snap)
	if snap.Phase != protocol.PhaseIntro || snap.Clock.T != 0 || !snap.Session.Restored || !snap.Clock.Paused {
		t.Errorf("snapshot = %s", env.Payload)
	}
}

// RS-5: until が DurationMs なら、ended の snapshot → round.result → internal.result。
func TestRestoreEnded(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	ref := newRefEngine(t, ts.srv.cat)
	ref.apply(sim.Command{Type: sim.CmdGameStart})
	c := ts.dialSingle(t)
	c.send(protocol.MessageTypeInternalRestore, "", restorePayload("s_restore.5", ref.inputLog(testBuild), ref.st.DurationMs))
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	if snap.Phase != protocol.PhaseEnded || snap.Clock.T != ref.st.DurationMs {
		t.Errorf("phase %s t %d", snap.Phase, snap.Clock.T)
	}
	r1 := c.expect(protocol.MessageTypeRoundResult, nil)
	r2 := c.expect(protocol.MessageTypeInternalResult, nil)
	if string(r1.Payload) != string(r2.Payload) {
		t.Errorf("round.result %s != internal.result %s", r1.Payload, r2.Payload)
	}
	assertNoNullArrays(t, r1.Payload)
	var res protocol.RoundResult
	if err := json.Unmarshal(r1.Payload, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Stats.FinalLogs) != 3 || !res.InputLogAvailable || res.Stats.LettersDelivered == nil {
		t.Errorf("result = %s", r1.Payload)
	}
	c.pingPong()
}

// RS-6: build の違いだけなら warn を出して成功する。
func TestRestoreAcrossBuilds(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	ref := newRefEngine(t, ts.srv.cat)
	ref.play()
	c := ts.dialSingle(t)
	c.send(protocol.MessageTypeInternalRestore, "", restorePayload("s_restore.6", ref.inputLog("git:old0000"), 0))
	c.expect(protocol.MessageTypeSnapshot, nil)
	logs := ts.logs.String()
	if !strings.Contains(logs, `"level":"WARN","msg":"restore across builds"`) ||
		!strings.Contains(logs, `"logBuild":"git:old0000"`) || !strings.Contains(logs, `"build":"test-build"`) {
		t.Errorf("logs do not have the warning:\n%s", logs)
	}
	if !strings.Contains(logs, `"msg":"restore ok"`) {
		t.Errorf("logs do not have restore ok:\n%s", logs)
	}
	if strings.Contains(logs, "inputLog") || strings.Contains(logs, "letterIds") {
		t.Errorf("logs contain payloads:\n%s", logs)
	}
}

// RS-7: 生きているセッションへの同じ sessionId の internal.restore はつなぎ直し（入力ログは使わない）。
func TestRestoreReconnect(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	c1 := ts.dialSingle(t)
	c1.initSingle("s_restore.7", 1)
	c2 := ts.dialSingle(t)
	c2.send(protocol.MessageTypeInternalRestore, "", restorePayload("s_restore.7", protocol.InputLog{Format: 99}, 0))
	var snap protocol.Snapshot
	c2.expect(protocol.MessageTypeSnapshot, &snap)
	if snap.Session.Restored || snap.Clock.Paused || snap.Session.Seed != 1 {
		t.Errorf("session %+v clock %+v", snap.Session, snap.Clock)
	}
	c1.expectFatal(codeReplaced, websocket.StatusNormalClosure)
}
