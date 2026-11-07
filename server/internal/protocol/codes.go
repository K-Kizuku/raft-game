package protocol

// AckCode は ack の失敗の理由（§4.1）。
type AckCode string

const (
	AckCodeFerryNotDocked   AckCode = "ferry_not_docked"
	AckCodeFerryNotReady    AckCode = "ferry_not_ready"
	AckCodeFerryBroken      AckCode = "ferry_broken"
	AckCodeCapacityExceeded AckCode = "capacity_exceeded"
	AckCodeLetterNotHere    AckCode = "letter_not_here"
	AckCodeNoRoute          AckCode = "no_route"
	AckCodeRouteBlocked     AckCode = "route_blocked"
	AckCodePhaseInvalid     AckCode = "phase_invalid"
	AckCodeRateLimited      AckCode = "rate_limited"
	AckCodeBadRequest       AckCode = "bad_request"
)

// AckCodes は AckCode をすべて、定義の順に返す。呼ぶたびに新しいスライスを返す。
func AckCodes() []AckCode {
	return []AckCode{
		AckCodeFerryNotDocked,
		AckCodeFerryNotReady,
		AckCodeFerryBroken,
		AckCodeCapacityExceeded,
		AckCodeLetterNotHere,
		AckCodeNoRoute,
		AckCodeRouteBlocked,
		AckCodePhaseInvalid,
		AckCodeRateLimited,
		AckCodeBadRequest,
	}
}

// HTTPErrorCode は公開 API のエラーの本文の code（§2）。
type HTTPErrorCode string

const (
	HTTPErrorCodeBadRequest       HTTPErrorCode = "bad_request"
	HTTPErrorCodeForbiddenOrigin  HTTPErrorCode = "forbidden_origin"
	HTTPErrorCodeTurnstileFailed  HTTPErrorCode = "turnstile_failed"
	HTTPErrorCodeNotFound         HTTPErrorCode = "not_found"
	HTTPErrorCodeMethodNotAllowed HTTPErrorCode = "method_not_allowed"
	HTTPErrorCodeUpgradeRequired  HTTPErrorCode = "upgrade_required"
	HTTPErrorCodeRateLimited      HTTPErrorCode = "rate_limited"
	HTTPErrorCodeInternalError    HTTPErrorCode = "internal_error"
	HTTPErrorCodeBudgetExhausted  HTTPErrorCode = "budget_exhausted"
	HTTPErrorCodeSessionsDisabled HTTPErrorCode = "sessions_disabled"
	HTTPErrorCodeLocked           HTTPErrorCode = "locked"
)
