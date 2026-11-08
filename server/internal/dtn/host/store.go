package host

import "errors"

// Store は 1 Actor の保存の境界（persistence-and-recovery.md §2）。Go では sqlitestore、Cloudflare では
// ActorHost DO の ctx.storage.sql（DTN-12）が同じ形を持つ。
//
// Host は Store を 1 つの goroutine からだけ使う（Actor ごとに直列）。Update の中で View・Update を呼ばない。
type Store interface {
	// Update は f を 1 つの書き込みのトランザクションで実行する。f が nil を返したら確定（commit）し、
	// 確定が終わってから戻る。f が誤りを返すか確定に失敗したら、ロールバックして誤りを返す。
	Update(f func(Tx) error) error
	// View は f を読み取りだけのトランザクションで実行する。
	View(f func(Tx) error) error
	// Destroy は保存をすべて消す（shutdown。sqlitestore はファイルを消す）。2 回目は何もしないで nil。
	// Destroy の後の Update・View は ErrDestroyed。
	Destroy() error
	// Close は保存を閉じる（消さない）。2 回目は何もしない。
	Close() error
}

// Tx はトランザクションの中の操作。読み取りの結果の bool は行があるか。
type Tx interface {
	// Meta は meta を読む。空の保存なら false。
	Meta() (Meta, bool, error)
	// PutMeta は meta をすべて書く。
	PutMeta(m Meta) error

	// Journal は event_seq の行を読む。
	Journal(seq uint64) (JournalRow, bool, error)
	// JournalBefore は event_seq が seq より小さい行のうち、最も大きいものを読む。
	JournalBefore(seq uint64) (JournalRow, bool, error)
	// LastJournal は event_seq が最も大きい行を読む。
	LastJournal() (JournalRow, bool, error)
	// LastApplied は status が applied の行のうち、event_seq が最も大きいものを読む。
	LastApplied() (JournalRow, bool, error)
	// Journals は journal のすべての行を event_seq の順に返す（復元の再生。DTN-10）。
	Journals() ([]JournalRow, error)
	// PutJournal は行を書く（同じ event_seq の行は置き換える）。
	PutJournal(r JournalRow) error
	// DeleteJournal は event_seq の行を消す（advance のまとめ）。
	DeleteJournal(seq uint64) error

	// Bundle は letter_id の行を読む。
	Bundle(letterID uint64) (BundleRow, bool, error)
	// PutBundle は行を書く（同じ letter_id の行は置き換える）。
	PutBundle(r BundleRow) error
	// DeleteBundle は letter_id の行を消す（transfer.abort で受け取り側の pending の行を消す。DTN-08）。
	// 行が無ければ何もしない。
	DeleteBundle(letterID uint64) error

	// Transfer・PutTransfer は DTN-08、Contact・PutContact は DTN-09 が使う。
	Transfer(id string) (TransferRow, bool, error)
	PutTransfer(r TransferRow) error
	Contact(id string) (ContactRow, bool, error)
	PutContact(r ContactRow) error

	// Snapshot はすべての行を主キーの順に返す。
	Snapshot() (Snapshot, error)
}

// ErrDestroyed は Destroy の後の Store の操作の誤り。
var ErrDestroyed = errors.New("host: store destroyed")

// ErrClosed は Close の後の操作の誤り。
var ErrClosed = errors.New("host: closed")
