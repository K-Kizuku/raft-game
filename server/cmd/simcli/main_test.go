package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/replaykit"
)

// テスト用のデータ（testdata/）。tiny.yaml の durationMs と合わせる。
const (
	testData       = "testdata"
	testStage      = "tiny"
	testDurationMs = 60000
)

// cliResult は run を 1 回呼んだ結果。
type cliResult struct {
	code   int
	stdout string
	stderr string
}

// runCLI は run を直接呼ぶ。stdin が nil なら空。
func runCLI(t *testing.T, stdin io.Reader, args ...string) cliResult {
	t.Helper()
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	var stdout, stderr bytes.Buffer
	code := run(args, stdin, &stdout, &stderr)
	return cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// wantCode は終了コードを確かめる。違えば標準エラーを出して止める。
func (r cliResult) wantCode(t *testing.T, want int) {
	t.Helper()
	if r.code != want {
		t.Fatalf("code = %d, want %d\nstdout:\n%s\nstderr:\n%s", r.code, want, r.stdout, r.stderr)
	}
}

// parseLines は結果の行（JSON Lines）を読む。
func parseLines(t *testing.T, stdout string) []seedResult {
	t.Helper()
	var out []seedResult
	for line := range strings.SplitSeq(strings.TrimSuffix(stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		var r seedResult
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("結果の行を読めない: %v\n%s", err, line)
		}
		out = append(out, r)
	}
	return out
}

// lastLine は s の最後の空でない行。
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

// runWithInputs は run --inputs-dir <TempDir>/in を呼び、ディレクトリと結果の行を返す。
func runWithInputs(t *testing.T, botName, seeds string) (string, []seedResult) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "in")
	r := runCLI(t, nil, "run", "--data", testData, "--stage", testStage, "--bot", botName, "--seeds", seeds, "--inputs-dir", dir)
	r.wantCode(t, exitOK)
	return dir, parseLines(t, r.stdout)
}

// baseInputLog は random・シード 1 の入力ログ（run --inputs-dir で書いたもの）と、run の結果の行を返す。
func baseInputLog(t *testing.T) (replaykit.InputLog, seedResult) {
	t.Helper()
	dir, lines := runWithInputs(t, "random", "1")
	return readLogFile(t, inputLogPath(dir, testStage, "random", 1)), lines[0]
}

// readLogFile は入力ログのファイルを replaykit.DecodeInputLog で読む。
func readLogFile(t *testing.T, path string) replaykit.InputLog {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	l, err := replaykit.DecodeInputLog(f)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return l
}

// writeLogFile は l を t.TempDir() のファイルに書き、パスを返す。
func writeLogFile(t *testing.T, l replaykit.InputLog) string {
	t.Helper()
	data, err := replaykit.EncodeInputLog(l)
	if err != nil {
		t.Fatal(err)
	}
	return writeRawFile(t, data)
}

