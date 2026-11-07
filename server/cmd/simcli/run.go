package main

import (
	"fmt"
	"io"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

const runSynopsis = "simcli run --data DIR --stage ID [--bot NAME] --seeds LIST [--invariants=BOOL] [--determinism] [--inputs-dir DIR]"

// runRun は run サブコマンド。シードを書いた順に回し、1 シード 1 行の JSON を stdout に出す。
// 1 つのシードが失敗しても残りを回す。全シードが成功なら 0、失敗があれば 1、使い方の誤りなら 2。
func runRun(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("run")
	data := fs.String("data", "", "データのディレクトリ（DIR/islands.json と DIR/stages/*.yaml）（必須）")
	stageID := fs.String("stage", "", "ステージの id（必須）")
	botName := fs.String("bot", defaultBot, "ボットの名前")
	seedsArg := fs.String("seeds", "", `シードの並び。例: "1..1000"・"7"・"1..3,10,20..22"（必須）`)
	invariants := fs.Bool("invariants", true, "不変条件の検査（テストモード）。結果は変わらない")
	determinism := fs.Bool("determinism", false, "各シードを 2 回回し、トレースのハッシュと入力ログが一致することを確かめる")
	inputsDir := fs.String("inputs-dir", "", "各シードの入力ログを DIR/<stageId>-<bot>-<seed>.json に書く")
	if code, done := parseFlags(fs, args, stderr, runSynopsis, "data", "stage", "seeds"); done {
		return code
	}
	seeds, err := parseSeeds(*seedsArg)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "simcli: %v\n", err)
		return exitUsage
	}
	if !checkBot(*botName, stderr) {
		return exitUsage
	}
	st := loadStageReport(*data, *stageID, stderr)
	if st == nil {
		return exitFail
	}

	build := buildID()
	failed := 0
	for _, seed := range seeds {
		res := runSeed(st, seed, *botName, *invariants, *determinism, *inputsDir, build)
		if !res.OK {
			failed++
			printSeedFailure(stderr, seed, res)
		}
		if err := res.writeLine(stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "simcli: 標準出力に書けない: %v\n", err)
			return exitFail
		}
	}
	_, _ = fmt.Fprintf(stderr, "simcli run: %s bot=%s seeds=%d ok=%d failed=%d\n",
		st.ID, *botName, len(seeds), len(seeds)-failed, failed)
	if failed > 0 {
		return exitFail
	}
	return exitOK
}

// runSeed は run の 1 シード分（決定性の確かめと入力ログの書き出しを含む）。
func runSeed(st *stage.Stage, seed uint64, botName string, invariants, determinism bool, inputsDir, build string) seedResult {
	res, _, inputs, err := playSeed(st, seed, botName, invariants)
	if err == nil && determinism {
		res2, _, inputs2, err2 := playSeed(st, seed, botName, invariants)
		res.stacks = append(res.stacks, res2.stacks...)
		switch {
		case err2 != nil:
			res.fail(fmt.Errorf("determinism: 2 回目が失敗した: %w", err2))
		case res2.TraceHash != res.TraceHash:
			res.fail(fmt.Errorf("determinism: traceHash %s != %s", res.TraceHash, res2.TraceHash))
		default:
			if i := inputsDiffer(inputs, inputs2); i >= 0 {
				res.fail(fmt.Errorf("determinism: input log differs at commands[%d]", i))
			}
		}
	}
	if inputsDir != "" {
		l := inputLog{
			Format:    inputLogFormat,
			Build:     build,
			StageID:   st.ID,
			StageHash: st.Hash,
			Seed:      seed,
			Commands:  inputs,
		}
		if inputs == nil {
			l.Commands = []sim.InputEntry{}
		}
		if werr := writeInputLog(inputsDir, botName, l); werr != nil {
			werr = fmt.Errorf("入力ログを書けない: %w", werr)
			if !res.OK { // 先の失敗の理由を残す
				werr = fmt.Errorf("%s; %w", res.Error, werr)
			}
			res.fail(werr)
		}
	}
	return res
}
