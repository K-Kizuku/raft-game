package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// 自動航行（cmd.ferry.auto。T-724）のテスト。船が 2 隻のステージがいるので、提供データの stage2 を使う。

const (
	autoStage = "stage2"
	autoSeed  = 20261101
)

// dataCatalog はリポジトリの data/ のステージ定義を読む。
func dataCatalog(t *testing.T) *Catalog {
	t.Helper()
	cat, err := LoadCatalog(os.DirFS(filepath.Join("..", "..", "..", "data")))
	if err != nil {
		t.Fatalf("LoadCatalog(data): %v", err)
	}
	if _, ok := cat.Lookup(autoStage); !ok {
		t.Fatalf("data に %s がない", autoStage)
	}
	return cat
}

// newAutoServer は data/ のステージで動く single か standalone のサーバー。
func newAutoServer(t *testing.T, mode Mode, autoFerry bool) *testServer {
	t.Helper()
	cat := dataCatalog(t)
	return newTestServer(t, mode, func(c *Config, s *Server) {
		s.cat = cat
		c.AutoFerry = autoFerry
	})
}

// doRecorder は、Durable Object と同じ規則で入力ログを組み立てる（DesignDoc §12.3「入力ログと復元」）。
// cmdId のあるコマンドを覚え、ok: true の ack で (t, cmd) を足す。cmd.ferry.auto は覚えない。
// internal.input はそのまま足す。
type doRecorder struct {
	c       *client
	pending map[string]protocol.ClientCommand
	log     []protocol.InputCommand
	inputs  []protocol.InputCommand // internal.input で届いたもの
	acks    map[string]protocol.Ack
}

func newDORecorder(c *client) *doRecorder {
	return &doRecorder{c: c, pending: map[string]protocol.ClientCommand{}, acks: map[string]protocol.Ack{}}
}

// send はブラウザのコマンドを送る。
func (r *doRecorder) send(typ protocol.MessageType, cmdID string, payload any) {
	r.c.t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		r.c.t.Fatal(err)
	}
	if typ != protocol.MessageTypeCmdFerryAuto {
		r.pending[cmdID] = protocol.ClientCommand{Type: typ, Payload: b}
	}
	r.c.send(typ, cmdID, json.RawMessage(b))
}

// drain は ping を送り、pong までに届いた電文を処理する。
func (r *doRecorder) drain() {
	r.c.t.Helper()
	r.c.send(protocol.MessageTypePing, "", protocol.PingPayload{ClientTime: 1})
	for {
		env := r.c.recv()
		switch env.Type {
		case protocol.MessageTypePong:
			return
		case protocol.MessageTypeAck:
			var a protocol.Ack
			if err := json.Unmarshal(env.Payload, &a); err != nil {
				r.c.t.Fatal(err)
			}
			r.acks[a.CmdID] = a
			if cmd, ok := r.pending[a.CmdID]; ok {
				delete(r.pending, a.CmdID)
				if a.OK {
					r.log = append(r.log, protocol.InputCommand{T: a.T, Cmd: cmd})
				}
			}
		case protocol.MessageTypeInternalInput:
			var in protocol.InputCommand
			if err := json.Unmarshal(env.Payload, &in); err != nil {
				r.c.t.Fatalf("internal.input %s: %v", env.Payload, err)
			}
			r.inputs = append(r.inputs, in)
			r.log = append(r.log, in)
		}
	}
}

// ack は cmdID の ack（drain の後で読む）。
func (r *doRecorder) ack(cmdID string) protocol.Ack {
	r.c.t.Helper()
	a, ok := r.acks[cmdID]
	if !ok {
		r.c.t.Fatalf("ack %s がない", cmdID)
	}
	return a
}

// stepFor は、セッションのステップ（loop.go）を 250ms ずつ n 回動かす（1 倍速でゲーム内の 250ms ずつ）。
func stepFor(sess *session, n int) {
	for range n {
		sess.do(func() { sess.step(sess.lastStep.Add(250 * time.Millisecond)) })
	}
}

// ferryAuto は cmd.ferry.auto の payload。
func ferryAuto(id string, on bool) protocol.FerryAutoPayload {
	return protocol.FerryAutoPayload{FerryID: id, Enabled: on}
}

