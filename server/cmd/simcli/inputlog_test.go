package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// sampleLog は 5 種類のコマンドを含む入力ログ。
func sampleLog() inputLog {
	return inputLog{
		Format:    1,
		Build:     "git:3f2c1ab",
		StageID:   "stage1",
		StageHash: "sha256:0123",
		Seed:      18446744073709551615,
		Commands: []sim.InputEntry{
			{T: 0, Cmd: sim.Command{Type: sim.CmdGameStart}},
			{T: 1500, Cmd: sim.Command{Type: sim.CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{12, 13}}},
			{T: 1500, Cmd: sim.Command{Type: sim.CmdFerryUnload, FerryID: "f1", LetterIDs: nil}},
			{T: 1500, Cmd: sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2}},
			{T: 3000, Cmd: sim.Command{Type: sim.CmdFerryAutoload, FerryID: "f2", To: 3}},
		},
	}
}

// normalized は比べるためのフィールドだけを残し、空の LetterIDs を nil にそろえる。
func normalized(l inputLog) inputLog {
	out := inputLog{Format: l.Format, Build: l.Build, StageID: l.StageID, StageHash: l.StageHash, Seed: l.Seed}
	for _, c := range l.Commands {
		if len(c.Cmd.LetterIDs) == 0 {
			c.Cmd.LetterIDs = nil
		}
		out.Commands = append(out.Commands, c)
	}
	return out
}

func decodeString(s string) (inputLog, error) { return decodeInputLog(strings.NewReader(s)) }

// I-1: 往復。
func TestInputLogRoundTrip(t *testing.T) {
	t.Parallel()
	in := sampleLog()
	data, err := encodeInputLog(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeInputLog(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(normalized(out), normalized(in)) {
		t.Errorf("round trip:\n got %+v\nwant %+v", out, in)
	}
	if out.Ignored != 0 {
		t.Errorf("ignored = %d", out.Ignored)
	}
}

// I-2: バイト列の形。
func TestInputLogBytes(t *testing.T) {
	t.Parallel()
	a, err := encodeInputLog(sampleLog())
	if err != nil {
		t.Fatal(err)
	}
	b, err := encodeInputLog(sampleLog())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("同じ値から違うバイト列")
	}
	if !json.Valid(a) {
		t.Errorf("not valid JSON:\n%s", a)
	}
	want := `{"format":1,"build":"git:3f2c1ab","stageId":"stage1","stageHash":"sha256:0123","seed":18446744073709551615,"commands":[
{"t":0,"cmd":{"type":"cmd.game.start","payload":{}}},
{"t":1500,"cmd":{"type":"cmd.ferry.load","payload":{"ferryId":"f1","letterIds":[12,13]}}},
{"t":1500,"cmd":{"type":"cmd.ferry.unload","payload":{"ferryId":"f1","letterIds":[]}}},
{"t":1500,"cmd":{"type":"cmd.ferry.depart","payload":{"ferryId":"f1","to":2}}},
{"t":3000,"cmd":{"type":"cmd.ferry.autoload","payload":{"ferryId":"f2","to":3}}}
]}
`
	if string(a) != want {
		t.Errorf("bytes:\n%s\nwant:\n%s", a, want)
	}

	empty := sampleLog()
	empty.Commands = nil
	e, err := encodeInputLog(empty)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"format":1,"build":"git:3f2c1ab","stageId":"stage1","stageHash":"sha256:0123","seed":18446744073709551615,"commands":[]}` + "\n"; string(e) != want {
		t.Errorf("empty:\n%s\nwant:\n%s", e, want)
	}
	if !json.Valid(e) {
		t.Errorf("not valid JSON: %s", e)
	}
}

// I-3: DesignDoc §10.1 の形。
func TestInputLogDesignDocShape(t *testing.T) {
	t.Parallel()
	l, err := decodeString(`{
  "format": 1,
  "build": "git:3f2c1ab",
  "stageId": "stage2",
  "stageHash": "sha256:…",
  "seed": 20261101,
  "commands": [
    {"t": 0,    "cmd": {"type": "cmd.game.start",   "payload": {}}},
    {"t": 0,    "cmd": {"type": "cmd.ferry.load",   "payload": {"ferryId": "f1", "letterIds": [12, 13]}}},
    {"t": 1200, "cmd": {"type": "cmd.ferry.depart", "payload": {"ferryId": "f1", "to": 2}}}
  ]
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Commands) != 3 {
		t.Fatalf("commands = %d", len(l.Commands))
	}
	want := sim.InputEntry{T: 1200, Cmd: sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2}}
	if !reflect.DeepEqual(l.Commands[2], want) {
		t.Errorf("commands[2] = %+v, want %+v", l.Commands[2], want)
	}
	if l.StageID != "stage2" || l.Seed != 20261101 || l.Build != "git:3f2c1ab" {
		t.Errorf("header = %+v", l)
	}
}

