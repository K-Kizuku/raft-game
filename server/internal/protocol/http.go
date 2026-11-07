package protocol

// 公開 API（§2）の本文。管理用 API（/api/admin/guards）は Worker の中だけで使うので、ここに置かない。

// HealthzResponse は GET /api/healthz の応答。
type HealthzResponse struct {
	Status string `json:"status" tstype:"\"ok\""`
	Build  string `json:"build"`
}

// StagesResponse は GET /api/stages の応答。
type StagesResponse struct {
	Stages []StageSummary `json:"stages"`
}

// StageSummary はステージ選択の 1 行。
type StageSummary struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Region     string  `json:"region"`
	Islands    int     `json:"islands"`
	Ferries    int     `json:"ferries"`
	DurationMs int64   `json:"durationMs"`
	Unlock     *string `json:"unlock" tstype:"string | null,required"`
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
