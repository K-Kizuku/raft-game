package protocol

import "fmt"

// Code はエラーコード（actor-protocol.md §6）。
type Code string

// エラーコード（actor-protocol.md §6 の表の 14 個）。
const (
	CodeInvalid            Code = "invalid"             // 形が不正・上限が減った・未知の手紙の ID
	CodeUnsupportedVersion Code = "unsupported_version" // v が違う
	CodeSessionMismatch    Code = "session_mismatch"    // 別のセッション
	CodeUnknownActor       Code = "unknown_actor"       // 別の Actor
	CodeStaleEpoch         Code = "stale_epoch"         // 古い Actor の世代
	CodeStaleCoordEpoch    Code = "stale_coord_epoch"   // 古い Coordinator の世代
	CodeStaleEvent         Code = "stale_event"         // 古い eventSeq
	CodeConflict           Code = "conflict"            // 同じ eventSeq で中身が違う
	CodeTooLarge           Code = "too_large"           // 上限を超えた
	CodeNotReady           Code = "not_ready"           // 起動・復元の途中
	CodeContactClosed      Code = "contact_closed"      // 閉じた Contact を使う転送
	CodeContactMismatch    Code = "contact_mismatch"    // Contact と合わない転送
	CodeRestoreFailed      Code = "restore_failed"      // 再生の応答の digest が記録と一致しない
	CodeInternal           Code = "internal"            // Actor の中の失敗
)

// Codes は §6 の表の順のエラーコードの一覧。
var Codes = []Code{
	CodeInvalid, CodeUnsupportedVersion, CodeSessionMismatch, CodeUnknownActor,
	CodeStaleEpoch, CodeStaleCoordEpoch, CodeStaleEvent, CodeConflict, CodeTooLarge,
	CodeNotReady, CodeContactClosed, CodeContactMismatch, CodeRestoreFailed, CodeInternal,
}

// Error は応答の error（actor-protocol.md §2.2）。error interface を満たすので、Actor は *Error を返して
// コードを指定できる（Runtime はそのコードを応答に入れる）。
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

// Error は "code: message" を返す。
func (e *Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

// Errorf は code と書式の Message の *Error を返す。
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