// I-4: 読み込みの誤り。エラーの文に場所が入る。
func TestInputLogDecodeErrors(t *testing.T) {
	t.Parallel()
	const head = `"format":1,"build":"b","stageId":"s","stageHash":"sha256:x","seed":1`
	withCmd := func(entry string) string { return `{` + head + `,"commands":[` + entry + `]}` }
	cases := []struct {
		name string
		in   string
		want string // エラーの文に含まれる場所
	}{
		{"format 2", `{"format":2,"stageId":"s","stageHash":"h","seed":1,"commands":[]}`, "format"},
		{"format がない", `{"stageId":"s","stageHash":"h","seed":1,"commands":[]}`, "format"},
		{"stageId が空", `{"format":1,"stageId":"","stageHash":"h","seed":1,"commands":[]}`, "stageId"},
		{"stageHash がない", `{"format":1,"stageId":"s","seed":1,"commands":[]}`, "stageHash"},
		{"seed がない", `{"format":1,"stageId":"s","stageHash":"h","commands":[]}`, "seed"},
		{"commands がない", `{` + head + `}`, "commands"},
		{"commands が null", `{` + head + `,"commands":null}`, "commands"},
		{"最上位の未知のキー", `{` + head + `,"commands":[],"extra":1}`, `"extra"`},
		{"cmd の未知のキー", withCmd(`{"t":0,"cmd":{"type":"cmd.game.start","payload":{},"x":1}}`), "commands[0].cmd:"},
		{"entry の未知のキー", withCmd(`{"t":0,"cmd":{"type":"cmd.game.start","payload":{}},"y":1}`), "commands[0]:"},
		{"payload の未知のキー", withCmd(`{"t":0,"cmd":{"type":"cmd.ferry.depart","payload":{"ferryId":"f1","to":2,"z":1}}}`), "commands[0].cmd.payload:"},
		{"start の payload の未知のキー", withCmd(`{"t":0,"cmd":{"type":"cmd.game.start","payload":{"z":1}}}`), "commands[0].cmd.payload:"},
		{"未知の type", withCmd(`{"t":0,"cmd":{"type":"cmd.relay.load","payload":{}}}`), "commands[0].cmd.type"},
		{"type がない", withCmd(`{"t":0,"cmd":{"payload":{}}}`), "commands[0].cmd.type"},
		{"cmd がない", withCmd(`{"t":0}`), "commands[0].cmd"},
		{"payload がない", withCmd(`{"t":0,"cmd":{"type":"cmd.game.start"}}`), "commands[0].cmd.payload"},
		{"t: -1", withCmd(`{"t":-1,"cmd":{"type":"cmd.game.start","payload":{}}}`), "commands[0].t"},
		{"t: 1.5", withCmd(`{"t":1.5,"cmd":{"type":"cmd.game.start","payload":{}}}`), "commands[0].t"},
		{`t: "0"`, withCmd(`{"t":"0","cmd":{"type":"cmd.game.start","payload":{}}}`), "commands[0].t"},
		{"t がない", withCmd(`{"cmd":{"type":"cmd.game.start","payload":{}}}`), "commands[0].t"},
		{"load の letterIds がない", withCmd(`{"t":0,"cmd":{"type":"cmd.ferry.load","payload":{"ferryId":"f1"}}}`), "commands[0].cmd.payload.letterIds"},
		{"letterIds の要素が負", withCmd(`{"t":0,"cmd":{"type":"cmd.ferry.unload","payload":{"ferryId":"f1","letterIds":[-1]}}}`), "commands[0].cmd.payload:"},
		{"depart の to がない", withCmd(`{"t":0,"cmd":{"type":"cmd.ferry.depart","payload":{"ferryId":"f1"}}}`), "commands[0].cmd.payload.to"},
		{"autoload の to がない", withCmd(`{"t":0,"cmd":{"type":"cmd.ferry.autoload","payload":{"ferryId":"f1"}}}`), "commands[0].cmd.payload.to"},
		{"ferryId が空", withCmd(`{"t":0,"cmd":{"type":"cmd.ferry.depart","payload":{"ferryId":"","to":2}}}`), "commands[0].cmd.payload.ferryId"},
		{"ferryId がない", withCmd(`{"t":0,"cmd":{"type":"cmd.ferry.load","payload":{"letterIds":[]}}}`), "commands[0].cmd.payload.ferryId"},
		{"2 件目の誤り", withCmd(`{"t":0,"cmd":{"type":"cmd.game.start","payload":{}}},{"t":5,"cmd":{"type":"cmd.ferry.depart","payload":{"ferryId":"f1"}}}`), "commands[1].cmd.payload.to"},
		{"JSON の後ろの余分なデータ", `{` + head + `,"commands":[]} {}`, "余分なデータ"},
		{"壊れた JSON", `{"format":1,`, "入力ログ"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l, err := decodeString(c.in)
			if err == nil {
				t.Fatalf("decode = %+v, want error", l)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q に %q がない", err, c.want)
			}
		})
	}
}