// AF-1・AF-2（AC-1・AC-2）: f2 を自動にすると novice が f2 だけを動かし、プレイヤーは f1 を操作できる。
// ボットの操作は internal.input で届き、DO と同じ規則で組み立てた入力ログが Engine の入力ログと一致し、
// その入力ログから復元すると同じ状態・同じトレースになる。
func TestAutoFerryBotAndRestore(t *testing.T) {
	t.Parallel()
	ts := newAutoServer(t, ModeSingle, false)
	c := ts.dialSingle(t)
	initP := protocol.InternalInitPayload{SessionID: "s_auto.1", StageID: autoStage, Seed: autoSeed, AutoFerry: true}
	c.send(protocol.MessageTypeInternalInit, "", initP)
	c.expect(protocol.MessageTypeSnapshot, nil)
	sess := ts.singleSession(t)
	r := newDORecorder(c)

	r.send(protocol.MessageTypeCmdFerryAuto, "a-1", ferryAuto("f2", true))
	r.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
	r.drain()
	if a := r.ack("a-1"); !a.OK {
		t.Fatalf("cmd.ferry.auto ack = %+v", a)
	}
	if a := r.ack("c-1"); !a.OK {
		t.Fatalf("cmd.game.start ack = %+v", a)
	}

	// novice は着いてから 3 秒待つ（ラウンドの始めは時刻 0 に着いたとみなす）。
	stepFor(sess, 20)
	r.drain()
	if len(r.inputs) == 0 {
		t.Fatal("ボットの操作（internal.input）が届かない")
	}

	// プレイヤーは f1 を操作できる（自動でない船）。
	var f1 sim.FerryView
	var to uint64
	sess.do(func() {
		snap := sess.engine.Snapshot()
		for _, f := range snap.Ferries {
			if f.ID == "f1" {
				f1 = f
			}
		}
		for _, rt := range sess.st.Routes {
			if f1.State == "docked" && (rt.A == f1.IslandID || rt.B == f1.IslandID) {
				to = rt.A + rt.B - f1.IslandID
				break
			}
		}
	})
	if f1.State != "docked" || to == 0 {
		t.Fatalf("f1 = %+v（港にいて航路があるはず）", f1)
	}
	r.send(protocol.MessageTypeCmdFerryDepart, "c-2", protocol.FerryToPayload{FerryID: "f1", To: to})
	r.drain()
	if a := r.ack("c-2"); !a.OK {
		t.Fatalf("f1 の出航 ack = %+v", a)
	}

	stepFor(sess, 40)
	r.drain()

	// ボットは f2 だけを操作し、出航もする。
	departed := false
	for _, in := range r.inputs {
		cmd, err := simconv.DecodeEngineCommand(in.Cmd.Type, in.Cmd.Payload)
		if err != nil {
			t.Fatalf("internal.input %+v: %v", in, err)
		}
		if cmd.FerryID != "f2" {
			t.Errorf("ボットが f2 以外を操作した: %+v", in)
		}
		departed = departed || cmd.Type == sim.CmdFerryDepart
	}
	if !departed {
		t.Errorf("ボットが f2 を出航させていない: %s", mustJSON(t, r.inputs))
	}

	// snapshot の session.autoFerries（つなぎ直した画面が状態を取り直す）。
	c.send(protocol.MessageTypeCmdSync, "s-1", struct{}{})
	c.expect(protocol.MessageTypeAck, nil)
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	if !slices.Equal(snap.Session.AutoFerries, []string{"f2"}) {
		t.Errorf("session.autoFerries = %v, want [f2]", snap.Session.AutoFerries)
	}

	// DO の入力ログ（ack と internal.input から組み立てたもの）は、Engine の入力ログと同じ。
	var (
		engineLog []protocol.InputCommand
		now       sim.SimTime
		want      protocol.Snapshot
		wantHash  [32]byte
	)
	sess.do(func() {
		for _, en := range sess.engine.InputLog() {
			cc, err := simconv.EncodeEngineCommand(en.Cmd)
			if err != nil {
				t.Error(err)
				return
			}
			engineLog = append(engineLog, protocol.InputCommand{T: int64(en.T), Cmd: cc})
		}
		now = sess.engine.Now()
		want = simconv.SnapshotFromSim(sess.engine.Snapshot())
		wantHash = sess.engine.TraceHash()
	})
	if got, w := mustJSON(t, r.log), mustJSON(t, engineLog); got != w {
		t.Fatalf("DO の入力ログ =\n%s\nEngine の入力ログ =\n%s", got, w)
	}

	// 復元（別のコンテナ）で同じ状態・同じトレースになる。
	ts2 := newAutoServer(t, ModeSingle, false)
	c2 := ts2.dialSingle(t)
	st, _ := ts2.srv.cat.Lookup(autoStage)
	restore := protocol.InternalRestorePayload{
		InternalInitPayload: initP,
		InputLog: protocol.InputLog{
			Format: sim.ReplayFormat, Build: testBuild, StageID: autoStage, StageHash: st.Hash, Seed: autoSeed, Commands: r.log,
		},
		Until: int64(now),
	}
	c2.send(protocol.MessageTypeInternalRestore, "", restore)
	var restored protocol.Snapshot
	c2.expect(protocol.MessageTypeSnapshot, &restored)
	if got, w := snapshotParts(t, restored), snapshotParts(t, want); got != w {
		t.Fatalf("復元した snapshot =\n%s\nwant\n%s", got, w)
	}
	if restored.Session.AutoFerries != nil {
		t.Errorf("復元したセッションの autoFerries = %v（自動航行は引き継がない）", restored.Session.AutoFerries)
	}
	sess2 := ts2.singleSession(t)
	var gotHash [32]byte
	sess2.do(func() { gotHash = sess2.engine.TraceHash() })
	if gotHash != wantHash {
		t.Errorf("復元したトレースのハッシュが違う")
	}

	// 自動をやめると、ボットは f2 を操作しない。
	r.send(protocol.MessageTypeCmdFerryAuto, "a-2", ferryAuto("f2", false))
	r.drain()
	if a := r.ack("a-2"); !a.OK {
		t.Fatalf("cmd.ferry.auto（false）ack = %+v", a)
	}
	n := len(r.inputs)
	stepFor(sess, 40)
	r.drain()
	if len(r.inputs) != n {
		t.Errorf("手動に戻した後もボットが操作した: %s", mustJSON(t, r.inputs[n:]))
	}
}

