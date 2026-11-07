package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/bot"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

var traceHashRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// checkRunLines は run の結果の行が、wantSeeds の順に全部成功していることを確かめる。
func checkRunLines(t *testing.T, lines []seedResult, wantSeeds []uint64) {
	t.Helper()
	if len(lines) != len(wantSeeds) {
		t.Fatalf("lines = %d, want %d", len(lines), len(wantSeeds))
	}
	for i, l := range lines {
		if l.Seed != wantSeeds[i] {
			t.Errorf("line %d: seed = %d, want %d", i, l.Seed, wantSeeds[i])
		}
		if !l.OK || l.Error != "" {
			t.Errorf("seed %d: ok = %v, error = %q", l.Seed, l.OK, l.Error)
		}
		if l.Phase != sim.PhaseEnded {
			t.Errorf("seed %d: phase = %q", l.Seed, l.Phase)
		}
		if l.T == nil || *l.T != testDurationMs {
			t.Errorf("seed %d: t = %v, want %d", l.Seed, l.T, testDurationMs)
		}
		if l.Events == nil || *l.Events <= 0 {
			t.Errorf("seed %d: events = %v", l.Seed, l.Events)
		}
		if l.Commands == nil || *l.Commands < 1 {
			t.Errorf("seed %d: commands = %v", l.Seed, l.Commands)
		}
		if !traceHashRE.MatchString(l.TraceHash) {
			t.Errorf("seed %d: traceHash = %q", l.Seed, l.TraceHash)
		}
		if l.Stars == nil || l.Score == nil {
			t.Errorf("seed %d: stars = %v, score = %v", l.Seed, l.Stars, l.Score)
		}
	}
}

// RS-1: run の結果の行の stars・score は Engine.Result() の値。同じ引数なら同じバイト列。
func TestRunStarsScore(t *testing.T) {
	t.Parallel()
	st, err := loadStage(testData, testStage)
	if err != nil {
		t.Fatal(err)
	}
	for _, botName := range []string{"random", "greedy"} {
		t.Run(botName, func(t *testing.T) {
			t.Parallel()
			args := []string{"run", "--data", testData, "--stage", testStage, "--bot", botName, "--seeds", "1..3"}
			a := runCLI(t, nil, args...)
			b := runCLI(t, nil, args...)
			a.wantCode(t, exitOK)
			b.wantCode(t, exitOK)
			if a.stdout != b.stdout {
				t.Errorf("stdout differs:\n%s\n%s", a.stdout, b.stdout)
			}
			for _, l := range parseLines(t, a.stdout) {
				e, err := newEngine(st, l.Seed, true)
				if err != nil {
					t.Fatal(err)
				}
				bt, err := bot.New(botName, l.Seed)
				if err != nil {
					t.Fatal(err)
				}
				if err := bot.Play(e, bt); err != nil {
					t.Fatal(err)
				}
				r, ok := e.Result()
				if !ok {
					t.Fatalf("seed %d: Result() の ok が false", l.Seed)
				}
				if l.Stars == nil || *l.Stars != r.Stars || l.Score == nil || *l.Score != r.Score.Total {
					t.Errorf("seed %d: stars = %v, score = %v, want %d %d", l.Seed, l.Stars, l.Score, r.Stars, r.Score.Total)
				}
				want := fmt.Sprintf(`"traceHash":%q,"stars":%d,"score":%d}`, l.TraceHash, r.Stars, r.Score.Total)
				if !strings.Contains(a.stdout, want) {
					t.Errorf("seed %d: 行に %s がない:\n%s", l.Seed, want, a.stdout)
				}
			}
		})
	}
}

// R-1・R-2: 書いた順に 1 シード 1 行。全部成功なら 0。
func TestRunOK(t *testing.T) {
	t.Parallel()
	for _, botName := range []string{"random", "greedy"} {
		t.Run(botName, func(t *testing.T) {
			t.Parallel()
			r := runCLI(t, nil, "run", "--data", testData, "--stage", testStage, "--bot", botName, "--seeds", "1..3")
			r.wantCode(t, exitOK)
			checkRunLines(t, parseLines(t, r.stdout), []uint64{1, 2, 3})
			if got := lastLine(r.stderr); !strings.HasSuffix(got, "ok=3 failed=0") ||
				got != "simcli run: "+testStage+" bot="+botName+" seeds=3 ok=3 failed=0" {
				t.Errorf("stderr の最後の行 = %q", got)
			}
		})
	}
}

