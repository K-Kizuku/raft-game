package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

const recordSynopsis = "simcli record --data DIR --stage ID [--bot NAME] --seed N [--invariants=BOOL] [--inputs-dir DIR]"

// 記録の形式（DesignDoc §13.5）。
const (
	recordFormat = 1
	recordKind   = "simcli.record"
)

// recordHeader は記録の 1 行目（ヘッダー）。キーはこの順に出る。
type recordHeader struct {
	Format     int    `json:"format"`
	Kind       string `json:"kind"`
	Build      string `json:"build"`
	StageID    string `json:"stageId"`
	StageHash  string `json:"stageHash"`
	Seed       uint64 `json:"seed"`
	Bot        string `json:"bot"`
	DurationMs int64  `json:"durationMs"`
	EventCount int    `json:"eventCount"`
	TraceHash  string `json:"traceHash"`
}

// runRecord は record サブコマンド。ボットに 1 ラウンド遊ばせ、記録を stdout に書く。
// 失敗したら stdout に何も書かない（--inputs-dir の入力ログは書く）。
func runRecord(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("record")
	data := fs.String("data", "", "データのディレクトリ（DIR/islands.json と DIR/stages/*.yaml）（必須）")
	stageID := fs.String("stage", "", "ステージの id（必須）")
	botName := fs.String("bot", defaultBot, "ボットの名前")
	seedArg := fs.String("seed", "", "シード（10 進の uint64）（必須）")
	invariants := fs.Bool("invariants", true, "不変条件の検査（テストモード）。結果は変わらない")
	inputsDir := fs.String("inputs-dir", "", "入力ログを DIR/<stageId>-<bot>-<seed>.json に書く")
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
	st := loadStageReport(*data, *stageID, stderr)
	if st == nil {
		return exitFail
	}

	var snap sim.Snapshot
	res, events, inputs, err := playSeedWith(st, seed, *botName, *invariants, func(e *sim.Engine) {
		snap = e.Snapshot() // ボットを動かす前（phase は intro、lastSeq は 0）
	})
	build := buildID()
	if *inputsDir != "" {
		if inputs == nil {
			inputs = []sim.InputEntry{}
		}
		l := inputLog{Format: inputLogFormat, Build: build, StageID: st.ID, StageHash: st.Hash, Seed: seed, Commands: inputs}
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

	out, err := encodeRecord(recordHeader{
		Format:     recordFormat,
		Kind:       recordKind,
		Build:      build,
		StageID:    st.ID,
		StageHash:  st.Hash,
		Seed:       seed,
		Bot:        *botName,
		DurationMs: st.DurationMs,
		EventCount: len(events),
		TraceHash:  res.TraceHash,
	}, snap, events)
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
	return exitOK
}

// encodeRecord は記録の全体をメモリの上で組み立てる。1 行目にヘッダー（末尾に ,）、2 行目に "snapshot":…,、
// 3 行目に "events":[、続けてイベントを 1 件 1 行（最後以外は末尾に ,）、最後の行に ]}。末尾に改行 1 つ。
// イベントの各行はトレースのハッシュ（DesignDoc §10.2）と同じ json.Marshal の結果なので、各行の末尾の , を除いて
// 改行を付けて連結した SHA-256 が traceHash と一致する。
func encodeRecord(h recordHeader, snap sim.Snapshot, events []sim.DomainEvent) ([]byte, error) {
	head, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	sb, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	var b bytes.Buffer
	b.Write(head[:len(head)-1]) // 最後の } を除く
	b.WriteString(",\n")
	if err := writeLine(&b, `"snapshot":`, sb, ","); err != nil {
		return nil, err
	}
	b.WriteString(`"events":[` + "\n")
	for i, ev := range events {
		line, err := json.Marshal(ev)
		if err != nil {
			return nil, fmt.Errorf("events[%d]: %w", i, err)
		}
		sep := ","
		if i == len(events)-1 {
			sep = ""
		}
		if err := writeLine(&b, "", line, sep); err != nil {
			return nil, fmt.Errorf("events[%d]: %w", i, err)
		}
	}
	b.WriteString("]}\n")
	return b.Bytes(), nil
}

// writeLine は prefix・body・suffix と改行を b に書く。body に改行があれば誤り（1 件 1 行が崩れるため）。
func writeLine(b *bytes.Buffer, prefix string, body []byte, suffix string) error {
	if bytes.ContainsAny(body, "\r\n") {
		return errors.New("JSON に改行が入っている")
	}
	b.WriteString(prefix)
	b.Write(body)
	b.WriteString(suffix)
	b.WriteByte('\n')
	return nil
}