// AF-1b: ボットが動いている間も、ブラウザに届く電文（internal.* 以外）の seq は 1 から抜けなくつながる。
// internal.input は seq を持たない（DO はブラウザに送らないので、seq を使うとブラウザが抜けとみなして
// cmd.sync を繰り返す）。
func TestAutoFerrySeqHasNoGaps(t *testing.T) {
	t.Parallel()
	ts := newAutoServer(t, ModeSingle, false)
	c := ts.dialSingle(t)
	initP := protocol.InternalInitPayload{SessionID: "s_auto.seq", StageID: autoStage, Seed: autoSeed, AutoFerry: true}
	c.send(protocol.MessageTypeInternalInit, "", initP)
	if env := c.expect(protocol.MessageTypeSnapshot, nil); env.Seq != 1 {
		t.Fatalf("snapshot の seq = %d, want 1", env.Seq)
	}
	sess := ts.singleSession(t)
	last := uint64(1)
	inputs := 0
	// drain は ping を送り、pong までに届いた電文の seq を確かめる。
	drain := func() {
		t.Helper()
		c.send(protocol.MessageTypePing, "", protocol.PingPayload{ClientTime: 1})
		for {
			env := c.recv()
			if isInternal(env.Type) {
				if env.Seq != 0 {
					t.Errorf("%s の seq = %d（internal.* は seq を持たない）", env.Type, env.Seq)
				}
				if env.Type == protocol.MessageTypeInternalInput {
					inputs++
				}
				continue
			}
			if env.Seq != last+1 {
				t.Errorf("%s の seq = %d, want %d（抜けがある）", env.Type, env.Seq, last+1)
			}
			last = env.Seq
			if env.Type == protocol.MessageTypePong {
				return
			}
		}
	}
	c.send(protocol.MessageTypeCmdFerryAuto, "a-1", ferryAuto("f2", true))
	c.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
	drain()
	stepFor(sess, 60)
	drain()
	c.send(protocol.MessageTypeInternalPing, "", struct{}{}) // internal.pong も seq を持たない
	drain()
	if inputs == 0 {
		t.Fatal("ボットの操作（internal.input）が届かない")
	}
}

