// Command simcli は、サーバーなしで sim.Engine を動かす（DesignDoc §13.5、T-108）。
//
//	simcli run    --data DIR --stage ID [--bot NAME] --seeds LIST [--invariants=BOOL] [--determinism] [--inputs-dir DIR]
//	simcli replay --data DIR --input FILE [--invariants=BOOL] [--expect HASH]
//	simcli record --data DIR --stage ID [--bot NAME] --seed N [--invariants=BOOL] [--inputs-dir DIR]
//	simcli help
//
// 終了コードは 0（成功）・1（実行の失敗）・2（使い方の誤り）。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/K-Kizuku/raft-game/server/internal/bot"
)

// 終了コード。
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

// defaultBot は --bot の既定値。
const defaultBot = "greedy"

const usageText = `使い方:
  simcli run    --data DIR --stage ID [--bot NAME] --seeds LIST [--invariants=BOOL] [--determinism] [--inputs-dir DIR]
  simcli replay --data DIR --input FILE [--invariants=BOOL] [--expect HASH]
  simcli record --data DIR --stage ID [--bot NAME] --seed N [--invariants=BOOL] [--inputs-dir DIR]
  simcli help

サブコマンド:
  run     ボットに、シードの数だけラウンドを遊ばせ、1 シード 1 行の JSON を出す
  replay  入力ログを最初から適用し直し、結果の行を 1 行出す
  record  ボットに 1 ラウンド遊ばせ、イベントの列（記録）を標準出力に書く

終了コード: 0 = 成功、1 = 実行の失敗、2 = 使い方の誤り
各サブコマンドのフラグは simcli <サブコマンド> -h で出る。
`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// run はコマンドの本体。終了コード（0・1・2）を返す。グローバルな状態を持たず、os.Args・環境変数・カレントディレクトリを見ない。
// stdin は replay --input - のときだけ読む。
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "simcli: サブコマンドがない")
		_, _ = io.WriteString(stderr, usageText)
		return exitUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = io.WriteString(stderr, usageText)
		return exitOK
	case "run":
		return runRun(args[1:], stdout, stderr)
	case "replay":
		return runReplay(args[1:], stdin, stdout, stderr)
	case "record":
		return runRecord(args[1:], stdout, stderr)
	}
	_, _ = fmt.Fprintf(stderr, "simcli: 未知のサブコマンド %q\n", args[0])
	_, _ = io.WriteString(stderr, usageText)
	return exitUsage
}

// newFlagSet はサブコマンドのフラグの集合を作る。flag の出力は捨て、誤りは parseFlags が stderr に出す。
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// parseFlags は args を解釈する。続けてよければ (0, false)、終えるなら (終了コード, true) を返す。
// -h は使い方を出して 0、解釈の誤り・余分な引数・必須のフラグがないときは 2。
// required はフラグの名前（"-" を付けない）。足りないものを全部並べる。
func parseFlags(fs *flag.FlagSet, args []string, stderr io.Writer, synopsis string, required ...string) (int, bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printSubUsage(fs, stderr, synopsis)
			return exitOK, true
		}
		_, _ = fmt.Fprintf(stderr, "simcli %s: %v\n", fs.Name(), err)
		_, _ = fmt.Fprintf(stderr, "simcli %s: 使い方は simcli %s -h\n", fs.Name(), fs.Name())
		return exitUsage, true
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "simcli %s: 余分な引数: %s\n", fs.Name(), strings.Join(fs.Args(), " "))
		return exitUsage, true
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var missing []string
	for _, r := range required {
		if !set[r] {
			missing = append(missing, "--"+r)
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr, "simcli %s: 必須のフラグがない: %s\n", fs.Name(), strings.Join(missing, " "))
		return exitUsage, true
	}
	return exitOK, false
}

// printSubUsage はサブコマンドの使い方を stderr に出す。
func printSubUsage(fs *flag.FlagSet, stderr io.Writer, synopsis string) {
	_, _ = fmt.Fprintf(stderr, "使い方: %s\n\nフラグ:\n", synopsis)
	fs.SetOutput(stderr)
	fs.PrintDefaults()
	fs.SetOutput(io.Discard)
}

// checkBot は --bot の値を確かめる。未知なら使える名前を並べて false。
func checkBot(name string, stderr io.Writer) bool {
	names := bot.Names()
	if slices.Contains(names, name) {
		return true
	}
	_, _ = fmt.Fprintf(stderr, "simcli: 未知のボット %s（使えるもの: %s）\n", name, strings.Join(names, ", "))
	return false
}

// parseSeed は --seed の値（10 進の uint64）を解釈する。
func parseSeed(s string) (uint64, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("--seed %q は 10 進の uint64 でない", s)
	}
	return n, nil
}

// hashPattern は --expect・traceHash の形（"sha256:" + 64 桁の 16 進の小文字）。
var hashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// printErr は err の各行を "simcli: " で始めて stderr に出す。
func printErr(stderr io.Writer, err error) {
	for line := range strings.SplitSeq(err.Error(), "\n") {
		_, _ = fmt.Fprintf(stderr, "simcli: %s\n", line)
	}
}

// printSeedFailure は失敗したシードのエラーと、panic のスタックトレース（あれば）を stderr に出す。
func printSeedFailure(stderr io.Writer, seed uint64, res seedResult) {
	if res.OK {
		return
	}
	_, _ = fmt.Fprintf(stderr, "simcli: seed %d: %s\n", seed, res.Error)
	for _, st := range res.stacks {
		_, _ = stderr.Write(st)
		if len(st) > 0 && st[len(st)-1] != '\n' {
			_, _ = io.WriteString(stderr, "\n")
		}
	}
}
