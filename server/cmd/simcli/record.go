package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/K-Kizuku/raft-game/server/internal/replaykit"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

const recordSynopsis = "simcli record --data DIR --stage ID [--bot NAME] --seed N [--invariants=BOOL] [--inputs-dir DIR] [--normalized-trace FILE]"

// runRecord は record サブコマンド。ボットに 1 ラウンド遊ばせ、記録を stdout に書く。
// 失敗したら stdout に何も書かない（--inputs-dir の入力ログは書く）。--normalized-trace は成功したときだけ書く。
func runRecord(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("record")
	data := fs.String("data", "", "データのディレクトリ（DIR/islands.json と DIR/stages/*.yaml）（必須）")
	stageID := fs.String("stage", "", "ステージの id（必須）")
	botName := fs.String("bot", defaultBot, "ボットの名前")
	seedArg := fs.String("seed", "", "シード（10 進の uint64）（必須）")
	invariants := fs.Bool("invariants", true, "不変条件の検査（テストモード）。結果は変わらない")
	inputsDir := fs.String("inputs-dir", "", "入力ログを DIR/<stageId>-<bot>-<seed>.json に書く")
	normalized := fs.String("normalized-trace", "", normalizedTraceUsage)
	if code, done := parseFlags(fs, args, stderr, recordSynopsis, "data", "stage", "seed"); done {
		return code
	}
	seed, err := parseSeed(*seedArg)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "simcli: %v\n", err)
		return exitUsage
	}
	if !checkBot(*botName, stderr) {
		return exitUsage
	}
	if *normalized == "-" {
		_, _ = fmt.Fprintln(stderr, "simcli: --normalized-trace に - は使えない（標準出力は記録に使う）")
		return exitUsage
	}
	st := loadStageReport(*data, *stageID, stderr)
	if st == nil {
		return exitFail
	}

	var (
		snap sim.Snapshot
		eng  *sim.Engine
	)
	res, events, inputs, err := playSeedWith(st, seed, *botName, *invariants, func(e *sim.Engine) {
		snap = e.Snapshot() // ボットを動かす前（phase は intro、lastSeq は 0）
		eng = e             // --normalized-trace の round.result を、遊び終えたあとに読む
	})
	build := buildID()
	if *inputsDir != "" {
		if inputs == nil {
			inputs = []sim.InputEntry{}
		}
		l := replaykit.InputLog{Format: replaykit.InputLogFormat, Build: build, StageID: st.ID, StageHash: st.Hash, Seed: seed, Commands: inputs}
		if werr := writeInputLog(*inputsDir, *botName, l); werr != nil {
			werr = fmt.Errorf("入力ログを書けない: %w", werr)
			if err != nil {
				werr = errors.Join(err, werr)
			}
			err = werr
			res.fail(err)
		}
	}
	if err != nil {
		printSeedFailure(stderr, seed, res)
		return exitFail
	}

	out, err := replaykit.EncodeRecord(replaykit.NewRecordHeader(build, st, seed, *botName, len(events), res.TraceHash), snap, events)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "simcli: 記録を作れない（内部の誤り）: %v\n", err)
		return exitFail
	}
	if _, err := stdout.Write(out); err != nil {
		_, _ = fmt.Fprintf(stderr, "simcli: 標準出力に書けない: %v\n", err)
		return exitFail
	}
	_, _ = fmt.Fprintf(stderr, "simcli record: %s bot=%s seed=%d events=%d traceHash=%s\n",
		st.ID, *botName, seed, len(events), res.TraceHash)
	if *normalized != "" {
		return writeNormalizedTrace("record", *normalized, events, engineResult(eng), stderr)
	}
	return exitOK
}