// 書いた順（並べ替えない）。--bot の既定は greedy。
func TestRunOrderAndDefaultBot(t *testing.T) {
	t.Parallel()
	r := runCLI(t, nil, "run", "--data", testData, "--stage", testStage, "--seeds", "3,1")
	r.wantCode(t, exitOK)
	checkRunLines(t, parseLines(t, r.stdout), []uint64{3, 1})
	if !strings.Contains(r.stderr, "bot=greedy") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

// R-3: 同じ引数なら同じバイト列。
func TestRunSameBytes(t *testing.T) {
	t.Parallel()
	args := []string{"run", "--data", testData, "--stage", testStage, "--bot", "random", "--seeds", "4..5"}
	a := runCLI(t, nil, args...)
	b := runCLI(t, nil, args...)
	a.wantCode(t, exitOK)
	b.wantCode(t, exitOK)
	if a.stdout != b.stdout {
		t.Errorf("stdout differs:\n%s\n%s", a.stdout, b.stdout)
	}
}

// R-4: --invariants は結果を変えない。
func TestRunInvariantsFlag(t *testing.T) {
	t.Parallel()
	base := []string{"run", "--data", testData, "--stage", testStage, "--bot", "greedy", "--seeds", "1..2"}
	on := runCLI(t, nil, append(slices.Clone(base), "--invariants=true")...)
	off := runCLI(t, nil, append(slices.Clone(base), "--invariants=false")...)
	on.wantCode(t, exitOK)
	off.wantCode(t, exitOK)
	a, b := parseLines(t, on.stdout), parseLines(t, off.stdout)
	if len(a) != len(b) {
		t.Fatalf("lines: %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i].TraceHash != b[i].TraceHash || *a[i].Events != *b[i].Events || *a[i].Commands != *b[i].Commands {
			t.Errorf("seed %d: %+v != %+v", a[i].Seed, a[i], b[i])
		}
	}
}

// R-5: --determinism は 0。
func TestRunDeterminism(t *testing.T) {
	t.Parallel()
	r := runCLI(t, nil, "run", "--data", testData, "--stage", testStage, "--bot", "random", "--seeds", "1..2", "--determinism")
	r.wantCode(t, exitOK)
	checkRunLines(t, parseLines(t, r.stdout), []uint64{1, 2})
}

// R-6: --inputs-dir に、シードごとの入力ログが書かれる。
func TestRunInputsDir(t *testing.T) {
	t.Parallel()
	dir, lines := runWithInputs(t, "random", "1..3")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{testStage + "-random-1.json", testStage + "-random-2.json", testStage + "-random-3.json"}
	if !slices.Equal(names, want) {
		t.Fatalf("files = %v, want %v", names, want)
	}
	st, err := loadStage(testData, testStage)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range want {
		l := readLogFile(t, filepath.Join(dir, name))
		if l.Format != 1 || l.StageID != testStage || l.StageHash != st.Hash || l.Seed != uint64(i+1) {
			t.Errorf("%s: header = %d %q %q %d", name, l.Format, l.StageID, l.StageHash, l.Seed)
		}
		if len(l.Commands) == 0 || l.Commands[0].T != 0 || l.Commands[0].Cmd.Type != sim.CmdGameStart {
			t.Errorf("%s: 最初のコマンドが {0, cmd.game.start} でない: %+v", name, l.Commands)
		}
		if len(l.Commands) != *lines[i].Commands {
			t.Errorf("%s: commands = %d, run の行は %d", name, len(l.Commands), *lines[i].Commands)
		}
	}
}

// R-7: V-6（CLI）。run の入力ログを replay すると、同じ行になる。
func TestRunReplayMatches(t *testing.T) {
	t.Parallel()
	dir, lines := runWithInputs(t, "random", "1..3")
	for _, want := range lines {
		path := inputLogPath(dir, testStage, "random", want.Seed)
		for _, extra := range [][]string{nil, {"--expect", want.TraceHash}} {
			args := append([]string{"replay", "--data", testData, "--input", path}, extra...)
			r := runCLI(t, nil, args...)
			r.wantCode(t, exitOK)
			got := parseLines(t, r.stdout)
			if len(got) != 1 {
				t.Fatalf("replay lines = %d", len(got))
			}
			g := got[0]
			if g.Seed != want.Seed || !g.OK || g.Phase != want.Phase || *g.T != *want.T ||
				*g.Events != *want.Events || *g.Commands != *want.Commands || g.TraceHash != want.TraceHash ||
				g.Stars == nil || *g.Stars != *want.Stars || g.Score == nil || *g.Score != *want.Score {
				t.Errorf("seed %d: replay %+v != run %+v", want.Seed, g, want)
			}
		}
	}
}

// 変換（stage.SimConfig）が誤りを返すステージでは、全シードが失敗し、行は seed・ok・error だけになる。
func TestRunEngineError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	islands, err := os.ReadFile(filepath.Join(testData, "islands.json"))
	if err != nil {
		t.Fatal(err)
	}
	yaml, err := os.ReadFile(filepath.Join(testData, "stages", testStage+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(yaml), "checkQuorum: true", "checkQuorum: false", 1)
	if bad == string(yaml) {
		t.Fatal("checkQuorum が見つからない")
	}
	if err := os.WriteFile(filepath.Join(dir, "islands.json"), islands, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "stages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stages", testStage+".yaml"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runCLI(t, nil, "run", "--data", dir, "--stage", testStage, "--seeds", "1..2")
	r.wantCode(t, exitFail)
	got := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	if len(got) != 2 {
		t.Fatalf("stdout = %q", r.stdout)
	}
	for i, line := range got {
		want := fmt.Sprintf(`{"seed":%d,"ok":false,"error":"simcli: %s: stage: %s: raft.checkQuorum: `, i+1, testStage, testStage)
		if !strings.HasPrefix(line, want) {
			t.Errorf("line %d = %s, want prefix %s", i, line, want)
		}
	}
	if !strings.Contains(r.stderr, "simcli: seed 1: ") || !strings.HasSuffix(lastLine(r.stderr), "ok=0 failed=2") {
		t.Errorf("stderr = %s", r.stderr)
	}
}

// --inputs-dir に書けないシードは失敗になり、残りのシードも回す（終了コード 1）。
func TestRunInputsDirUnwritable(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runCLI(t, nil, "run", "--data", testData, "--stage", testStage, "--bot", "random", "--seeds", "1..2", "--inputs-dir", filepath.Join(file, "in"))
	r.wantCode(t, exitFail)
	lines := parseLines(t, r.stdout)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	for _, l := range lines {
		if l.OK || !strings.Contains(l.Error, "入力ログを書けない") {
			t.Errorf("seed %d: ok = %v, error = %q", l.Seed, l.OK, l.Error)
		}
	}
	if !strings.HasSuffix(lastLine(r.stderr), "seeds=2 ok=0 failed=2") {
		t.Errorf("stderr = %s", r.stderr)
	}
}
