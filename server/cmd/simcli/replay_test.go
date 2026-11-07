package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/replaykit"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// replayFile は replay --data testdata --input path を呼ぶ。
func replayFile(t *testing.T, path string, extra ...string) cliResult {
	t.Helper()
	return runCLI(t, nil, append([]string{"replay", "--data", testData, "--input", path}, extra...)...)
}

// oneResult は結果の行がちょうど 1 行であることを確かめて返す。
func oneResult(t *testing.T, stdout string) seedResult {
	t.Helper()
	lines := parseLines(t, stdout)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1:\n%s", len(lines), stdout)
	}
	return lines[0]
}

// withCommands は l の Commands を cmds にした複製。
func withCommands(l replaykit.InputLog, cmds []sim.InputEntry) replaykit.InputLog {
	l.Commands = cmds
	l.Pos = nil
	return l
}

// RP-1: --expect が違えば 1。
func TestReplayExpectMismatch(t *testing.T) {
	t.Parallel()
	l, run := baseInputLog(t)
	path := writeLogFile(t, l)
	wrong := "sha256:" + strings.Repeat("0", 64)
	r := replayFile(t, path, "--expect", wrong)
	r.wantCode(t, exitFail)
	if got := oneResult(t, r.stdout); !got.OK || got.TraceHash != run.TraceHash {
		t.Errorf("line = %+v", got)
	}
	if !strings.Contains(r.stderr, "期待: "+wrong) || !strings.Contains(r.stderr, "実際: "+run.TraceHash) {
		t.Errorf("stderr = %s", r.stderr)
	}
}

// RP-2: stageHash が違えば 1。
func TestReplayStageHashMismatch(t *testing.T) {
	t.Parallel()
	l, _ := baseInputLog(t)
	l.StageHash = "sha256:" + strings.Repeat("1", 64)
	r := replayFile(t, writeLogFile(t, l))
	r.wantCode(t, exitFail)
	if !strings.Contains(r.stderr, "ハッシュが今の定義と違う") || r.stdout != "" {
		t.Errorf("stdout = %q, stderr = %s", r.stdout, r.stderr)
	}
}

// RP-3: stageId が存在しなければ 1。
func TestReplayUnknownStage(t *testing.T) {
	t.Parallel()
	l, _ := baseInputLog(t)
	l.StageID = "nope"
	r := replayFile(t, writeLogFile(t, l))
	r.wantCode(t, exitFail)
	if !strings.Contains(r.stderr, "ステージ nope がない") {
		t.Errorf("stderr = %s", r.stderr)
	}
}

// RP-4: 存在しない手紙を積むと 1。どのコマンドで、どのコードかを書く。
func TestReplayApplyFails(t *testing.T) {
	t.Parallel()
	l, _ := baseInputLog(t)
	cmds := slices.Insert(slices.Clone(l.Commands), 1,
		sim.InputEntry{T: 0, Cmd: sim.Command{Type: sim.CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{999999}}})
	r := replayFile(t, writeLogFile(t, withCommands(l, cmds)))
	r.wantCode(t, exitFail)
	got := oneResult(t, r.stdout)
	if got.OK || !strings.Contains(got.Error, "commands[1]") || !strings.Contains(got.Error, sim.CodeLetterNotHere) {
		t.Errorf("line = %+v", got)
	}
	if !strings.Contains(r.stderr, "commands[1]") || !strings.Contains(r.stderr, sim.CodeLetterNotHere) {
		t.Errorf("stderr = %s", r.stderr)
	}
}

// RP-5: cmd.game.start より前の t: 1000 の cmd.ferry.depart は phase_invalid で 1。
func TestReplayBeforeStart(t *testing.T) {
	t.Parallel()
	l, _ := baseInputLog(t)
	cmds := []sim.InputEntry{
		{T: 1000, Cmd: sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2}},
		{T: 1000, Cmd: sim.Command{Type: sim.CmdGameStart}},
	}
	r := replayFile(t, writeLogFile(t, withCommands(l, cmds)))
	r.wantCode(t, exitFail)
	if !strings.Contains(r.stderr, "commands[0]") || !strings.Contains(r.stderr, sim.CodePhaseInvalid) {
		t.Errorf("stderr = %s", r.stderr)
	}
}

