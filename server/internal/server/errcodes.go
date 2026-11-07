package server

import "github.com/coder/websocket"

// error の電文（プロトコル §5）の code。protocol には置かない（T-302 の判断。値の一覧はプロトコル §5）。
const (
	codeBadRequest         = "bad_request"
	codeVersionMismatch    = "version_mismatch"
	codeNotInitialized     = "not_initialized"
	codeAlreadyInitialized = "already_initialized"
	codeSessionMismatch    = "session_mismatch"
	codeStageNotFound      = "stage_not_found"
	codeRestoreFailed      = "restore_failed"
	codeInternalError      = "internal_error"
	codeReplaced           = "replaced"
	codeShuttingDown       = "shutting_down"
)

// fatal の error の後に閉じるコード（RFC 6455 §7.4.1）。
const (
	closeNormal        = websocket.StatusNormalClosure   // 1000: replaced
	closeGoingAway     = websocket.StatusGoingAway       // 1001: shutting_down
	closeUnsupported   = websocket.StatusUnsupportedData // 1003: バイナリのフレーム
	closePolicy        = websocket.StatusPolicyViolation // 1008: version_mismatch・not_initialized・session_mismatch・bad_request
	closeInternalError = websocket.StatusInternalError   // 1011: stage_not_found・restore_failed・internal_error
	closeTryAgainLater = websocket.StatusTryAgainLater   // 1013: 送信キューがあふれた
)

// fatalError は、接続を閉じる error の電文 1 つ（code・message・閉じるコード）。
type fatalError struct {
	code    string
	message string
	close   websocket.StatusCode
}

func (f *fatalError) Error() string { return f.code + ": " + f.message }
