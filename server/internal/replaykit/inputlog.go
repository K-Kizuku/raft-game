package replaykit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// InputLogFormat は入力ログの形式の版（DesignDoc §10.1 の format）。
const InputLogFormat = 1

// セッションのコマンド（プロトコル §4）。リプレイでは捨てる（DesignDoc §10.1）。
const (
	cmdGamePause = "cmd.game.pause"
	cmdGameSpeed = "cmd.game.speed"
	cmdGameSkip  = "cmd.game.skip"
)

// InputLog は入力ログ（DesignDoc §10.1）。JSON との変換は DecodeInputLog・EncodeInputLog が持つ。
type InputLog struct {
	Format    int    // 1
	Build     string // 書き出したビルド（読み込みでは使わない）
	StageID   string
	StageHash string // stage.Stage.Hash
	Seed      uint64
	Commands  []sim.InputEntry

	Ignored int   // 読み込みで捨てたセッションのコマンドの件数（書き出しでは使わない）
	Pos     []int // 読み込みで、Commands の各件がファイルの commands の何番目だったか（書き出しでは使わない）
}

// inputLogHeader は入力ログの commands より前のキー（この順に出す）。
type inputLogHeader struct {
	Format    int    `json:"format"`
	Build     string `json:"build"`
	StageID   string `json:"stageId"`
	StageHash string `json:"stageHash"`
	Seed      uint64 `json:"seed"`
}

// wireEntry・wireCmd は commands の 1 件の JSON（書き出し用）。
type wireEntry struct {
	T   sim.SimTime `json:"t"`
	Cmd wireCmd     `json:"cmd"`
}

type wireCmd struct {
	Type    sim.CommandType `json:"type"`
	Payload any             `json:"payload"`
}

type payloadEmpty struct{}

type payloadLetters struct {
	FerryID   string   `json:"ferryId"`
	LetterIDs []uint64 `json:"letterIds"`
}

type payloadTo struct {
	FerryID string `json:"ferryId"`
	To      uint64 `json:"to"`
}

type payloadEvent struct {
	Event string `json:"event"`
}

// encodePayload は sim.Command を payload の値にする。表にない種類は内部の誤り。
func encodePayload(c sim.Command) (any, error) {
	switch c.Type {
	case sim.CmdGameStart:
		return payloadEmpty{}, nil
	case sim.CmdFerryLoad, sim.CmdFerryUnload:
		ids := c.LetterIDs
		if ids == nil {
			ids = []uint64{}
		}
		return payloadLetters{FerryID: c.FerryID, LetterIDs: ids}, nil
	case sim.CmdFerryDepart, sim.CmdFerryAutoload:
		return payloadTo{FerryID: c.FerryID, To: c.To}, nil
	case sim.CmdDemoTrigger:
		return payloadEvent{Event: c.Event}, nil
	}
	return nil, fmt.Errorf("入力ログに書けないコマンド %q（内部の誤り）", c.Type)
}