// RP-6: t が減る・t が durationMs より後なら 1。
func TestReplayBadTimes(t *testing.T) {
	t.Parallel()
	l, _ := baseInputLog(t)
	n := len(l.Commands)
	if l.Commands[n-1].T == 0 {
		t.Fatal("最後のコマンドの t が 0")
	}
	cases := []struct {
		name  string
		entry sim.InputEntry
		want  string
	}{
		{"t が減る", sim.InputEntry{T: 0, Cmd: sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2}}, "前のコマンドより前"},
		{"t が durationMs より後", sim.InputEntry{T: testDurationMs + 1, Cmd: sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2}}, "ラウンドの終わり 60000 より後"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cmds := append(slices.Clone(l.Commands), c.entry)
			r := replayFile(t, writeLogFile(t, withCommands(l, cmds)))
			r.wantCode(t, exitFail)
			want := "commands[" + strconv.Itoa(n) + "]: t "
			if !strings.Contains(r.stderr, want) || !strings.Contains(r.stderr, c.want) {
				t.Errorf("stderr に %q・%q がない: %s", want, c.want, r.stderr)
			}
		})
	}
}

// RP-7: cmd.game.pause・cmd.game.speed を差し込んでも traceHash は同じ。
func TestReplayIgnoresSessionCommands(t *testing.T) {
	t.Parallel()
	l, run := baseInputLog(t)
	data, err := replaykit.EncodeInputLog(l)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(data), "\n")
	// lines[0] はヘッダー、lines[1] は最初のコマンド。2 件目の前に差し込む。
	extra := []string{
		`{"t":1,"cmd":{"type":"cmd.game.pause","payload":{"paused":true}}},` + "\n",
		`{"t":2,"cmd":{"type":"cmd.game.speed","payload":{"speed":4000}}},` + "\n",
	}
	lines = slices.Insert(lines, 2, extra...)
	r := replayFile(t, writeRawFile(t, []byte(strings.Join(lines, ""))), "--expect", run.TraceHash)
	r.wantCode(t, exitOK)
	if got := oneResult(t, r.stdout); got.TraceHash != run.TraceHash || *got.Commands != *run.Commands {
		t.Errorf("line = %+v, run = %+v", got, run)
	}
	if !strings.Contains(r.stderr, "simcli replay: 無視したコマンド 2 件") {
		t.Errorf("stderr = %s", r.stderr)
	}
}

// RP-8: commands が空なら intro のまま 0。
func TestReplayEmpty(t *testing.T) {
	t.Parallel()
	l, _ := baseInputLog(t)
	r := replayFile(t, writeLogFile(t, withCommands(l, nil)))
	r.wantCode(t, exitOK)
	got := oneResult(t, r.stdout)
	const empty = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if !got.OK || got.Phase != sim.PhaseIntro || *got.T != 0 || *got.Events != 0 || *got.Commands != 0 || got.TraceHash != empty {
		t.Errorf("line = %+v", got)
	}
	want := `{"seed":1,"ok":true,"phase":"intro","t":0,"events":0,"commands":0,"traceHash":"` + empty + `"}` + "\n"
	if r.stdout != want {
		t.Errorf("stdout = %s, want %s", r.stdout, want)
	}
	if strings.Contains(r.stderr, "無視した") {
		t.Errorf("stderr = %s", r.stderr)
	}
}

// RP-9: --input - なら stdin から読む。
func TestReplayStdin(t *testing.T) {
	t.Parallel()
	l, run := baseInputLog(t)
	data, err := replaykit.EncodeInputLog(l)
	if err != nil {
		t.Fatal(err)
	}
	r := runCLI(t, bytes.NewReader(data), "replay", "--data", testData, "--input", "-")
	r.wantCode(t, exitOK)
	if got := oneResult(t, r.stdout); got.TraceHash != run.TraceHash {
		t.Errorf("line = %+v, run = %+v", got, run)
	}
}

