package game

import (
	"fmt"

	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// Status は依頼の状態（プロトコル §5.3 の status、ゲームデザイン §4.3）。
type Status string

const (
	StatusReceived  Status = "received"   // 受付（発生した直後、route の前）
	StatusWaiting   Status = "waiting"    // 送付待ち（依頼書が港にある・代表が分からず待っている）
	StatusInTransit Status = "in_transit" // 輸送中（今の依頼書が船の上）
	StatusProposed  Status = "proposed"   // 審議中（Leader が Propose した）
	StatusCommitted Status = "committed"  // 共和国で確定（いずれかの島の台帳に適用された）
	StatusSucceeded Status = "succeeded"  // 確定（依頼元の台帳で ok）
	StatusRejected  Status = "rejected"   // 予約失敗（依頼元の台帳で rejected）
)

// Request は依頼の変わらない値（プロトコル §5.3 の id〜deadline）。時刻はゲーム内時間のミリ秒。
type Request struct {
	ID        string
	Origin    uint64
	Kind      string
	Resource  string
	Critical  bool
	CreatedAt int64
	Deadline  int64
}

// Command は台帳に提案するコマンド（DesignDoc §7.1）。V は ledger.Version、Amount は 0。
func (r Request) Command() ledger.Command {
	return ledger.Command{
		V:        ledger.Version,
		ReqID:    r.ID,
		Origin:   r.Origin,
		Kind:     r.Kind,
		Resource: r.Resource,
	}
}

// ReqID は "<stageID>-<n を 4 桁以上の 10 進で 0 埋め>" を返す（例: ("stage2", 7) → "stage2-0007"、10000 以上はそのまま）。
func ReqID(stageID string, n int) string {
	return fmt.Sprintf("%s-%04d", stageID, n)
}

// Result は依頼元の台帳の結果。
type Result struct {
	Status string // ledger.StatusOK か ledger.StatusRejected
	Reason string // rejected のときだけ（ledger の理由）
}

// Outcome は、最後の route の結果。
type Outcome uint8

const (
	OutcomeNone     Outcome = iota // まだ route していない
	OutcomeWaiting                 // 代表が分からない・提案が落とされた（次の RaftTick でやり直す）
	OutcomeLetter                  // 依頼書を港に置いた（最後に作った依頼書が「今の依頼書」）
	OutcomeProposed                // Leader が Propose した
)

// Track は状態の判定の材料（sim が観察して更新する）。
type Track struct {
	Outcome   Outcome
	OnFerry   bool    // Outcome == OutcomeLetter のとき、今の依頼書が船の上にある
	Committed bool    // いずれかの島の台帳に、この依頼の最初のエントリが適用された
	Result    *Result // 依頼元の台帳の結果。nil = まだ
}

// Status は状態を判定する（DesignDoc §8.2）。上の行ほど優先する。
//
//	Result が ok                     → succeeded
//	Result がそのほか                → rejected
//	Committed                        → committed
//	OutcomeProposed                  → proposed
//	OutcomeLetter で OnFerry         → in_transit
//	OutcomeLetter・OutcomeWaiting    → waiting
//	OutcomeNone                      → received
func (t Track) Status() Status {
	switch {
	case t.Result != nil && t.Result.Status == ledger.StatusOK:
		return StatusSucceeded
	case t.Result != nil:
		return StatusRejected
	case t.Committed:
		return StatusCommitted
	}
	switch t.Outcome {
	case OutcomeProposed:
		return StatusProposed
	case OutcomeLetter:
		if t.OnFerry {
			return StatusInTransit
		}
		return StatusWaiting
	case OutcomeWaiting:
		return StatusWaiting
	}
	return StatusReceived
}
