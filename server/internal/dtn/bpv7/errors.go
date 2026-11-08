package bpv7

import "fmt"

// Code は読み込み・符号化の失敗の種類（bpv7-profile.md §6）。
type Code string

// bpv7-profile.md §6 の 10 個のエラーコード。Decode は上から順ではなく、
// 次の検査の順（§6 の「検査の順」）に確かめて最初の失敗を返す:
// too_large → malformed → version → unsupported_flags → crc → eid → timestamp →
// age → unsupported_block → structure。
const (
	CodeMalformed        Code = "bpv7_malformed"
	CodeVersion          Code = "bpv7_version"
	CodeUnsupportedFlags Code = "bpv7_unsupported_flags"
	CodeCRC              Code = "bpv7_crc"
	CodeEID              Code = "bpv7_eid"
	CodeTimestamp        Code = "bpv7_timestamp"
	CodeAge              Code = "bpv7_age"
	CodeUnsupportedBlock Code = "bpv7_unsupported_block"
	CodeStructure        Code = "bpv7_structure"
	CodeTooLarge         Code = "bpv7_too_large"
)

// Error はこのパッケージが返すエラー。Code で種類を見分ける。
type Error struct {
	Code   Code
	Detail string // 人が読むための補足。比べない
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return "bpv7: " + string(e.Code)
	}
	return "bpv7: " + string(e.Code) + ": " + e.Detail
}

// Is は、Code が同じ *Error を同じエラーとみなす（errors.Is(err, &Error{Code: CodeCRC})）。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

func errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}
