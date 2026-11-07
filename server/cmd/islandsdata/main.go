// Command islandsdata は、国土交通省「離島に関するデータ」の CSV（data/raw/）から、
// ゲームが使う島のデータ data/islands.json を作る（データ仕様 §2.5・T-205）。
//
//	go -C server run ./cmd/islandsdata \
//	  --raw ../data/raw --curated ../data/curated \
//	  --select ../data/islands-select.yaml --out ../data/islands.json [--check]
//
// 同じ入力からは、必ず同じバイト列を出す。実時間・乱数・浮動小数点を使わない。
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run はコマンドの本体。終了コード（0・1・2）を返す。
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("islandsdata", flag.ContinueOnError)
	fs.SetOutput(stderr)
	raw := fs.String("raw", "", "提供データのディレクトリ（data/raw）")
	curated := fs.String("curated", "", "抜き出しのディレクトリ（data/curated）")
	sel := fs.String("select", "", "ETL の設定（data/islands-select.yaml）")
	out := fs.String("out", "", "出力の JSON（data/islands.json）")
	check := fs.Bool("check", false, "ファイルを書かず、--out が生成結果と同じかを確かめる")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "使い方: islandsdata --raw DIR --curated DIR --select FILE --out FILE [--check]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "islandsdata: 余分な引数: %s\n", strings.Join(fs.Args(), " "))
		fs.Usage()
		return 2
	}
	var missing []string
	for _, f := range []struct{ name, v string }{
		{"--raw", *raw}, {"--curated", *curated}, {"--select", *sel}, {"--out", *out},
	} {
		if f.v == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr, "islandsdata: 必須のフラグがない: %s\n", strings.Join(missing, " "))
		fs.Usage()
		return 2
	}

	data, o, err := generate(*raw, *curated, *sel)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "islandsdata: %v\n", err)
		return 1
	}
	if *check {
		cur, err := os.ReadFile(*out)
		if err == nil && bytes.Equal(cur, data) {
			return 0
		}
		reason := "生成結果と違う"
		if err != nil {
			reason = fmt.Sprintf("読めない（%v）", err)
		}
		_, _ = fmt.Fprintf(stderr, "islandsdata: %s が%s。再生成する:\n  go -C server run ./cmd/islandsdata --raw %s --curated %s --select %s --out %s\n",
			*out, reason, *raw, *curated, *sel, *out)
		return 1
	}
	if err := writeAtomic(*out, data); err != nil {
		_, _ = fmt.Fprintf(stderr, "islandsdata: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stderr, "islandsdata: %s を書いた（島 %d・抜き出し %d）\n", *out, len(o.Islands), len(o.Excerpts))
	return 0
}

// writeAtomic は、path と同じディレクトリの一時ファイルに書いてから置き換える。
// 失敗したときは path を変えず、一時ファイルを消す。
func writeAtomic(path string, data []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("%s: 一時ファイルを作れない: %w", path, err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return fmt.Errorf("%s: 書けない: %w", tmp, err)
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return fmt.Errorf("%s: 権限を変えられない: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("%s: 置き換えられない: %w", path, err)
	}
	return nil
}