// RP-10: 最後の数件を消しても、ラウンドの終わりまで進めて 0。
func TestReplayTruncated(t *testing.T) {
	t.Parallel()
	l, _ := baseInputLog(t)
	if len(l.Commands) < 4 {
		t.Fatalf("commands = %d", len(l.Commands))
	}
	r := replayFile(t, writeLogFile(t, withCommands(l, l.Commands[:len(l.Commands)-3])))
	r.wantCode(t, exitOK)
	got := oneResult(t, r.stdout)
	if !got.OK || got.Phase != sim.PhaseEnded || *got.T != testDurationMs || *got.Commands != len(l.Commands)-3 {
		t.Errorf("line = %+v", got)
	}
}

// 入力ログの誤りは 1（場所を書く）。ファイルがないときも 1。
func TestReplayBadInput(t *testing.T) {
	t.Parallel()
	path := writeRawFile(t, []byte(`{"format":1,"stageId":"tiny","stageHash":"h","seed":1,"commands":[{"t":-1}]}`))
	r := replayFile(t, path)
	r.wantCode(t, exitFail)
	if !strings.Contains(r.stderr, "commands[0].t") || r.stdout != "" {
		t.Errorf("stdout = %q, stderr = %s", r.stdout, r.stderr)
	}

	missing := path + ".nope"
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("ファイルがある")
	}
	r = replayFile(t, missing)
	r.wantCode(t, exitFail)
}

// replayTrace は replay --input path --trace <TempDir>/t.json を呼び、結果とトレースのパスを返す。
func replayTrace(t *testing.T, path string, extra ...string) (cliResult, string) {
	t.Helper()
	trace := filepath.Join(t.TempDir(), "t.json")
	return replayFile(t, path, append([]string{"--trace", trace}, extra...)...), trace
}

// wantNoFile は path にファイルがないことを確かめる。
func wantNoFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s: ファイルがある（err = %v）", path, err)
	}
}

// TR-1・TR-2: record --inputs-dir の入力ログから replay --trace で作った記録は、record の記録と bot だけが違う。
func TestReplayTraceMatchesRecord(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "in")
	rr, rec := recordOK(t, "--inputs-dir", dir)
	input := inputLogPath(dir, testStage, "greedy", 1)
	r, trace := replayTrace(t, input)
	r.wantCode(t, exitOK)
	line := oneResult(t, r.stdout)
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	// TR-1: ヘッダーの bot だけが違い、ほかはバイト列で一致する。
	want := strings.Replace(rr.stdout, `"bot":"greedy",`, `"bot":"",`, 1)
	if want == rr.stdout {
		t.Fatal(`record の記録に "bot":"greedy" がない`)
	}
	if string(data) != want {
		t.Errorf("trace:\n%s\nwant:\n%s", data, want)
	}
	var got parsedRecord
	if err := json.Unmarshal(data, &got); err != nil { // TR-2: 1 つの JSON の値として読める
		t.Fatal(err)
	}
	if got.Bot != "" || got.StageID != rec.StageID || got.StageHash != rec.StageHash || got.Seed != rec.Seed ||
		got.EventCount != rec.EventCount || got.TraceHash != rec.TraceHash || got.Build != rec.Build ||
		got.DurationMs != rec.DurationMs {
		t.Errorf("header = %+v, record = %+v", got, rec)
	}
	if got.TraceHash != line.TraceHash || got.EventCount != *line.Events || got.EventCount != len(got.Events) {
		t.Errorf("header = %+v, line = %+v", got, line)
	}
	// TR-2: events の各行（末尾の , を除く）に改行を付けて連結した SHA-256 が traceHash。
	lines := strings.Split(string(data), "\n")
	start := slices.Index(lines, `"events":[`) + 1
	end := slices.Index(lines, "]}")
	if start <= 0 || end < start {
		t.Fatalf("events の範囲が見つからない: %d %d", start, end)
	}
	h := sha256.New()
	for _, l := range lines[start:end] {
		h.Write([]byte(strings.TrimSuffix(l, ",")))
		h.Write([]byte{'\n'})
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	if formatHash(sum) != got.TraceHash {
		t.Errorf("hash = %s, traceHash = %s", formatHash(sum), got.TraceHash)
	}
	if want := "simcli replay: トレースを " + trace + " に書いた（events=" + strconv.Itoa(got.EventCount) + "）"; !strings.Contains(r.stderr, want) {
		t.Errorf("stderr = %s, want %s", r.stderr, want)
	}
	// --trace の有無で結果の行は変わらない。
	if plain := replayFile(t, input); plain.stdout != r.stdout {
		t.Errorf("stdout: --trace あり %s、なし %s", r.stdout, plain.stdout)
	}
}

