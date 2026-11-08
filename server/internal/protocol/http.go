package protocol

// 公開 API（§2）の本文。管理用 API（/api/admin/guards）は Worker の中だけで使うので、ここに置かない。

// HealthzResponse は GET /api/healthz の応答。
type HealthzResponse struct {
	Status string `json:"status" tstype:"\"ok\""`
	Build  string `json:"build"`
}

// StagesResponse は GET /api/stages の応答。
type StagesResponse struct {
	Stages   []StageSummary `json:"stages"`
	Features Features       `json:"features"`
}

// Features は、その Worker（standalone ではプロセス）で有効な任意の機能（T-724）。画面が切り替えを出すかを決める。
type Features struct {
	AutoFerry bool `json:"autoFerry"` // 自動航行（cmd.ferry.auto）。Worker の変数 AUTO_FERRY が "1"、standalone は --auto-ferry
}

// StageSummary はステージ選択の 1 行。
type StageSummary struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Region     string    `json:"region"`
	Islands    int       `json:"islands"`
	Ferries    int       `json:"ferries"`
	DurationMs int64     `json:"durationMs"`
	Unlock     *string   `json:"unlock" tstype:"string | null,required"`
	Intro      StageText `json:"intro"`    // ステージの紹介文（データ仕様 §5.2 の intro）
	Learning   []string  `json:"learning"` // 学べることのキー（データ仕様 §5.4）。なければ []（null にしない）
	Demo       bool      `json:"demo"`     // デモ用のステージ（定義に demo がある）。POST /api/sessions で demo: true を送る
}

// StageText は出典区分つきの文章（データ仕様 §1・§5.4 の Text）。
type StageText struct {
	Text       string      `json:"text"`
	Provenance Provenance  `json:"provenance"`
	Source     *TextSource `json:"source" tstype:"TextSource | null,required"`
}

// TextSource は文章の出典（データ仕様 §5.4 の Source）。
type TextSource struct {
	Dataset string  `json:"dataset"` // islands.json の sources のキー
	Region  string  `json:"region"`
	Field   string  `json:"field"`
	Quote   *string `json:"quote" tstype:"string | null,required"` // 引用がなければ null
}

// CreateSessionRequest は POST /api/sessions の本文。
type CreateSessionRequest struct {
	StageID        string  `json:"stageId"`
	Seed           *uint64 `json:"seed,omitempty"`
	Demo           *bool   `json:"demo,omitempty"`
	TurnstileToken *string `json:"turnstileToken,omitempty"`
}

// CreateSessionResponse は POST /api/sessions の応答（201）。
type CreateSessionResponse struct {
	SessionID string `json:"sessionId"`
	Token     string `json:"token"`
	WSPath    string `json:"wsPath"`
}

// ErrorResponse はエラーの本文。
type ErrorResponse struct {
	Code    HTTPErrorCode `json:"code"`
	Message string        `json:"message"`
}
