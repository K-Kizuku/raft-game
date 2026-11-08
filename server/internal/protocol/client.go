package protocol

// クライアント → サーバーの payload（§4）。
// cmd.game.start・cmd.sync の payload は {}（Go では struct{}、TS では EmptyPayload）。

// HelloPayload は hello の payload。
type HelloPayload struct {
	Client string `json:"client"`
	Resume bool   `json:"resume"`
}

// GamePausePayload は cmd.game.pause の payload。
type GamePausePayload struct {
	Paused bool `json:"paused"`
}

// GameSpeedPayload は cmd.game.speed の payload。
type GameSpeedPayload struct {
	Speed float64 `json:"speed"` // 0.5 / 1 / 2 / 4（デモ操作では 8 も）。値の検査は T-303
}

// GameSkipPayload は cmd.game.skip の payload。
type GameSkipPayload struct {
	Until string `json:"until" tstype:"\"next_notable\""`
}

// FerryLettersPayload は cmd.ferry.load・cmd.ferry.unload・cmd.relay.load の payload。
type FerryLettersPayload struct {
	FerryID   string   `json:"ferryId"`
	LetterIDs []uint64 `json:"letterIds"`
}

// FerryToPayload は cmd.ferry.autoload・cmd.ferry.depart の payload。
type FerryToPayload struct {
	FerryID string `json:"ferryId"`
	To      uint64 `json:"to"`
}

// FerryAutoPayload は cmd.ferry.auto の payload（T-724）。enabled が true なら船 ferryId を novice のボットに任せる。
type FerryAutoPayload struct {
	FerryID string `json:"ferryId"`
	Enabled bool   `json:"enabled"`
}

// SubEngineerPayload は sub.engineer の payload。
type SubEngineerPayload struct {
	Enabled bool   `json:"enabled"`
	NodeID  uint64 `json:"nodeId,omitempty"` // enabled が false のときは省略できる
}

// DemoTriggerPayload は demo.trigger の payload。
type DemoTriggerPayload struct {
	Event string `json:"event" tstype:"\"storm\""`
}

// PingPayload は ping の payload。
type PingPayload struct {
	ClientTime float64 `json:"clientTime"` // performance.now() をそのまま送る
}
