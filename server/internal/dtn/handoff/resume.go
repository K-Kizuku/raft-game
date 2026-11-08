package handoff

// HostTransferState は Actor Host の transfers の行の状態（transfer.status の state。dtn/protocol の
// Transfer* と同じ文字列）。
type HostTransferState string

// Host の状態。
const (
	HostNone      HostTransferState = "none"      // 行が無い
	HostPrepared  HostTransferState = "prepared"  // 送り元: bytes を prepared にした
	HostPending   HostTransferState = "pending"   // 受け取り: bytes を pending で保存した
	HostActivated HostTransferState = "activated" // 受け取り: then を適用した
	HostCollected HostTransferState = "collected" // 送り元: then を適用した
	HostAborted   HostTransferState = "aborted"   // 取り消した
)

// Action は、Coordinator の復元で、止まった transfer に次にすること。
type Action uint8

// 次の一手。
const (
	// ActionNone は何もしない（もう終わっている）。
	ActionNone Action = iota
	// ActionResendPrepare は transfer.prepare を同じ eventSeq で送り直す（送り元の Host は保存済みの prepared を
	// 見て accept を送り直す）。
	ActionResendPrepare
	// ActionCommit は受け取る Host の ACK（transfer.status の ack）で Persisted にして CAS する。
	ActionCommit
	// ActionActivate は transfer.activate を送る（dispatch を記録してあれば同じ eventSeq で送り直す。冪等）。
	ActionActivate
	// ActionGC は transfer.gc を送る（同上）。
	ActionGC
	// ActionAbort は両方の Host に transfer.abort を送り、台帳を Aborted にする（committed の前だけ）。
	ActionAbort
	// ActionFail は台帳と Host の状態が矛盾している（バグか、受け取り側の bytes が失われた）。ゲームを
	// dtn_internal で終える。
	ActionFail
)

var actionNames = [...]string{"none", "resend-prepare", "commit", "activate", "gc", "abort", "fail"}

func (a Action) String() string {
	if int(a) < len(actionNames) {
		return actionNames[a]
	}
	return "action(?)"
}

// anyState は resumeRule の「どの状態でもよい」。
const anyState HostTransferState = "*"

// resumeRule は Resume の表の 1 行。上から順に見て、最初に合った行の Action を返す。
type resumeRule struct {
	stage    Stage
	src, dst HostTransferState
	action   Action
	why      string // handoff-and-contact.md §3.3 の「止まった場所」
}

// resumeTable は handoff-and-contact.md §3.3 の表（止まった場所 → 回復）を、台帳の段階と両方の Host の状態で
// 引ける形にしたもの。台帳の段階は Coordinator の journal から復元したもので、Host より遅れていることがある
// （journal に書く前に止まった）。activate・gc は eventSeq の冪等なので、遅れていても送り直してよい。
var resumeTable = []resumeRule{
	// 取り消しの途中（committed の前だけ）。両方の Host を aborted にそろえる
	{Proposed, HostAborted, anyState, ActionAbort, "取り消しの途中"},
	{Proposed, anyState, HostAborted, ActionAbort, "取り消しの途中"},
	{Persisted, HostAborted, anyState, ActionAbort, "取り消しの途中"},
	{Persisted, anyState, HostAborted, ActionAbort, "取り消しの途中"},

	// §3.3 の 1 行目: transfer.prepare の保存の前
	{Proposed, HostNone, HostNone, ActionResendPrepare, "prepare の保存の前"},
	// §3.3 の 2 行目: 送り元の保存の後、accept の前
	{Proposed, HostPrepared, HostNone, ActionResendPrepare, "送り元の保存の後・accept の前"},
	// §3.3 の 3・4 行目: 受け取り側の保存の後（ACK が届く前・ACK の後で CAS の前）。受け取り側が保存済みなら CAS する
	{Proposed, HostPrepared, HostPending, ActionCommit, "受け取り側の保存の後・CAS の前"},
	{Persisted, HostPrepared, HostPending, ActionCommit, "ACK の後・CAS の前"},

	// §3.3 の 5 行目: CAS の後、activate の前（受け取り側が適用済みでも、同じ eventSeq の送り直しは冪等）
	{Committed, HostPrepared, HostPending, ActionActivate, "CAS の後・activate の前"},
	{Committed, HostPrepared, HostActivated, ActionActivate, "activate の後・台帳に記録する前"},
	{Committed, HostCollected, HostActivated, ActionActivate, "gc の後・台帳に記録する前"},

	// §3.3 の 6 行目: activate の後、gc の前
	{Activated, HostPrepared, HostActivated, ActionGC, "activate の後・gc の前"},
	{Activated, HostCollected, HostActivated, ActionGC, "gc の後・台帳に記録する前"},

	// 終わっている
	{Collected, HostCollected, HostActivated, ActionNone, "終わっている"},
	{Aborted, HostNone, HostNone, ActionNone, "取り消し済み"},
	{Aborted, HostAborted, HostNone, ActionNone, "取り消し済み"},
	{Aborted, HostNone, HostAborted, ActionNone, "取り消し済み"},
	{Aborted, HostAborted, HostAborted, ActionNone, "取り消し済み"},
	{Aborted, HostPrepared, anyState, ActionAbort, "取り消しの途中"},
	{Aborted, anyState, HostPending, ActionAbort, "取り消しの途中"},
}

// Resume は、Coordinator の復元で、段階 st の transfer の次の一手を、送り元の Host の状態 src と受け取る Host の
// 状態 dst（transfer.status）から返す（handoff-and-contact.md §3.3。呼ぶのは DTN-10）。表に無い組み合わせ
// （例: committed なのに受け取り側に bytes が無い）は ActionFail。
func Resume(st Stage, src, dst HostTransferState) Action {
	if r, ok := lookupResume(st, src, dst); ok {
		return r.action
	}
	return ActionFail
}

// Explain は Resume が合わせた表の行の「止まった場所」（ログ用）。表に無い組み合わせは "矛盾"。
func Explain(st Stage, src, dst HostTransferState) string {
	if r, ok := lookupResume(st, src, dst); ok {
		return r.why
	}
	return "矛盾"
}

func lookupResume(st Stage, src, dst HostTransferState) (resumeRule, bool) {
	for _, r := range resumeTable {
		if r.stage == st && (r.src == anyState || r.src == src) && (r.dst == anyState || r.dst == dst) {
			return r, true
		}
	}
	return resumeRule{}, false
}
