package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/bot"
	"github.com/K-Kizuku/raft-game/server/internal/replaykit"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// テストはネイティブでも GOOS=js GOARCH=wasm でも動く（ビルドタグなし）。ステージ定義はリポジトリの data/ を読み、
// 入力ログはボットに遊ばせて作る（期待のハッシュをファイルに固定しない）。

var testCat *catalog

func repoCatalog(t *testing.T) *catalog {
	t.Helper()
	if testCat == nil {
		c, err := loadCatalog(os.DirFS("../../../data"))
		if err != nil {
			t.Fatalf("loadCatalog: %v", err)
		}
		testCat = c
	}
	return testCat
}

func mustStage(t *testing.T, id string) *stage.Stage {
	t.Helper()
	st, _, ok := repoCatalog(t).lookup(id)
	if !ok {
		t.Fatalf("ステージ %s がない", id)
	}
	return st
}

// played はボットに 1 ラウンド遊ばせた結果。
type played struct {
	st        *stage.Stage
	log       replaykit.InputLog
	traceHash string
	events    int
	stars     int
}

func play(t *testing.T, stageID, botName string, seed uint64) played {
	t.Helper()
	st := mustStage(t, stageID)
	cfg, err := stage.SimConfig(st)
	if err != nil {
		t.Fatal(err)
	}
	e, err := sim.New(cfg, seed, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := bot.New(botName, seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := bot.Play(e, b); err != nil {
		t.Fatal(err)
	}
	events := e.DrainEvents()
	h := e.TraceHash()
	r, _ := e.Result()
	return played{
		st: st,
		log: replaykit.InputLog{
			Format: replaykit.InputLogFormat, Build: "git:test123", StageID: st.ID, StageHash: st.Hash,
			Seed: seed, Commands: e.InputLog(),
		},
		traceHash: "sha256:" + hex.EncodeToString(h[:]),
		events:    len(events),
		stars:     r.Stars,
	}
}

func encode(t *testing.T, l replaykit.InputLog) []byte {
	t.Helper()
	if l.Commands == nil {
		l.Commands = []sim.InputEntry{}
	}
	b, err := replaykit.EncodeInputLog(l)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// okBody は成功の応答のうちテストで見る分。
type okBody struct {
	OK      bool   `json:"ok"`
	Build   string `json:"build"`
	Ignored int    `json:"ignored"`
	Record  struct {
		Kind       string            `json:"kind"`
		Build      string            `json:"build"`
		StageID    string            `json:"stageId"`
		Bot        *string           `json:"bot"`
		EventCount int               `json:"eventCount"`
		TraceHash  string            `json:"traceHash"`
		Snapshot   json.RawMessage   `json:"snapshot"`
		Events     []json.RawMessage `json:"events"`
	} `json:"record"`
	Stage struct {
		Title   string            `json:"title"`
		Islands []json.RawMessage `json:"islands"`
	} `json:"stage"`
	IslandsState []json.RawMessage `json:"islandsState"`
	Score        *json.RawMessage  `json:"score"`
	Result       struct {
		Stars             int  `json:"stars"`
		InputLogAvailable bool `json:"inputLogAvailable"`
	} `json:"result"`
}

// errBody は失敗の応答。
type errBody struct {
	OK       bool    `json:"ok"`
	Build    string  `json:"build"`
	Code     string  `json:"code"`
	Message  string  `json:"message"`
	StageID  *string `json:"stageId"`
	LogBuild *string `json:"logBuild"`
}

func decodeOK(t *testing.T, out []byte) okBody {
	t.Helper()
	var b okBody
	if err := json.Unmarshal(out, &b); err != nil {
		t.Fatalf("応答が JSON でない: %v\n%.300s", err, out)
	}
	if !b.OK {
		t.Fatalf("ok でない: %.500s", out)
	}
	return b
}

func decodeErr(t *testing.T, out []byte, code string) errBody {
	t.Helper()
	var b errBody
	if err := json.Unmarshal(out, &b); err != nil {
		t.Fatalf("応答が JSON でない: %v\n%.300s", err, out)
	}
	if b.OK || b.Code != code {
		t.Fatalf("ok=%v code=%q（want ok=false code=%q） message=%q", b.OK, b.Code, code, b.Message)
	}
	if b.Message == "" {
		t.Errorf("message が空")
	}
	if b.Build != build {
		t.Errorf("build = %q（want %q）", b.Build, build)
	}
	return b
}

func checkReplayOK(t *testing.T, p played) {
	t.Helper()
	out := handle(encode(t, p.log), repoCatalog(t), build)
	b := decodeOK(t, out)
	if b.Build != build {
		t.Errorf("build = %q（want %q）", b.Build, build)
	}
	if b.Record.Kind != replaykit.RecordKind || b.Record.StageID != p.st.ID || b.Record.Build != build {
		t.Errorf("record のヘッダー: kind=%q stageId=%q build=%q", b.Record.Kind, b.Record.StageID, b.Record.Build)
	}
	if b.Record.TraceHash != p.traceHash {
		t.Errorf("traceHash = %s（遊ばせた Engine: %s）", b.Record.TraceHash, p.traceHash)
	}
	if b.Record.Bot == nil || *b.Record.Bot != "" {
		t.Errorf("record.bot = %v（want \"\"）", b.Record.Bot)
	}
	if b.Record.EventCount != p.events || len(b.Record.Events) != p.events {
		t.Errorf("eventCount = %d・events %d 件（want %d）", b.Record.EventCount, len(b.Record.Events), p.events)
	}
	if len(b.Record.Snapshot) == 0 {
		t.Errorf("record.snapshot がない")
	}
	if b.Stage.Title != p.st.Title || len(b.Stage.Islands) != len(p.st.Islands) {
		t.Errorf("stage.title = %q・島 %d（want %q・%d）", b.Stage.Title, len(b.Stage.Islands), p.st.Title, len(p.st.Islands))
	}
	if b.IslandsState == nil || b.Score == nil {
		t.Errorf("islandsState・score がない")
	}
	if b.Result.Stars != p.stars {
		t.Errorf("result.stars = %d（want %d）", b.Result.Stars, p.stars)
	}
	if b.Result.InputLogAvailable {
		t.Errorf("result.inputLogAvailable = true（want false）")
	}
}

// RW-1
func TestHandleStage1(t *testing.T) {
	checkReplayOK(t, play(t, "stage1", "greedy", 1))
}

// RW-2
func TestHandleDemoIzu(t *testing.T) {
	checkReplayOK(t, play(t, "demo-izu", "greedy", 1))
}

// セッションのコマンドは捨てて ignored に数える（simcli replay と同じ）。
func TestHandleIgnoresSessionCommands(t *testing.T) {
	p := play(t, "stage1", "greedy", 1)
	in := encode(t, p.log)
	// 2 件目の後ろ（cmd.game.start の後）に cmd.game.pause と cmd.game.speed を差し込む。
	lines := strings.SplitN(string(in), "\n", 3)
	extra := `{"t":0,"cmd":{"type":"cmd.game.pause","payload":{}}},` + "\n" +
		`{"t":0,"cmd":{"type":"cmd.game.speed","payload":{"speed":2}}},` + "\n"
	in2 := lines[0] + "\n" + extra + lines[1] + "\n" + lines[2]
	b := decodeOK(t, handle([]byte(in2), repoCatalog(t), build))
	if b.Ignored != 2 {
		t.Errorf("ignored = %d（want 2）", b.Ignored)
	}
	if b.Record.TraceHash != p.traceHash {
		t.Errorf("traceHash = %s（want %s）", b.Record.TraceHash, p.traceHash)
	}
}

// RW-3
func TestHandleStageMismatch(t *testing.T) {
	p := play(t, "stage2", "steady", 1)
	l := p.log
	l.StageHash = "sha256:" + strings.Repeat("0", 64)
	b := decodeErr(t, handle(encode(t, l), repoCatalog(t), build), codeStageMismatch)
	if b.StageID == nil || *b.StageID != "stage2" || b.LogBuild == nil || *b.LogBuild != "git:test123" {
		t.Errorf("stageId = %v・logBuild = %v", b.StageID, b.LogBuild)
	}
}

// RW-4
func TestHandleBadInputs(t *testing.T) {
	cat := repoCatalog(t)
	st := mustStage(t, "stage1")
	unknown := replaykit.InputLog{Format: 1, Build: "dev", StageID: "no-such-stage", StageHash: st.Hash, Seed: 1}
	b := decodeErr(t, handle(encode(t, unknown), cat, build), codeUnknownStage)
	if b.StageID == nil || *b.StageID != "no-such-stage" || b.LogBuild == nil {
		t.Errorf("unknown_stage: stageId = %v・logBuild = %v", b.StageID, b.LogBuild)
	}

	valid := string(encode(t, replaykit.InputLog{Format: 1, Build: "dev", StageID: st.ID, StageHash: st.Hash, Seed: 1}))
	for name, in := range map[string]string{
		"空":        "",
		"JSON でない": "not json",
		"配列":       "[]",
		"未知のキー":    strings.Replace(valid, `{"format"`, `{"extra":1,"format"`, 1),
		"後ろの余分":    valid + "{}",
	} {
		b := decodeErr(t, handle([]byte(in), cat, build), codeBadInput)
		if b.StageID != nil || b.LogBuild != nil {
			t.Errorf("%s: 読めない入力ログに stageId・logBuild が付いた", name)
		}
	}

	big := append([]byte(valid), bytes.Repeat([]byte(" "), maxInputBytes+1-len(valid))...)
	if len(big) != maxInputBytes+1 {
		t.Fatalf("len = %d", len(big))
	}
	decodeErr(t, handle(big, cat, build), codeTooLarge)
	// ちょうど 1 MiB は読む（後ろの空白は JSON として許される）。
	if b := handle(big[:maxInputBytes], cat, build); bytes.Contains(b, []byte(`"code":"too_large"`)) {
		t.Errorf("1 MiB ちょうどで too_large になった")
	}
}

// RW-5
func TestHandleNotStarted(t *testing.T) {
	st := mustStage(t, "stage1")
	l := replaykit.InputLog{Format: 1, Build: "dev", StageID: st.ID, StageHash: st.Hash, Seed: 1}
	decodeErr(t, handle(encode(t, l), repoCatalog(t), build), codeNotStarted)
}

// RW-6
func TestHandleDiverged(t *testing.T) {
	cat := repoCatalog(t)
	p := play(t, "stage1", "greedy", 1)
	if len(p.log.Commands) < 2 || p.log.Commands[0].Cmd.Type != sim.CmdGameStart {
		t.Fatalf("入力ログの形が想定と違う（%d 件）", len(p.log.Commands))
	}
	start := p.log.Commands[0]

	// 存在しない船の ferryId。
	bad := []sim.Command{{Type: sim.CmdFerryDepart, FerryID: "no-such-ferry", To: 1}}
	// 航路のない島への出航（船の今の島から航路がない島を探す。なければ存在しない島）。
	cfg, err := stage.SimConfig(p.st)
	if err != nil {
		t.Fatal(err)
	}
	e, err := sim.New(cfg, p.log.Seed, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := e.Snapshot().Ferries[0]
	to := uint64(9999)
	for _, is := range p.st.Islands {
		if is.NodeID == f.IslandID || hasRoute(p.st, f.IslandID, is.NodeID) {
			continue
		}
		to = is.NodeID
		break
	}
	bad = append(bad, sim.Command{Type: sim.CmdFerryDepart, FerryID: f.ID, To: to})

	for _, c := range bad {
		l := p.log
		l.Commands = append([]sim.InputEntry{start, {T: start.T, Cmd: c}}, p.log.Commands[1:]...)
		decodeErr(t, handle(encode(t, l), cat, build), codeDiverged)
	}

	// 時刻が前のコマンドより前のエントリ（ErrReplayOrder）。
	l := p.log
	last := p.log.Commands[len(p.log.Commands)-1]
	if last.T == 0 {
		t.Fatalf("最後のコマンドの時刻が 0")
	}
	l.Commands = append(append([]sim.InputEntry{}, p.log.Commands...), sim.InputEntry{T: last.T - 1, Cmd: last.Cmd})
	decodeErr(t, handle(encode(t, l), cat, build), codeDiverged)
}

func hasRoute(st *stage.Stage, a, b uint64) bool {
	for _, r := range st.Routes {
		if (r.A == a && r.B == b) || (r.A == b && r.B == a) {
			return true
		}
	}
	return false
}

// RW-7
func TestHandleDeterministic(t *testing.T) {
	in := encode(t, play(t, "stage1", "greedy", 1).log)
	a := handle(in, repoCatalog(t), build)
	b := handle(in, repoCatalog(t), build)
	if !bytes.Equal(a, b) {
		t.Errorf("同じ入力で応答が違う（%d バイトと %d バイト）", len(a), len(b))
	}
}

// RW-8
func TestHandleNoCatalog(t *testing.T) {
	st := mustStage(t, "stage1")
	in := encode(t, replaykit.InputLog{Format: 1, Build: "dev", StageID: st.ID, StageHash: st.Hash, Seed: 1})
	for _, input := range [][]byte{in, nil} {
		b := decodeErr(t, handle(input, nil, build), codeInternal)
		if !strings.Contains(b.Message, "ステージ定義を埋め込んでいない") {
			t.Errorf("message = %q", b.Message)
		}
	}
}

// Node での確かめのモード（main_js.go から呼ぶ）。
func TestRunCheck(t *testing.T) {
	cat := repoCatalog(t)
	p := play(t, "stage1", "greedy", 1)
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, encode(t, p.log), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCheck([]string{"--input", good}, cat, build, &stdout, &stderr); code != 0 {
		t.Fatalf("終了コード %d（stderr: %s）", code, stderr.String())
	}
	want := "traceHash=" + p.traceHash + " events="
	if got := stdout.String(); !strings.HasPrefix(got, want) || !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 {
		t.Errorf("stdout = %q（want %q…）", got, want)
	}

	l := p.log
	l.StageHash = "sha256:" + strings.Repeat("0", 64)
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, encode(t, l), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runCheck([]string{"--input", bad}, cat, build, &stdout, &stderr); code != 1 {
		t.Fatalf("終了コード %d（want 1）", code)
	}
	if stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "code=stage_mismatch message=") {
		t.Errorf("stdout = %q・stderr = %q", stdout.String(), stderr.String())
	}

	stderr.Reset()
	if code := runCheck([]string{}, cat, build, &stdout, &stderr); code != 2 {
		t.Errorf("引数なし: 終了コード %d（want 2）", code)
	}
}
