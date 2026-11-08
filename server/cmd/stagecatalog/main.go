// Command stagecatalog は、ステージ定義（data/stages/*.yaml）と data/islands.json から、
// Worker に同梱する GET /api/stages の一覧 web/worker/routes/stage-catalog.gen.json を作る
// （docs/06-protocol.md §2・T-311）。
//
//	go -C server run ./cmd/stagecatalog --data ../data --out ../web/worker/routes/stage-catalog.gen.json [--check]
//
// 同じ入力からは、必ず同じバイト列を出す。時刻・ハッシュ・パスなど、ステージ定義以外で変わる値を入れない。
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/stageconv"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// catalogFormat は生成物の format（1 で固定）。
const catalogFormat = 1

// catalog は生成物の形。
type catalog struct {
	Format int            `json:"format"`
	Stages []catalogStage `json:"stages"`
}

// catalogStage は 1 つのステージ。GET /api/stages の形（protocol.StageSummary）に、DTN の環境が使う
// dtn を足したもの（docs/dtn/environment.md §1・DTN-12）。Worker は dtn をブラウザに返さない。
type catalogStage struct {
	protocol.StageSummary
	DTN dtnInfo `json:"dtn"`
}

// dtnInfo は分散モード（DTN）の Actor の名前と、定義したイベントの種類。
//
//   - actors: "island:<ノード ID>"（1..N の順）・"ferry:<船の ID>"（ferries の順）
//   - eventKinds: events の種類（最初に出た順・重複なし）。demo.storm があれば最後に "demo.storm"
//   - unsupportedEvents: eventKinds のうち分散モードが対応しないもの（docs/dtn/simulation-time.md §6）。
//     空でないステージは、DTN の環境で起動を断る（dtn_unsupported_stage）
type dtnInfo struct {
	Actors            []string `json:"actors"`
	EventKinds        []string `json:"eventKinds"`
	UnsupportedEvents []string `json:"unsupportedEvents"`
}

// dtnSupportedEvents は分散モードが対応するイベントの種類（嵐とデモの嵐）。DTN-17 で増やす。
var dtnSupportedEvents = map[string]bool{
	string(stage.EventStorm): true,
	"demo.storm":             true,
}

// dtnOf はステージの dtnInfo を作る。同じステージからは必ず同じ値になる（map の走査順に依存しない）。
func dtnOf(st *stage.Stage) dtnInfo {
	info := dtnInfo{Actors: []string{}, EventKinds: []string{}, UnsupportedEvents: []string{}}
	for _, is := range st.Islands {
		info.Actors = append(info.Actors, fmt.Sprintf("island:%d", is.NodeID))
	}
	for _, f := range st.Ferries {
		info.Actors = append(info.Actors, "ferry:"+f.ID)
	}
	seen := map[string]bool{}
	add := func(kind string) {
		if seen[kind] {
			return
		}
		seen[kind] = true
		info.EventKinds = append(info.EventKinds, kind)
		if !dtnSupportedEvents[kind] {
			info.UnsupportedEvents = append(info.UnsupportedEvents, kind)
		}
	}
	for _, ev := range st.Events {
		add(string(ev.Kind))
	}
	if st.Demo != nil && st.Demo.Storm != nil {
		add("demo.storm")
	}
	return info
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run はコマンドの本体。終了コード（0・1・2）を返す。
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("stagecatalog", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data", "", "データのディレクトリ（data）。islands.json と stages/ を読む")
	out := fs.String("out", "", "出力の JSON（web/worker/routes/stage-catalog.gen.json）")
	check := fs.Bool("check", false, "ファイルを書かず、--out が生成結果と同じかを確かめる")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "使い方: stagecatalog --data DIR --out FILE [--check]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "stagecatalog: 余分な引数: %s\n", strings.Join(fs.Args(), " "))
		fs.Usage()
		return 2
	}
	var missing []string
	for _, f := range []struct{ name, v string }{{"--data", *dataDir}, {"--out", *out}} {
		if f.v == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr, "stagecatalog: 必須のフラグがない: %s\n", strings.Join(missing, " "))
		fs.Usage()
		return 2
	}

	stages, err := stage.LoadDir(os.DirFS(*dataDir), "islands.json", "stages")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "stagecatalog: %v\n", err)
		return 1
	}
	demos := 0
	for _, st := range stages {
		for _, w := range st.Warnings {
			_, _ = fmt.Fprintf(stderr, "stagecatalog: 警告: %s: %s: %s\n", st.ID, w.Path, w.Message)
		}
		if st.Demo != nil {
			demos++
		}
	}
	data, err := encode(stages)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "stagecatalog: %v\n", err)
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
		_, _ = fmt.Fprintf(stderr, "stagecatalog: %s が%s。作り直す:\n  %s\n", *out, reason, regenCommand(*dataDir, *out))
		return 1
	}
	if err := writeAtomic(*out, data); err != nil {
		_, _ = fmt.Fprintf(stderr, "stagecatalog: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stderr, "stagecatalog: %s を書いた（ステージ %d・うちデモ %d）\n", *out, len(stages), demos)
	return 0
}

// encode は一覧の JSON を作る。HTML のエスケープをせず、2 つの空白で字下げし、改行 1 つで終わる。
func encode(stages []*stage.Stage) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	summaries := stageconv.Summaries(stages)
	entries := make([]catalogStage, len(stages))
	for i, st := range stages {
		entries[i] = catalogStage{StageSummary: summaries[i], DTN: dtnOf(st)}
	}
	if err := enc.Encode(catalog{Format: catalogFormat, Stages: entries}); err != nil {
		return nil, fmt.Errorf("JSON にできない: %w", err)
	}
	return buf.Bytes(), nil
}

// regenCommand は作り直すコマンド（渡されたパスのまま）。
func regenCommand(dataDir, out string) string {
	return "go -C server run ./cmd/stagecatalog --data " + dataDir + " --out " + out
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