// writeRawFile は data を t.TempDir() のファイルに書き、パスを返す。
func writeRawFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// U-1: 使い方の誤りは 2。
func TestUsageErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		args   []string
		stderr []string // 標準エラーに含まれる文
	}{
		{"引数なし", nil, []string{"サブコマンドがない"}},
		{"未知のサブコマンド", []string{"bogus"}, []string{"未知のサブコマンド", "bogus"}},
		{"run に --data がない", []string{"run", "--stage", testStage, "--seeds", "1"}, []string{"必須のフラグがない: --data\n"}},
		{"run の必須のフラグを全部並べる", []string{"run"}, []string{"必須のフラグがない: --data --stage --seeds"}},
		{"run --seed", []string{"run", "--data", testData, "--stage", testStage, "--seeds", "1", "--seed", "1"}, []string{"-seed"}},
		{"record --seeds", []string{"record", "--data", testData, "--stage", testStage, "--seed", "1", "--seeds", "1"}, []string{"-seeds"}},
		{"余分な引数", []string{"run", "--data", testData, "--stage", testStage, "--seeds", "1", "extra"}, []string{"余分な引数: extra"}},
		{"未知のボット", []string{"run", "--data", testData, "--stage", testStage, "--seeds", "1", "--bot", "nobody"}, []string{"未知のボット nobody", "greedy", "random"}},
		{"record の未知のボット", []string{"record", "--data", testData, "--stage", testStage, "--seed", "1", "--bot", "nobody"}, []string{"未知のボット nobody"}},
		{"--expect の形", []string{"replay", "--data", testData, "--input", "-", "--expect", "sha256:XYZ"}, []string{"--expect"}},
		{"--seed -1", []string{"record", "--data", testData, "--stage", testStage, "--seed", "-1"}, []string{"--seed"}},
		{"--seed 0x10", []string{"record", "--data", testData, "--stage", testStage, "--seed", "0x10"}, []string{"--seed"}},
		{"--seeds の形", []string{"run", "--data", testData, "--stage", testStage, "--seeds", "3..1"}, []string{"--seeds"}},
		{"replay の必須のフラグ", []string{"replay"}, []string{"必須のフラグがない: --data --input"}},
		{"record の必須のフラグ", []string{"record"}, []string{"必須のフラグがない: --data --stage --seed"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := runCLI(t, nil, c.args...)
			r.wantCode(t, exitUsage)
			if r.stdout != "" {
				t.Errorf("stdout = %q, want empty", r.stdout)
			}
			for _, s := range c.stderr {
				if !strings.Contains(r.stderr, s) {
					t.Errorf("stderr に %q がない:\n%s", s, r.stderr)
				}
			}
			if !strings.HasPrefix(r.stderr, "simcli") {
				t.Errorf("stderr が simcli で始まらない:\n%s", r.stderr)
			}
		})
	}
}

// U-2: 使い方の表示は 0。
func TestUsageHelp(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}, {"run", "-h"}, {"replay", "-h"}, {"record", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			r := runCLI(t, nil, args...)
			r.wantCode(t, exitOK)
			if r.stdout != "" {
				t.Errorf("stdout = %q, want empty", r.stdout)
			}
			if !strings.Contains(r.stderr, "使い方") {
				t.Errorf("stderr に使い方がない:\n%s", r.stderr)
			}
		})
	}
}

// U-3: ステージを読めない・ステージがないときは 1。
func TestStageErrors(t *testing.T) {
	t.Parallel()
	t.Run("--data がない", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "nope")
		r := runCLI(t, nil, "run", "--data", missing, "--stage", testStage, "--seeds", "1")
		r.wantCode(t, exitFail)
		if r.stdout != "" {
			t.Errorf("stdout = %q, want empty", r.stdout)
		}
		if !strings.Contains(r.stderr, "simcli: stage: islands.json") {
			t.Errorf("stderr に stage のエラーがない:\n%s", r.stderr)
		}
	})
	t.Run("--stage nope", func(t *testing.T) {
		t.Parallel()
		r := runCLI(t, nil, "run", "--data", testData, "--stage", "nope", "--seeds", "1")
		r.wantCode(t, exitFail)
		if want := "simcli: ステージ nope がない（あるもの: " + testStage + "）"; !strings.Contains(r.stderr, want) {
			t.Errorf("stderr に %q がない:\n%s", want, r.stderr)
		}
	})
	t.Run("ステージが空", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		src, err := os.ReadFile(filepath.Join(testData, "islands.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "islands.json"), src, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(dir, "stages"), 0o755); err != nil {
			t.Fatal(err)
		}
		r := runCLI(t, nil, "record", "--data", dir, "--stage", testStage, "--seed", "1")
		r.wantCode(t, exitFail)
		if want := "（あるもの: なし）"; !strings.Contains(r.stderr, want) {
			t.Errorf("stderr に %q がない:\n%s", want, r.stderr)
		}
	})
}
