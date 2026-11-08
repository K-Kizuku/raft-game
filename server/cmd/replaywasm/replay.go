package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime/debug"
	"strings"

	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/replaykit"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// build は WASM のビルドの ID（-ldflags "-X main.build=…"）。応答の build に入れる。
var build = "dev"

// maxInputBytes は入力ログの大きさの上限（1 MiB）。超えたら読む前に too_large で断る。
const maxInputBytes = 1 << 20

// 失敗の応答の code（doc.go の表。R4 はこの値で文言を決める）。
const (
	codeTooLarge      = "too_large"
	codeBadInput      = "bad_input"
	codeUnknownStage  = "unknown_stage"
	codeStageMismatch = "stage_mismatch"
	codeDiverged      = "diverged"
	codeNotStarted    = "not_started"
	codeInternal      = "internal"
)

// catalog は埋め込んだステージ定義の一覧と、各ステージの島の表示用の値（ノード ID の順）。
// main で 1 回だけ読み、呼び出しのたびに読まない。作った後は変えない。
type catalog struct {
	stages  []*stage.Stage
	islands [][]stage.IslandDisplay // stages と同じ順
}

// loadCatalog は fsys の islands.json と stages/*.yaml を読んで検証し、各ステージの島の表示用の値を作る。
func loadCatalog(fsys fs.FS) (*catalog, error) {
	stages, err := stage.LoadDir(fsys, "islands.json", "stages")
	if err != nil {
		return nil, fmt.Errorf("ステージ定義を読めない: %w", err)
	}
	display, err := stage.LoadDisplay(fsys, "islands.json")
	if err != nil {
		return nil, fmt.Errorf("ステージ定義を読めない: %w", err)
	}
	c := &catalog{stages: stages, islands: make([][]stage.IslandDisplay, len(stages))}
	for i, st := range stages {
		islands, err := display.ForStage(st)
		if err != nil {
			return nil, err
		}
		c.islands[i] = islands
	}
	return c, nil
}

// lookup は id のステージと島の表示用の値を返す。
func (c *catalog) lookup(id string) (*stage.Stage, []stage.IslandDisplay, bool) {
	for i, st := range c.stages {
		if st.ID == id {
			return st, c.islands[i], true
		}
	}
	return nil, nil, false
}

// ids はステージの id を読み込みの順に返す（unknown_stage の文に使う）。
func (c *catalog) ids() []string {
	out := make([]string, len(c.stages))
	for i, st := range c.stages {
		out[i] = st.ID
	}
	return out
}

// okResponse は成功の応答（doc.go）。キーはフィールドの順に出る。
type okResponse struct {
	OK           bool                   `json:"ok"`
	Build        string                 `json:"build"`
	Ignored      int                    `json:"ignored"`
	Record       json.RawMessage        `json:"record"`
	Stage        protocol.StageView     `json:"stage"`
	IslandsState []protocol.IslandState `json:"islandsState"`
	Score        protocol.Score         `json:"score"`
	Result       protocol.RoundResult   `json:"result"`

	traceHash  string // Node での確かめのモードで出す（JSON に出さない）
	eventCount int
}

// errResponse は失敗の応答（doc.go）。StageID・LogBuild は入力ログを読めたときだけ付ける。
type errResponse struct {
	OK       bool    `json:"ok"`
	Build    string  `json:"build"`
	Code     string  `json:"code"`
	Message  string  `json:"message"`
	StageID  *string `json:"stageId,omitempty"`
	LogBuild *string `json:"logBuild,omitempty"`
}

// failure は再生の失敗。log は入力ログを読めたときだけ nil でない。
type failure struct {
	code    string
	message string
	log     *replaykit.InputLog
}

func (f *failure) response(build string) errResponse {
	r := errResponse{Build: build, Code: f.code, Message: f.message}
	if f.log != nil {
		stageID, logBuild := f.log.StageID, f.log.Build
		r.StageID = &stageID
		r.LogBuild = &logBuild
	}
	return r
}

// handle は入力ログ（ファイルの中身そのまま）を再生し、応答の JSON を返す（raftReplay の本体）。
// cat が nil（ステージ定義を埋め込んでいない・読めない）なら、呼ぶたびに internal。panic は回復して internal にする。
// 同じ入力・同じ cat・同じ build なら、同じバイト列を返す。
func handle(input []byte, cat *catalog, build string) []byte {
	resp, fail := replay(input, cat, build)
	if fail != nil {
		return encodeFailure(fail, build)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return encodeFailure(&failure{code: codeInternal, message: "応答を JSON にできない: " + err.Error()}, build)
	}
	return out
}

// encodeFailure は失敗の応答の JSON を返す。
func encodeFailure(f *failure, build string) []byte {
	out, err := json.Marshal(f.response(build))
	if err != nil { // 文字列とポインタだけなので起きない
		return []byte(`{"ok":false,"build":"","code":"internal","message":"応答を JSON にできない"}`)
	}
	return out
}