// AF-3: 一時停止中はボットを動かさない。
func TestAutoFerryPaused(t *testing.T) {
	t.Parallel()
	ts := newAutoServer(t, ModeSingle, false)
	c := ts.dialSingle(t)
	c.send(protocol.MessageTypeInternalInit, "", protocol.InternalInitPayload{
		SessionID: "s_auto.3", StageID: autoStage, Seed: autoSeed, AutoFerry: true,
	})
	c.expect(protocol.MessageTypeSnapshot, nil)
	sess := ts.singleSession(t)
	r := newDORecorder(c)
	r.send(protocol.MessageTypeCmdFerryAuto, "a-1", ferryAuto("f1", true))
	r.send(protocol.MessageTypeCmdFerryAuto, "a-2", ferryAuto("f2", true))
	r.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
	r.send(protocol.MessageTypeCmdGamePause, "p-1", protocol.GamePausePayload{Paused: true})
	r.drain()
	stepFor(sess, 40)
	r.drain()
	if len(r.inputs) != 0 {
		t.Errorf("一時停止中にボットが操作した: %s", mustJSON(t, r.inputs))
	}
	var tNow sim.SimTime
	sess.do(func() { tNow = sess.engine.Now() })
	if tNow != 0 {
		t.Errorf("一時停止中に時刻が進んだ: %d", tNow)
	}
}

// AF-4（AC-3）: 自動航行が無効なら cmd.ferry.auto は feature_disabled。
func TestAutoFerryDisabled(t *testing.T) {
	t.Parallel()
	// single: internal.init に autoFerry がない（DO の AUTO_FERRY が "1" でない）。
	ts := newAutoServer(t, ModeSingle, true)
	c := ts.dialSingle(t)
	c.send(protocol.MessageTypeInternalInit, "", protocol.InternalInitPayload{SessionID: "s_auto.4", StageID: autoStage, Seed: 1})
	c.expect(protocol.MessageTypeSnapshot, nil)
	c.send(protocol.MessageTypeCmdFerryAuto, "a-1", ferryAuto("f2", true))
	var ack protocol.Ack
	c.expect(protocol.MessageTypeAck, &ack)
	if ack.OK || ack.Code != protocol.AckCodeFeatureDisabled || ack.CmdID != "a-1" {
		t.Errorf("ack = %+v, want feature_disabled", ack)
	}

	// standalone: --auto-ferry=false なら features.autoFerry は false で、cmd.ferry.auto は feature_disabled。
	for _, on := range []bool{false, true} {
		ts := newAutoServer(t, ModeStandalone, on)
		_, stages := getStages(t, ts)
		if stages.Features.AutoFerry != on {
			t.Errorf("--auto-ferry=%v: features.autoFerry = %v", on, stages.Features.AutoFerry)
		}
		created := ts.createSession(t, `{"stageId":"`+autoStage+`"}`)
		c := ts.dialSession(t, created)
		c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{Client: "test"})
		c.expect(protocol.MessageTypeSnapshot, nil)
		c.send(protocol.MessageTypeCmdFerryAuto, "a-1", ferryAuto("f2", true))
		var ack protocol.Ack
		c.expect(protocol.MessageTypeAck, &ack)
		if on && !ack.OK || !on && (ack.OK || ack.Code != protocol.AckCodeFeatureDisabled) {
			t.Errorf("--auto-ferry=%v: ack = %+v", on, ack)
		}
	}
}

