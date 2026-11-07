package replaykit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// 記録の形式（DesignDoc §13.5）。
const (
	RecordFormat = 1
	RecordKind   = "simcli.record"
)

// RecordHeader は記録の 1 行目（ヘッダー）。キーはこの順に出る。
type RecordHeader struct {
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

// NewRecordHeader は記録のヘッダーを組み立てる（record と replay --trace が使う）。
// replay --trace では botName は空文字列（ボットではなく入力ログから作ったことを表す）。
func NewRecordHeader(build string, st *stage.Stage, seed uint64, botName string, eventCount int, traceHash string) RecordHeader {
	return RecordHeader{
		Format:     RecordFormat,
		Kind:       RecordKind,
		Build:      build,
		StageID:    st.ID,
		StageHash:  st.Hash,
		Seed:       seed,
		Bot:        botName,
		DurationMs: st.DurationMs,
		EventCount: eventCount,
		TraceHash:  traceHash,
	}
}

// EncodeRecord は記録の全体をメモリの上で組み立てる。1 行目にヘッダー（末尾に ,）、2 行目に "snapshot":…,、
// 3 行目に "events":[、続けてイベントを 1 件 1 行（最後以外は末尾に ,）、最後の行に ]}。末尾に改行 1 つ。
// イベントの各行はトレースのハッシュ（DesignDoc §10.2）と同じ json.Marshal の結果なので、各行の末尾の , を除いて
// 改行を付けて連結した SHA-256 が traceHash と一致する。
func EncodeRecord(h RecordHeader, snap sim.Snapshot, events []sim.DomainEvent) ([]byte, error) {
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