// load の letterIds が空の配列なのは形式の誤りにしない（再生で Engine が bad_request にする）。
func TestInputLogEmptyLetterIDs(t *testing.T) {
	t.Parallel()
	l, err := decodeString(`{"format":1,"stageId":"s","stageHash":"h","seed":0,"commands":[{"t":0,"cmd":{"type":"cmd.ferry.load","payload":{"ferryId":"f1","letterIds":[]}}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Commands) != 1 || len(l.Commands[0].Cmd.LetterIDs) != 0 || l.Build != "" {
		t.Errorf("l = %+v", l)
	}
}

// I-5: セッションのコマンドは捨てられ、件数が分かる。
func TestInputLogIgnoresSessionCommands(t *testing.T) {
	t.Parallel()
	l, err := decodeString(`{"format":1,"stageId":"s","stageHash":"h","seed":1,"commands":[
{"t":0,"cmd":{"type":"cmd.game.start","payload":{}}},
{"t":10,"cmd":{"type":"cmd.game.pause","payload":{"paused":true}}},
{"t":20,"cmd":{"type":"cmd.game.speed","payload":{"x":2}}},
{"t":30,"cmd":{"type":"cmd.game.skip","payload":{}}},
{"t":40,"cmd":{"type":"cmd.ferry.depart","payload":{"ferryId":"f1","to":2}}}
]}`)
	if err != nil {
		t.Fatal(err)
	}
	if l.Ignored != 3 {
		t.Errorf("ignored = %d, want 3", l.Ignored)
	}
	want := []sim.InputEntry{
		{T: 0, Cmd: sim.Command{Type: sim.CmdGameStart}},
		{T: 40, Cmd: sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2}},
	}
	if !reflect.DeepEqual(l.Commands, want) {
		t.Errorf("commands = %+v, want %+v", l.Commands, want)
	}
	if !reflect.DeepEqual(l.Pos, []int{0, 4}) {
		t.Errorf("pos = %v, want [0 4]", l.Pos)
	}
}

// I-6: 表にないコマンドは書き出しのエラー。
func TestInputLogEncodeUnknown(t *testing.T) {
	t.Parallel()
	l := sampleLog()
	l.Commands = append(l.Commands, sim.InputEntry{T: 4000, Cmd: sim.Command{Type: "cmd.game.pause"}})
	if data, err := encodeInputLog(l); err == nil {
		t.Errorf("encode = %s, want error", data)
	} else if !strings.Contains(err.Error(), "commands[5]") {
		t.Errorf("error = %v", err)
	}
}

// CL-D1: demo.trigger の行を含む入力ログの往復（バイト列も）。
func TestInputLogDemoTrigger(t *testing.T) {
	t.Parallel()
	in := sampleLog()
	in.Commands = append(in.Commands,
		sim.InputEntry{T: 5000, Cmd: sim.Command{Type: sim.CmdDemoTrigger, Event: sim.DemoEventStorm}},
		sim.InputEntry{T: 5000, Cmd: sim.Command{Type: sim.CmdDemoTrigger, Event: sim.DemoEventStorm}},
	)
	data, err := encodeInputLog(in)
	if err != nil {
		t.Fatal(err)
	}
	const line = `{"t":5000,"cmd":{"type":"demo.trigger","payload":{"event":"storm"}}}`
	if !strings.HasSuffix(string(data), line+",\n"+line+"\n]}\n") {
		t.Errorf("bytes:\n%s\nwant the last lines %s", data, line)
	}
	out, err := decodeInputLog(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(normalized(out), normalized(in)) {
		t.Errorf("round trip:\n got %+v\nwant %+v", out, in)
	}
	again, err := encodeInputLog(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, data) {
		t.Errorf("bytes after round trip:\n%s\nwant:\n%s", again, data)
	}

	// 値が storm かは読み込みで検査しない（Replay が bad_request → ErrReplayDiverged にする）。
	l, err := decodeString(`{"format":1,"stageId":"s","stageHash":"h","seed":0,"commands":[{"t":0,"cmd":{"type":"demo.trigger","payload":{"event":"rush"}}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := (sim.Command{Type: sim.CmdDemoTrigger, Event: "rush"}); len(l.Commands) != 1 || !reflect.DeepEqual(l.Commands[0].Cmd, want) {
		t.Errorf("commands = %+v, want [%+v]", l.Commands, want)
	}
}

// CL-D2: demo.trigger の payload の誤り。
func TestInputLogDemoTriggerErrors(t *testing.T) {
	t.Parallel()
	withPayload := func(payload string) string {
		return `{"format":1,"stageId":"s","stageHash":"h","seed":0,"commands":[{"t":0,"cmd":{"type":"cmd.game.start","payload":{}}},` +
			`{"t":0,"cmd":{"type":"demo.trigger","payload":` + payload + `}}]}`
	}
	cases := []struct {
		name, payload, want string
	}{
		{"event がない", `{}`, "commands[1].cmd.payload.event: ない"},
		{"event が空", `{"event":""}`, "commands[1].cmd.payload.event: 空"},
		{"未知のキー", `{"event":"storm","x":1}`, "commands[1].cmd.payload:"},
		{"event が数", `{"event":1}`, "commands[1].cmd.payload:"},
		{"payload が null", `null`, "commands[1].cmd.payload: ない"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l, err := decodeString(withPayload(c.payload))
			if err == nil {
				t.Fatalf("decode = %+v, want error", l)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q に %q がない", err, c.want)
			}
		})
	}
}