// 既にあるファイルは上書きする。
func TestReplayTraceOverwrites(t *testing.T) {
	t.Parallel()
	l, _ := baseInputLog(t)
	trace := filepath.Join(t.TempDir(), "t.json")
	if err := os.WriteFile(trace, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := replayFile(t, writeLogFile(t, l), "--trace", trace)
	r.wantCode(t, exitOK)
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Errorf("trace = %s", data)
	}
}

// TR-3: --trace - は使い方の誤り。--trace に空文字列は指定なしと同じ。
func TestReplayTraceDash(t *testing.T) {
	t.Parallel()
	l, run := baseInputLog(t)
	path := writeLogFile(t, l)
	r := replayFile(t, path, "--trace", "-")
	r.wantCode(t, exitUsage)
	if r.stdout != "" || !strings.Contains(r.stderr, "--trace") {
		t.Errorf("stdout = %q, stderr = %s", r.stdout, r.stderr)
	}
	r = replayFile(t, path, "--trace", "")
	r.wantCode(t, exitOK)
	if got := oneResult(t, r.stdout); got.TraceHash != run.TraceHash || strings.Contains(r.stderr, "トレースを") {
		t.Errorf("line = %+v, stderr = %s", got, r.stderr)
	}
}

// TR-4: --expect が違えば 1 で、トレースを書かない。
func TestReplayTraceExpectMismatch(t *testing.T) {
	t.Parallel()
	l, _ := baseInputLog(t)
	r, trace := replayTrace(t, writeLogFile(t, l), "--expect", "sha256:"+strings.Repeat("0", 64))
	r.wantCode(t, exitFail)
	wantNoFile(t, trace)
}

// TR-5: コマンドが失敗すれば 1 で、トレースを書かない（既にあるファイルも変えない）。
func TestReplayTraceApplyFails(t *testing.T) {
	t.Parallel()
	l, _ := baseInputLog(t)
	cmds := slices.Insert(slices.Clone(l.Commands), 1,
		sim.InputEntry{T: 0, Cmd: sim.Command{Type: sim.CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{999999}}})
	path := writeLogFile(t, withCommands(l, cmds))
	r, trace := replayTrace(t, path)
	r.wantCode(t, exitFail)
	wantNoFile(t, trace)

	if err := os.WriteFile(trace, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = replayFile(t, path, "--trace", trace)
	r.wantCode(t, exitFail)
	if data, err := os.ReadFile(trace); err != nil || string(data) != "old" {
		t.Errorf("trace = %q, err = %v", data, err)
	}
}

// TR-6: 書けない（ディレクトリがない）なら 1。結果の行は出ている。
func TestReplayTraceUnwritable(t *testing.T) {
	t.Parallel()
	l, run := baseInputLog(t)
	trace := filepath.Join(t.TempDir(), "nope", "t.json")
	r := replayFile(t, writeLogFile(t, l), "--trace", trace)
	r.wantCode(t, exitFail)
	if got := oneResult(t, r.stdout); !got.OK || got.TraceHash != run.TraceHash {
		t.Errorf("line = %+v", got)
	}
	if !strings.Contains(r.stderr, "simcli: トレースを書けない: ") {
		t.Errorf("stderr = %s", r.stderr)
	}
	wantNoFile(t, trace)
}

// TR-7: cmd.game.start の無い入力ログでも、intro のままの記録を書く。
func TestReplayTraceEmpty(t *testing.T) {
	t.Parallel()
	l, _ := baseInputLog(t)
	r, trace := replayTrace(t, writeLogFile(t, withCommands(l, nil)))
	r.wantCode(t, exitOK)
	line := oneResult(t, r.stdout)
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	var got parsedRecord
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != *line.Events || got.EventCount != *line.Events || got.TraceHash != line.TraceHash ||
		got.Bot != "" || got.Snapshot.Phase != "intro" || got.Seed != l.Seed {
		t.Errorf("record = %+v, line = %+v", got, line)
	}
	if !strings.HasSuffix(string(data), "\n"+`"events":[`+"\n]}\n") {
		t.Errorf("trace = %s", data)
	}
}