// EncodeInputLog は入力ログを決定的なバイト列にする。1 行目にヘッダーと "commands":[、続けて 1 件 1 行
// （最後以外は末尾に ,）、最後の行に ]}。末尾に改行 1 つ。件数が 0 なら 1 行で "commands":[]}。
func EncodeInputLog(l InputLog) ([]byte, error) {
	head, err := json.Marshal(inputLogHeader{
		Format: l.Format, Build: l.Build, StageID: l.StageID, StageHash: l.StageHash, Seed: l.Seed,
	})
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Write(head[:len(head)-1]) // 最後の } を除く
	if len(l.Commands) == 0 {
		b.WriteString(`,"commands":[]}` + "\n")
		return b.Bytes(), nil
	}
	b.WriteString(`,"commands":[` + "\n")
	for i, in := range l.Commands {
		p, err := encodePayload(in.Cmd)
		if err != nil {
			return nil, fmt.Errorf("commands[%d]: %w", i, err)
		}
		line, err := json.Marshal(wireEntry{T: in.T, Cmd: wireCmd{Type: in.Cmd.Type, Payload: p}})
		if err != nil {
			return nil, fmt.Errorf("commands[%d]: %w", i, err)
		}
		b.Write(line)
		if i < len(l.Commands)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("]}\n")
	return b.Bytes(), nil
}

// rawInputLog は読み込み用の最上位。ポインタでキーの有無を見る。
type rawInputLog struct {
	Format    *int               `json:"format"`
	Build     *string            `json:"build"`
	StageID   *string            `json:"stageId"`
	StageHash *string            `json:"stageHash"`
	Seed      *uint64            `json:"seed"`
	Commands  *[]json.RawMessage `json:"commands"`
}

type rawEntry struct {
	T   json.RawMessage `json:"t"`
	Cmd json.RawMessage `json:"cmd"`
}

type rawCmd struct {
	Type    *string         `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type rawPayloadLetters struct {
	FerryID   *string   `json:"ferryId"`
	LetterIDs *[]uint64 `json:"letterIds"`
}

type rawPayloadTo struct {
	FerryID *string `json:"ferryId"`
	To      *uint64 `json:"to"`
}

type rawPayloadEvent struct {
	Event *string `json:"event"`
}

// timePattern は t の形（0 以上の 10 進の整数。符号・小数・指数は誤り）。
var timePattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// DecodeInputLog は入力ログを厳密に読む。未知のキー・必須のキーがない・形の誤り・JSON の後ろの余分なデータは
// エラー（場所を書く）。cmd.game.pause・cmd.game.speed・cmd.game.skip は捨て、件数を Ignored に入れる。
func DecodeInputLog(r io.Reader) (InputLog, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var raw rawInputLog
	if err := dec.Decode(&raw); err != nil {
		return InputLog{}, fmt.Errorf("入力ログ: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return InputLog{}, errors.New("入力ログ: JSON の後ろに余分なデータがある")
	}
	switch {
	case raw.Format == nil:
		return InputLog{}, errors.New("入力ログ: format: ない")
	case *raw.Format != InputLogFormat:
		return InputLog{}, fmt.Errorf("入力ログ: format: %d は未対応（%d だけ）", *raw.Format, InputLogFormat)
	case raw.StageID == nil || *raw.StageID == "":
		return InputLog{}, errors.New("入力ログ: stageId: ないか空")
	case raw.StageHash == nil || *raw.StageHash == "":
		return InputLog{}, errors.New("入力ログ: stageHash: ないか空")
	case raw.Seed == nil:
		return InputLog{}, errors.New("入力ログ: seed: ない")
	case raw.Commands == nil:
		return InputLog{}, errors.New("入力ログ: commands: ない")
	}
	l := InputLog{
		Format:    *raw.Format,
		StageID:   *raw.StageID,
		StageHash: *raw.StageHash,
		Seed:      *raw.Seed,
		Commands:  []sim.InputEntry{},
	}
	if raw.Build != nil {
		l.Build = *raw.Build
	}
	for i, msg := range *raw.Commands {
		in, ignored, err := decodeEntry(msg)
		if err != nil {
			return InputLog{}, fmt.Errorf("入力ログ: commands[%d]%w", i, err)
		}
		if ignored {
			l.Ignored++
			continue
		}
		l.Commands = append(l.Commands, in)
		l.Pos = append(l.Pos, i)
	}
	return l, nil
}

// decodeEntry は commands の 1 件を読む。エラーの文は ".cmd.payload.to: …" のように場所から始める
// （呼び出し側が "commands[i]" を前に付ける）。セッションのコマンドなら ignored が true。
func decodeEntry(msg json.RawMessage) (in sim.InputEntry, ignored bool, err error) {
	var raw rawEntry
	if err := strictUnmarshal(msg, &raw); err != nil {
		return in, false, fmt.Errorf(": %w", err)
	}
	if raw.T == nil || string(raw.T) == "null" {
		return in, false, errors.New(".t: ない")
	}
	if !timePattern.Match(raw.T) {
		return in, false, fmt.Errorf(".t: 0 以上の整数でない（値: %s）", raw.T)
	}
	t, err := strconv.ParseInt(string(raw.T), 10, 64)
	if err != nil {
		return in, false, fmt.Errorf(".t: 0 以上の整数でない（値: %s）", raw.T)
	}
	in.T = sim.SimTime(t)
	if raw.Cmd == nil || string(raw.Cmd) == "null" {
		return in, false, errors.New(".cmd: ない")
	}
	var cmd rawCmd
	if err := strictUnmarshal(raw.Cmd, &cmd); err != nil {
		return in, false, fmt.Errorf(".cmd: %w", err)
	}
	if cmd.Type == nil {
		return in, false, errors.New(".cmd.type: ない")
	}
	typ := *cmd.Type
	switch typ {
	case cmdGamePause, cmdGameSpeed, cmdGameSkip:
		return in, true, nil
	}
	if cmd.Payload == nil || string(cmd.Payload) == "null" {
		return in, false, errors.New(".cmd.payload: ない")
	}
	in.Cmd.Type = sim.CommandType(typ)
	switch in.Cmd.Type {
	case sim.CmdGameStart:
		var p payloadEmpty
		if err := strictUnmarshal(cmd.Payload, &p); err != nil {
			return in, false, fmt.Errorf(".cmd.payload: %w", err)
		}
	case sim.CmdFerryLoad, sim.CmdFerryUnload:
		var p rawPayloadLetters
		if err := strictUnmarshal(cmd.Payload, &p); err != nil {
			return in, false, fmt.Errorf(".cmd.payload: %w", err)
		}
		if err := checkFerryID(p.FerryID); err != nil {
			return in, false, err
		}
		if p.LetterIDs == nil {
			return in, false, errors.New(".cmd.payload.letterIds: ない")
		}
		in.Cmd.FerryID = *p.FerryID
		in.Cmd.LetterIDs = *p.LetterIDs
	case sim.CmdFerryDepart, sim.CmdFerryAutoload:
		var p rawPayloadTo
		if err := strictUnmarshal(cmd.Payload, &p); err != nil {
			return in, false, fmt.Errorf(".cmd.payload: %w", err)
		}
		if err := checkFerryID(p.FerryID); err != nil {
			return in, false, err
		}
		if p.To == nil {
			return in, false, errors.New(".cmd.payload.to: ない")
		}
		in.Cmd.FerryID = *p.FerryID
		in.Cmd.To = *p.To
	case sim.CmdDemoTrigger:
		// 値が "storm" かは検査しない（Replay で bad_request → ErrReplayDiverged になる）。
		var p rawPayloadEvent
		if err := strictUnmarshal(cmd.Payload, &p); err != nil {
			return in, false, fmt.Errorf(".cmd.payload: %w", err)
		}
		switch {
		case p.Event == nil:
			return in, false, errors.New(".cmd.payload.event: ない")
		case *p.Event == "":
			return in, false, errors.New(".cmd.payload.event: 空")
		}
		in.Cmd.Event = *p.Event
	default:
		return in, false, fmt.Errorf(".cmd.type: 未知の種類 %q", typ)
	}
	return in, false, nil
}

// checkFerryID は payload の ferryId があって空でないことを確かめる。
func checkFerryID(id *string) error {
	if id == nil {
		return errors.New(".cmd.payload.ferryId: ない")
	}
	if *id == "" {
		return errors.New(".cmd.payload.ferryId: 空")
	}
	return nil
}

// strictUnmarshal は未知のキーを誤りにして data を v に読む。
func strictUnmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