// replay は handle の本体（再生の手順は doc.go）。panic を回復して internal の失敗にする。
// 入力ログの中身を標準出力・標準エラーに出さない。panic のときだけ、panic の値とスタックを標準エラーに出す。
func replay(input []byte, cat *catalog, build string) (resp okResponse, fail *failure) {
	var parsed *replaykit.InputLog
	defer func() {
		if r := recover(); r != nil {
			_, _ = fmt.Fprintf(os.Stderr, "raft-replay: panic: %v\n%s", r, debug.Stack())
			resp, fail = okResponse{}, &failure{code: codeInternal, message: fmt.Sprintf("再生の途中で内部の誤りが起きた（panic: %v）", r), log: parsed}
		}
	}()

	if cat == nil {
		return resp, &failure{code: codeInternal, message: "ステージ定義を埋め込んでいない（または読めない）。scripts/build-replay-wasm.sh でビルドする"}
	}
	if len(input) > maxInputBytes {
		return resp, &failure{code: codeTooLarge, message: fmt.Sprintf("入力ログが大きすぎる（%d バイト。上限は %d バイト）", len(input), maxInputBytes)}
	}
	l, err := replaykit.DecodeInputLog(bytes.NewReader(input))
	if err != nil {
		return resp, &failure{code: codeBadInput, message: err.Error()}
	}
	parsed = &l

	st, islands, ok := cat.lookup(l.StageID)
	if !ok {
		return resp, &failure{code: codeUnknownStage, log: parsed,
			message: fmt.Sprintf("ステージ %s がない（あるもの: %s）", l.StageID, strings.Join(cat.ids(), ", "))}
	}
	cfg, err := stage.SimConfig(st)
	if err != nil {
		return resp, &failure{code: codeInternal, log: parsed, message: fmt.Sprintf("ステージ %s: %v", st.ID, err)}
	}
	// 初めのスナップショット（simcli replay・record と同じ時点。phase は intro、lastSeq は 0）。
	first, err := sim.New(cfg, l.Seed, sim.Options{})
	if err != nil {
		return resp, &failure{code: codeInternal, log: parsed, message: fmt.Sprintf("ステージ %s: %v", st.ID, err)}
	}
	snap := first.Snapshot()

	e, err := sim.Replay(cfg, st.Hash, sim.ReplayLog{Format: l.Format, StageHash: l.StageHash, Seed: l.Seed, Entries: l.Commands}, sim.Options{})
	switch {
	case errors.Is(err, sim.ErrReplayStage):
		return resp, &failure{code: codeStageMismatch, log: parsed,
			message: fmt.Sprintf("入力ログのステージ %s のハッシュが、この版の定義と違う（ログ: %s、この版: %s）", l.StageID, l.StageHash, st.Hash)}
	case errors.Is(err, sim.ErrReplayDiverged), errors.Is(err, sim.ErrReplayOrder):
		return resp, &failure{code: codeDiverged, log: parsed, message: err.Error()}
	case err != nil:
		return resp, &failure{code: codeInternal, log: parsed, message: err.Error()}
	}
	switch e.Phase() {
	case sim.PhaseIntro:
		return resp, &failure{code: codeNotStarted, log: parsed, message: "入力ログに cmd.game.start がない（ラウンドが始まらない）"}
	case sim.PhaseSailing:
		e.RunUntil(sim.SimTime(st.DurationMs)) // simcli replay と同じく、ラウンドの終わりまで進める
	}

	events := e.DrainEvents()
	h := e.TraceHash()
	traceHash := "sha256:" + hex.EncodeToString(h[:])
	record, err := replaykit.EncodeRecord(replaykit.NewRecordHeader(build, st, l.Seed, "", len(events), traceHash), snap, events)
	if err != nil {
		return resp, &failure{code: codeInternal, log: parsed, message: "記録を作れない: " + err.Error()}
	}
	r, _ := e.Result() // 採点しないステージではゼロ値のまま使う
	story := game.WriteStory(e.StoryFacts(), replaykit.StoryNames(st))
	return okResponse{
		OK:           true,
		Build:        build,
		Ignored:      l.Ignored,
		Record:       record,
		Stage:        replaykit.StageView(st, islands),
		IslandsState: simconv.IslandStatesFromSim(snap.Islands),
		Score:        simconv.ScoreFromSim(snap.Score, int(st.Scoring.TargetSuccesses)),
		Result:       simconv.RoundResultFromSim(r, e.TechStats(), story, false),
		traceHash:    traceHash,
		eventCount:   len(events),
	}, nil
}

// runCheck は Node での確かめのモード（`node wasm_exec_node.js raft-replay.wasm --input FILE`）。
// FILE を読んで再生し、成功なら "traceHash=<traceHash> events=<数>" を stdout に 1 行出して 0、
// 失敗なら "code=<code> message=<message>" を stderr に出して 1。引数の誤りは 2。
func runCheck(args []string, cat *catalog, build string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("raft-replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", "", "入力ログのファイル（必須）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *input == "" || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "使い方: raft-replay --input FILE")
		return 2
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "code=%s message=入力ログを読めない: %v\n", codeInternal, err)
		return 1
	}
	resp, fail := replay(data, cat, build)
	if fail != nil {
		_, _ = fmt.Fprintf(stderr, "code=%s message=%s\n", fail.code, fail.message)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "traceHash=%s events=%d\n", resp.traceHash, resp.eventCount)
	return 0
}
