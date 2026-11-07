package main

import (
	"fmt"
	"io"
	"os"

	"github.com/K-Kizuku/raft-game/server/internal/replaykit"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

const replaySynopsis = "simcli replay --data DIR --input FILE [--invariants=BOOL] [--expect HASH] [--trace FILE]"

// runReplay は replay サブコマンド。入力ログを最初から適用し直し、結果の行を stdout に 1 行出す。
// Engine の公開 API（New・Snapshot・Apply・RunUntil・DrainEvents）で行う（T-304 の sim.Replay に置き換えるのは後のタスク）。
// --trace があれば、成功したとき（終了コードが 0 のとき）だけ、作り直したトレースを record と同じ記録の形式で書く。
func runReplay(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("replay")
	data := fs.String("data", "", "データのディレクトリ（DIR/islands.json と DIR/stages/*.yaml）（必須）")
	input := fs.String("input", "", "入力ログのファイル。- なら標準入力（必須）")
	invariants := fs.Bool("invariants", true, "不変条件の検査（テストモード）。結果は変わらない")
	expect := fs.String("expect", "", "traceHash が HASH（sha256:<64 桁の 16 進の小文字>）と違えば失敗にする")
	trace := fs.String("trace", "", "入力ログから作り直したトレースを、record と同じ記録の形式で FILE に書く")
	if code, done := parseFlags(fs, args, stderr, replaySynopsis, "data", "input"); done {
		return code
	}
	if *expect != "" && !hashPattern.MatchString(*expect) {
		_, _ = fmt.Fprintf(stderr, "simcli: --expect %q は sha256: と 64 桁の 16 進の小文字でない\n", *expect)
		return exitUsage
	}
	if *trace == "-" {
		_, _ = fmt.Fprintln(stderr, "simcli: --trace に - は使えない（標準出力は結果の行に使う）")
		return exitUsage
	}

	l, err := readInputLog(*input, stdin)
	if err != nil {
		printErr(stderr, err)
		return exitFail
	}
	if l.Ignored > 0 {
		_, _ = fmt.Fprintf(stderr, "simcli replay: 無視したコマンド %d 件\n", l.Ignored)
	}
	st := loadStageReport(*data, l.StageID, stderr)
	if st == nil {
		return exitFail
	}
	if st.Hash != l.StageHash {
		_, _ = fmt.Fprintf(stderr, "simcli: 入力ログのステージ %s のハッシュが今の定義と違う（ログ: %s、今: %s）\n",
			l.StageID, l.StageHash, st.Hash)
		return exitFail
	}

	res := seedResult{Seed: l.Seed}
	var (
		snap   sim.Snapshot
		events []sim.DomainEvent
	)
	e, err := newEngine(st, l.Seed, *invariants)
	if err != nil {
		res.fail(err)
	} else {
		snap = e.Snapshot() // 入力を適用する前（record と同じ時点。phase は intro、lastSeq は 0）
		end := sim.SimTime(st.DurationMs)
		stack, err := safely(func() error { return applyInputs(e, l.Commands, l.Pos, end) })
		events = e.DrainEvents()
		res.OK = true
		res.observe(e, len(events), len(e.InputLog()))
		if err != nil {
			res.fail(err)
			if len(stack) > 0 {
				res.stacks = append(res.stacks, stack)
			}
		}
	}
	printSeedFailure(stderr, l.Seed, res)
	if err := res.writeLine(stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "simcli: 標準出力に書けない: %v\n", err)
		return exitFail
	}
	if *expect != "" && res.TraceHash != *expect {
		_, _ = fmt.Fprintf(stderr, "simcli replay: traceHash が違う（期待: %s、実際: %s）\n", *expect, res.TraceHash)
		return exitFail
	}
	if !res.OK {
		return exitFail
	}
	if *trace != "" {
		return writeTrace(*trace, replaykit.NewRecordHeader(buildID(), st, l.Seed, "", len(events), res.TraceHash), snap, events, stderr)
	}
	return exitOK
}

// writeTrace は記録（DesignDoc §13.5）を path に書き、終了コードを返す。
func writeTrace(path string, h replaykit.RecordHeader, snap sim.Snapshot, events []sim.DomainEvent, stderr io.Writer) int {
	out, err := replaykit.EncodeRecord(h, snap, events)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "simcli: 記録を作れない（内部の誤り）: %v\n", err)
		return exitFail
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		_, _ = fmt.Fprintf(stderr, "simcli: トレースを書けない: %v\n", err)
		return exitFail
	}
	_, _ = fmt.Fprintf(stderr, "simcli replay: トレースを %s に書いた（events=%d）\n", path, len(events))
	return exitOK
}

// readInputLog は path（- なら stdin）の入力ログを読む。
func readInputLog(path string, stdin io.Reader) (replaykit.InputLog, error) {
	if path == "-" {
		return replaykit.DecodeInputLog(stdin)
	}
	f, err := os.Open(path)
	if err != nil {
		return replaykit.InputLog{}, fmt.Errorf("入力ログを開けない: %w", err)
	}
	defer func() { _ = f.Close() }()
	l, err := replaykit.DecodeInputLog(f)
	if err != nil {
		return replaykit.InputLog{}, fmt.Errorf("%s: %w", path, err)
	}
	return l, nil
}

// applyInputs は各コマンドを、その t まで RunUntil してから Apply する。最後に、航行中ならラウンドの終わり
// （end）まで進める（run と同じハッシュになるように）。PhaseIntro のままなら進めない。
// pos は、エラーの文に書くファイルの中の添字（nil なら cmds の添字）。
func applyInputs(e *sim.Engine, cmds []sim.InputEntry, pos []int, end sim.SimTime) error {
	for k, in := range cmds {
		i := k
		if pos != nil {
			i = pos[k]
		}
		if in.T < e.Now() {
			return fmt.Errorf("commands[%d]: t %d は前のコマンドより前", i, in.T)
		}
		if in.T > end {
			return fmt.Errorf("commands[%d]: t %d はラウンドの終わり %d より後", i, in.T, end)
		}
		e.RunUntil(in.T)
		ack := e.Apply(in.Cmd)
		if !ack.OK {
			return fmt.Errorf("commands[%d]: %s: %s: %s", i, in.Cmd.Type, ack.Code, ack.Message)
		}
		if ack.T != in.T {
			return fmt.Errorf("commands[%d]: 適用した時刻 %d が %d と違う", i, ack.T, in.T)
		}
	}
	if e.Phase() == sim.PhaseSailing {
		e.RunUntil(end)
	}
	return nil
}
