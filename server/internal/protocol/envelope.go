package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Version はプロトコルのバージョン（§3 の v、§5.1 の protocol）。
const Version = 1

// MessageType は電文の種類（§3 の type）。
type MessageType string

const (
	// クライアント → サーバー（§4）
	MessageTypeHello            MessageType = "hello"
	MessageTypeCmdGameStart     MessageType = "cmd.game.start"
	MessageTypeCmdGamePause     MessageType = "cmd.game.pause"
	MessageTypeCmdGameSpeed     MessageType = "cmd.game.speed"
	MessageTypeCmdGameSkip      MessageType = "cmd.game.skip"
	MessageTypeCmdFerryLoad     MessageType = "cmd.ferry.load"
	MessageTypeCmdFerryUnload   MessageType = "cmd.ferry.unload"
	MessageTypeCmdFerryAutoload MessageType = "cmd.ferry.autoload"
	MessageTypeCmdFerryDepart   MessageType = "cmd.ferry.depart"
	MessageTypeCmdRelayLoad     MessageType = "cmd.relay.load"
	MessageTypeCmdFerryAuto     MessageType = "cmd.ferry.auto"
	MessageTypeCmdSync          MessageType = "cmd.sync"
	MessageTypeSubEngineer      MessageType = "sub.engineer"
	MessageTypeDemoTrigger      MessageType = "demo.trigger"
	MessageTypePing             MessageType = "ping"
	// サーバー → クライアント（§5）
	MessageTypeSnapshot    MessageType = "snapshot"
	MessageTypeEvents      MessageType = "events"
	MessageTypeAck         MessageType = "ack"
	MessageTypeNodeDetail  MessageType = "node.detail"
	MessageTypeRoundResult MessageType = "round.result"
	MessageTypeError       MessageType = "error"
	MessageTypePong        MessageType = "pong"
	// 内部（§9）
	MessageTypeInternalInit    MessageType = "internal.init"
	MessageTypeInternalRestore MessageType = "internal.restore"
	MessageTypeInternalResult  MessageType = "internal.result"
	MessageTypeInternalPing    MessageType = "internal.ping"
	MessageTypeInternalPong    MessageType = "internal.pong"
	MessageTypeInternalInput   MessageType = "internal.input"
)

// MessageTypes は電文の種類をすべて、定義の順に返す。呼ぶたびに新しいスライスを返す。
func MessageTypes() []MessageType {
	return []MessageType{
		MessageTypeHello,
		MessageTypeCmdGameStart,
		MessageTypeCmdGamePause,
		MessageTypeCmdGameSpeed,
		MessageTypeCmdGameSkip,
		MessageTypeCmdFerryLoad,
		MessageTypeCmdFerryUnload,
		MessageTypeCmdFerryAutoload,
		MessageTypeCmdFerryDepart,
		MessageTypeCmdRelayLoad,
		MessageTypeCmdFerryAuto,
		MessageTypeCmdSync,
		MessageTypeSubEngineer,
		MessageTypeDemoTrigger,
		MessageTypePing,
		MessageTypeSnapshot,
		MessageTypeEvents,
		MessageTypeAck,
		MessageTypeNodeDetail,
		MessageTypeRoundResult,
		MessageTypeError,
		MessageTypePong,
		MessageTypeInternalInit,
		MessageTypeInternalRestore,
		MessageTypeInternalResult,
		MessageTypeInternalPing,
		MessageTypeInternalPong,
		MessageTypeInternalInput,
	}
}

// Envelope は WebSocket の電文 1 つ（§3）。
type Envelope struct {
	V       int             `json:"v"`
	Type    MessageType     `json:"type"`
	Seq     uint64          `json:"seq,omitempty"`   // サーバー → クライアント（1 から）
	CmdID   string          `json:"cmdId,omitempty"` // クライアント → サーバー
	Payload json.RawMessage `json:"payload"`
}

var (
	// ErrBadEnvelope は、電文が JSON のオブジェクトでない・type がないなど、エンベロープの形が不正なこと。
	ErrBadEnvelope = errors.New("protocol: bad envelope")
	// ErrVersionMismatch は、v がない・Version と違うこと（サーバーは error を返して切断する。§3）。
	ErrVersionMismatch = errors.New("protocol: version mismatch")
)

// emptyObject は payload がない・null のときの値。
var emptyObject = json.RawMessage(`{}`)

// DecodeEnvelope は電文 1 つを読む。
//
//   - JSON のオブジェクトとして読めない（壊れた JSON・配列・数値・null・後ろに余分なものがある）→ ErrBadEnvelope
//   - v がない・Version 以外 → ErrVersionMismatch（type より先に判定する）
//   - type がない・空・文字列でない、seq・cmdId の型が違う → ErrBadEnvelope
//   - payload がない・null → {}
//
// 知らないキーは無視する（§8）。type が MessageTypes() にないことはエラーにしない（呼び出し側が決める）。
func DecodeEnvelope(data []byte) (Envelope, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrBadEnvelope, err)
	}
	if m == nil {
		return Envelope{}, fmt.Errorf("%w: not a JSON object", ErrBadEnvelope)
	}

	var env Envelope
	raw, ok := m["v"]
	if !ok {
		return Envelope{}, fmt.Errorf("%w: v is missing", ErrVersionMismatch)
	}
	if err := json.Unmarshal(raw, &env.V); err != nil || env.V != Version {
		return Envelope{}, fmt.Errorf("%w: v is %s, want %d", ErrVersionMismatch, raw, Version)
	}

	raw, ok = m["type"]
	if !ok || isNull(raw) {
		return Envelope{}, fmt.Errorf("%w: type is missing", ErrBadEnvelope)
	}
	if err := json.Unmarshal(raw, &env.Type); err != nil {
		return Envelope{}, fmt.Errorf("%w: type: %v", ErrBadEnvelope, err)
	}
	if env.Type == "" {
		return Envelope{}, fmt.Errorf("%w: type is empty", ErrBadEnvelope)
	}

	if raw, ok := m["seq"]; ok {
		if err := json.Unmarshal(raw, &env.Seq); err != nil {
			return Envelope{}, fmt.Errorf("%w: seq: %v", ErrBadEnvelope, err)
		}
	}
	if raw, ok := m["cmdId"]; ok {
		if err := json.Unmarshal(raw, &env.CmdID); err != nil {
			return Envelope{}, fmt.Errorf("%w: cmdId: %v", ErrBadEnvelope, err)
		}
	}

	if raw, ok := m["payload"]; ok && !isNull(raw) {
		env.Payload = raw
	} else {
		env.Payload = append(json.RawMessage(nil), emptyObject...)
	}
	return env, nil
}

// EncodeEnvelope は電文 1 つを作る。v は Version。payload が nil なら {}、そうでなければ json.Marshal の結果で、
// JSON のオブジェクトにならなければエラー。キーの順は v・type・seq（0 なら出さない）・cmdId（空なら出さない）・payload。
// 末尾に改行を付けない。
func EncodeEnvelope(typ MessageType, seq uint64, cmdID string, payload any) ([]byte, error) {
	body := emptyObject
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("protocol: encode %s payload: %w", typ, err)
		}
		if len(b) == 0 || b[0] != '{' {
			return nil, fmt.Errorf("protocol: encode %s payload: not a JSON object: %s", typ, b)
		}
		body = b
	}
	out, err := json.Marshal(Envelope{V: Version, Type: typ, Seq: seq, CmdID: cmdID, Payload: body})
	if err != nil {
		return nil, fmt.Errorf("protocol: encode %s: %w", typ, err)
	}
	return out, nil
}

// isNull は raw が JSON の null かを返す。
func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
