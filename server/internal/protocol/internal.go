package protocol

import "encoding/json"

// 内部の電文（§9。Durable Object ⇔ コンテナ）の payload。
// internal.result の payload は RoundResult、internal.ping・internal.pong は {}。

// InternalInitPayload は internal.init の payload。
type InternalInitPayload struct {
	SessionID string `json:"sessionId"`
	StageID   string `json:"stageId"`
	Seed      uint64 `json:"seed"`
	Demo      bool   `json:"demo"`
}

// InternalRestorePayload は internal.restore の payload。
type InternalRestorePayload struct {
	InternalInitPayload `tstype:",extends"`
	InputLog            InputLog `json:"inputLog"`
	// Until は復元で最後に進める時刻（ゲーム内のミリ秒）。0 か省略なら入力ログの最後のコマンドの t。
	// DO は最後に中継した clock.t を入れる。
	Until int64 `json:"until,omitempty"`
}

// InputLog は入力ログ（DesignDoc §10.1）。
type InputLog struct {
	Format    int            `json:"format"` // 1
	Build     string         `json:"build"`
	StageID   string         `json:"stageId"`
	StageHash string         `json:"stageHash"`
	Seed      uint64         `json:"seed"`
	Commands  []InputCommand `json:"commands"`
}

// InputCommand は入力ログの 1 件。T はコマンドを適用したゲーム内時刻。
type InputCommand struct {
	T   int64         `json:"t"`
	Cmd ClientCommand `json:"cmd"`
}

// ClientCommand はクライアントのコマンド（電文の type と payload）。
type ClientCommand struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload"`
}