// AF-5: cmd.ferry.auto の誤り。
func TestAutoFerryErrors(t *testing.T) {
	t.Parallel()
	ts := newAutoServer(t, ModeSingle, false)
	c := ts.dialSingle(t)
	c.send(protocol.MessageTypeInternalInit, "", protocol.InternalInitPayload{
		SessionID: "s_auto.5", StageID: autoStage, Seed: 1, AutoFerry: true,
	})
	c.expect(protocol.MessageTypeSnapshot, nil)
	tests := []struct {
		name    string
		payload any
		code    protocol.AckCode
	}{
		{"enabled がない", map[string]any{"ferryId": "f1"}, protocol.AckCodeBadRequest},
		{"ferryId がない", map[string]any{"enabled": true}, protocol.AckCodeBadRequest},
		{"ferryId が空", map[string]any{"ferryId": "", "enabled": true}, protocol.AckCodeBadRequest},
		{"enabled が文字列", map[string]any{"ferryId": "f1", "enabled": "yes"}, protocol.AckCodeBadRequest},
		{"知らない船", map[string]any{"ferryId": "f9", "enabled": true}, protocol.AckCodeBadRequest},
	}
	for i, tt := range tests {
		id := "e-" + string(rune('1'+i))
		c.send(protocol.MessageTypeCmdFerryAuto, id, tt.payload)
		var ack protocol.Ack
		c.expect(protocol.MessageTypeAck, &ack)
		if ack.OK || ack.Code != tt.code || ack.CmdID != id {
			t.Errorf("%s: ack = %+v, want %s", tt.name, ack, tt.code)
		}
	}
	// cmdId がなければ error（bad_request、fatal でない）。
	c.send(protocol.MessageTypeCmdFerryAuto, "", ferryAuto("f1", true))
	c.expectError(codeBadRequest, false)
	// 同じ状態にしても成功する（2 回目も ok）。
	for _, id := range []string{"o-1", "o-2"} {
		c.send(protocol.MessageTypeCmdFerryAuto, id, ferryAuto("f1", true))
		var ack protocol.Ack
		c.expect(protocol.MessageTypeAck, &ack)
		if !ack.OK {
			t.Errorf("%s: ack = %+v", id, ack)
		}
	}
	c.pingPong()
}

// AF-6: ラウンドの終わった後は phase_invalid。
func TestAutoFerryEnded(t *testing.T) {
	t.Parallel()
	ts := newAutoServer(t, ModeSingle, false)
	c := ts.dialSingle(t)
	c.send(protocol.MessageTypeInternalInit, "", protocol.InternalInitPayload{
		SessionID: "s_auto.6", StageID: autoStage, Seed: 1, AutoFerry: true,
	})
	c.expect(protocol.MessageTypeSnapshot, nil)
	sess := ts.singleSession(t)
	c.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
	c.expect(protocol.MessageTypeAck, nil)
	c.expect(protocol.MessageTypeEvents, nil)
	sess.do(func() { sess.advanceTo(sim.SimTime(sess.st.DurationMs)) })
	c.expect(protocol.MessageTypeEvents, nil)
	c.expect(protocol.MessageTypeRoundResult, nil)
	c.expect(protocol.MessageTypeInternalResult, nil)
	c.send(protocol.MessageTypeCmdFerryAuto, "a-1", ferryAuto("f1", true))
	var ack protocol.Ack
	c.expect(protocol.MessageTypeAck, &ack)
	if ack.OK || ack.Code != protocol.AckCodePhaseInvalid {
		t.Errorf("ack = %+v, want phase_invalid", ack)
	}
}

// AF-7: standalone では internal.input を送らない（入力ログは Durable Object だけが持つ）。
func TestAutoFerryStandaloneNoInternalInput(t *testing.T) {
	t.Parallel()
	ts := newAutoServer(t, ModeStandalone, true)
	created := ts.createSession(t, `{"stageId":"`+autoStage+`"}`)
	c := ts.dialSession(t, created)
	c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{Client: "test"})
	c.expect(protocol.MessageTypeSnapshot, nil)
	sess := ts.standaloneSession(t, created.SessionID)
	r := newDORecorder(c)
	r.send(protocol.MessageTypeCmdFerryAuto, "a-1", ferryAuto("f2", true))
	r.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
	r.drain()
	stepFor(sess, 40)
	r.drain()
	if len(r.inputs) != 0 {
		t.Errorf("standalone が internal.input を送った: %s", mustJSON(t, r.inputs))
	}
	var n int
	sess.do(func() {
		for _, en := range sess.engine.InputLog() {
			if en.Cmd.FerryID == "f2" {
				n++
			}
		}
	})
	if n == 0 {
		t.Error("standalone でボットが f2 を操作していない")
	}
}
