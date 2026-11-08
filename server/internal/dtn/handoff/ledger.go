package handoff

import (
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// Stage は Coordinator の台帳での transfer の段階（handoff-and-contact.md §3.1）。
type Stage uint8

// 段階。許す遷移は Proposed → Persisted → Committed → Activated → Collected と、Proposed・Persisted → Aborted だけ。
const (
	// Proposed は Coordinator が journal に intent を書いた（送り元の Host が prepare を保存するのを待つ）。
	Proposed Stage = iota + 1
	// Persisted は受け取る Host が bytes を保存して ACK した（CAS を待つ）。
	Persisted
	// Committed は論理的な持ち主が受け取り側になった（取り消さない）。
	Committed
	// Activated は受け取り側の Actor が then を適用した。
	Activated
	// Collected は送り元の Actor が then を適用し、送り元の写しが不要になった。
	Collected
	// Aborted は committed の前に取り消した。
	Aborted
)

var stageNames = [...]string{"", "proposed", "persisted", "committed", "activated", "collected", "aborted"}

func (s Stage) String() string {
	if int(s) < len(stageNames) && s != 0 {
		return stageNames[s]
	}
	return fmt.Sprintf("stage(%d)", uint8(s))
}

// Terminal は s が終わりの段階（Collected・Aborted）か。
func (s Stage) Terminal() bool { return s == Collected || s == Aborted }

// next は s から進める段階（Aborted を除く）。
func (s Stage) next() Stage {
	switch s {
	case Proposed, Persisted, Committed, Activated:
		return s + 1
	}
	return 0
}

// Transfer は 1 回の引き渡し（送り元 → 受け取り）。
type Transfer struct {
	// ID は x<eventSeq>（transfer.prepare を送る dispatch の番号）。
	ID string
	// From は送り元の置き場所（手紙の今の持ち主）。To は受け取る Actor の置き場所。
	From, To Place
	// ContactID は寄港の ID（k<eventSeq>）。
	ContactID string
	// Letters は手紙の ID（transfer.prepare の letterIds の順。ACK の digests もこの順）。
	Letters []ferry.LetterID
	// Final は手紙ごとの Commit の後の持ち主（Letters と同じ長さ）。nil なら全部 To。
	// 下ろす操作（自島の手紙は港・他島の手紙は倉庫）と到着の配達（delivered）で使う（§3.2）。
	// 各値は To と同じ Actor の置き場所か delivered。
	Final []Place
	// Digests は ACK の digests（Persisted で入る。Letters と同じ順）。
	Digests []string
	// Stage は今の段階。
	Stage Stage
}

// FinalOf は i 番目の手紙の Commit の後の持ち主。
func (t *Transfer) FinalOf(i int) Place {
	if t.Final == nil {
		return t.To
	}
	return t.Final[i]
}

func (t Transfer) clone() Transfer {
	t.Letters = slices.Clone(t.Letters)
	t.Final = slices.Clone(t.Final)
	t.Digests = slices.Clone(t.Digests)
	return t
}

// Ack は受け取る Host の永続 ACK（dtn/protocol の TransferAck と同じ中身）。
type Ack struct {
	TransferID string
	Digests    []string
}

// 誤り。Ledger のメソッドは、これらを %w で包んだ誤りを返す（errors.Is で見分ける）。誤りのときは何も変えない。
var (
	// ErrInvalid は引数の形の誤り（バグ）。
	ErrInvalid = errors.New("handoff: invalid")
	// ErrUnknownTransfer は台帳に無い transfer。
	ErrUnknownTransfer = errors.New("handoff: unknown transfer")
	// ErrDuplicate は同じ ID の transfer・同じ ID の手紙がすでにある。
	ErrDuplicate = errors.New("handoff: duplicate")
	// ErrTransition は許さない遷移（バグ）。
	ErrTransition = errors.New("handoff: transition not allowed")
	// ErrAborted は取り消した transfer への ACK（遅れて届いた ACK。呼び出し側は捨てる）。
	ErrAborted = errors.New("handoff: transfer aborted")
	// ErrCAS は Commit の CAS の失敗（持ち主が From でない・別の transfer が握っている）。
	ErrCAS = errors.New("handoff: owner changed")
	// ErrHeld は、Handoff の途中の手紙を Handoff の外で動かそうとした。
	ErrHeld = errors.New("handoff: letter is held by a transfer")
)

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Ledger は Coordinator の所有権の台帳（handoff-and-contact.md §1・§3）。手紙ごとの論理的な持ち主と、
// transfer の段階を持つ。ゼロ値は使えない（NewLedger で作る）。同時に 2 つ以上のメソッドを呼ばない。
//
// 手紙は、Commit から Collected まで、その transfer に握られる（holder）。握られている手紙は、別の transfer の
// Commit・Move で動かせない。Propose は手紙を握らない（同じ手紙の 2 つの transfer は、先に Commit した方だけが
// 通り、もう一方の Commit は ErrCAS）。
type Ledger struct {
	owner     map[ferry.LetterID]Place
	holder    map[ferry.LetterID]string
	transfers map[string]*Transfer
}

// NewLedger は空の台帳を作る。
func NewLedger() *Ledger {
	return &Ledger{owner: map[ferry.LetterID]Place{}, holder: map[ferry.LetterID]string{}, transfers: map[string]*Transfer{}}
}

// Owner は手紙 id の論理的な持ち主。台帳に無い手紙は ""。
func (l *Ledger) Owner(id ferry.LetterID) Place { return l.owner[id] }

// Holder は手紙 id を握っている transfer の ID（Commit から Collected まで）。無ければ ""。
func (l *Ledger) Holder(id ferry.LetterID) string { return l.holder[id] }

// Transfer は transfer id の写しを返す。
func (l *Ledger) Transfer(id string) (Transfer, bool) {
	t, ok := l.transfers[id]
	if !ok {
		return Transfer{}, false
	}
	return t.clone(), true
}

// Open は終わっていない（Collected・Aborted でない）transfer の写しを、ID の番号の順に返す
// （復元で Resume にかける一覧）。
func (l *Ledger) Open() []Transfer {
	var out []Transfer
	for _, t := range l.transfers {
		if !t.Stage.Terminal() {
			out = append(out, t.clone())
		}
	}
	slices.SortFunc(out, func(a, b Transfer) int {
		x, _ := ParseTransferID(a.ID)
		y, _ := ParseTransferID(b.ID)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	})
	return out
}

// Create は新しい手紙 id を at（島の港）の持ち主で記録する（letter.created）。
func (l *Ledger) Create(id ferry.LetterID, at Place) error {
	switch {
	case id == 0:
		return fmt.Errorf("%w: letter id 0", ErrInvalid)
	case !at.IsIsland() || at.Slot() != "port":
		return fmt.Errorf("%w: letter %d must be created at an island port, not %q", ErrInvalid, id, at)
	}
	if _, ok := l.owner[id]; ok {
		return fmt.Errorf("%w: letter %d already exists", ErrDuplicate, id)
	}
	l.owner[id] = at
	return nil
}

// Move は Handoff の外の持ち主の変化（港の規則での破棄 → dropped など）を CAS で記録する。今の持ち主が from で、
// transfer に握られていないときだけ to にする。
func (l *Ledger) Move(id ferry.LetterID, from, to Place) error {
	if !to.Valid() {
		return fmt.Errorf("%w: place %q", ErrInvalid, to)
	}
	cur, ok := l.owner[id]
	switch {
	case !ok:
		return fmt.Errorf("%w: letter %d is not in the ledger", ErrInvalid, id)
	case cur != from:
		return fmt.Errorf("%w: letter %d is at %q, not %q", ErrCAS, id, cur, from)
	case l.holder[id] != "":
		return fmt.Errorf("%w: letter %d by %s", ErrHeld, id, l.holder[id])
	}
	l.owner[id] = to
	return nil
}

// Propose は transfer t を Proposed で記録する（Coordinator が journal に intent を書いたとき）。持ち主は変えない。
// t.Stage は 0 か Proposed、t.Digests は空であること。
func (l *Ledger) Propose(t Transfer) error {
	if err := checkTransfer(&t); err != nil {
		return err
	}
	if _, ok := l.transfers[t.ID]; ok {
		return fmt.Errorf("%w: transfer %s", ErrDuplicate, t.ID)
	}
	c := t.clone()
	c.Stage = Proposed
	l.transfers[t.ID] = &c
	return nil
}

func checkTransfer(t *Transfer) error {
	if _, ok := ParseTransferID(t.ID); !ok {
		return fmt.Errorf("%w: transfer id %q is not x<eventSeq>", ErrInvalid, t.ID)
	}
	switch {
	case t.Stage != 0 && t.Stage != Proposed:
		return fmt.Errorf("%w: transfer %s: a new transfer is %s, not %s", ErrInvalid, t.ID, Proposed, t.Stage)
	case len(t.Digests) != 0:
		return fmt.Errorf("%w: transfer %s: digests come with the ACK", ErrInvalid, t.ID)
	case t.From.Actor() == "" || t.To.Actor() == "":
		return fmt.Errorf("%w: transfer %s: %q → %q must be between actors", ErrInvalid, t.ID, t.From, t.To)
	case t.From.IsIsland() == t.To.IsIsland():
		// 島から島・船から船は通らない（D-1）
		return fmt.Errorf("%w: transfer %s: %q → %q must be between an island and a ferry", ErrInvalid, t.ID, t.From, t.To)
	case t.ContactID == "":
		return fmt.Errorf("%w: transfer %s: empty contact", ErrInvalid, t.ID)
	case len(t.Letters) == 0:
		return fmt.Errorf("%w: transfer %s: no letters", ErrInvalid, t.ID)
	case t.Final != nil && len(t.Final) != len(t.Letters):
		return fmt.Errorf("%w: transfer %s: %d final places for %d letters", ErrInvalid, t.ID, len(t.Final), len(t.Letters))
	}
	for i, id := range t.Letters {
		if id == 0 {
			return fmt.Errorf("%w: transfer %s: letter id 0", ErrInvalid, t.ID)
		}
		if slices.Contains(t.Letters[:i], id) {
			return fmt.Errorf("%w: transfer %s: letter %d twice", ErrInvalid, t.ID, id)
		}
	}
	for i, p := range t.Final {
		if p != Delivered && (!p.Valid() || p.Actor() != t.To.Actor()) {
			return fmt.Errorf("%w: transfer %s: final place %q of letter %d is not on %s", ErrInvalid, t.ID, p, t.Letters[i], t.To.Actor())
		}
		if p == Delivered && !t.To.IsIsland() {
			return fmt.Errorf("%w: transfer %s: only islands take deliveries", ErrInvalid, t.ID)
		}
	}
	return nil
}

func (l *Ledger) get(id string) (*Transfer, error) {
	t, ok := l.transfers[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTransfer, id)
	}
	return t, nil
}

// advance は t を want に進める。今の段階から want へ進めないなら ErrTransition。
func advance(t *Transfer, want Stage) error {
	if t.Stage.next() != want {
		return fmt.Errorf("%w: transfer %s: %s → %s", ErrTransition, t.ID, t.Stage, want)
	}
	t.Stage = want
	return nil
}

// Persisted は受け取る Host の ACK を記録する（Proposed → Persisted）。取り消した transfer への ACK は
// ErrAborted（遅れて届いた ACK。呼び出し側は捨てる）。ACK の transferId・digests の数が合わなければ ErrInvalid。
func (l *Ledger) Persisted(id string, ack Ack) error {
	t, err := l.get(id)
	if err != nil {
		return err
	}
	if t.Stage == Aborted {
		return fmt.Errorf("%w: ack for %s", ErrAborted, id)
	}
	switch {
	case ack.TransferID != id:
		return fmt.Errorf("%w: ack for %q given to %s", ErrInvalid, ack.TransferID, id)
	case len(ack.Digests) != len(t.Letters):
		return fmt.Errorf("%w: transfer %s: ack has %d digests for %d letters", ErrInvalid, id, len(ack.Digests), len(t.Letters))
	}
	for _, d := range ack.Digests {
		if !digestRe.MatchString(d) {
			return fmt.Errorf("%w: transfer %s: digest %q", ErrInvalid, id, d)
		}
	}
	if err := advance(t, Persisted); err != nil {
		return err
	}
	t.Digests = slices.Clone(ack.Digests)
	return nil
}

// Commit は所有権を CAS で移す（Persisted → Committed）。すべての手紙の今の持ち主が From で、別の transfer に
// 握られていないときだけ、持ち主を To（Final があれば手紙ごとの値）にし、Collected まで握る。1 通でも違えば
// 何も変えずに ErrCAS。
func (l *Ledger) Commit(id string) error {
	t, err := l.get(id)
	if err != nil {
		return err
	}
	if t.Stage != Persisted {
		return fmt.Errorf("%w: transfer %s: %s → %s", ErrTransition, id, t.Stage, Committed)
	}
	for _, lid := range t.Letters {
		if cur := l.owner[lid]; cur != t.From {
			return fmt.Errorf("%w: transfer %s: letter %d is at %q, not %q", ErrCAS, id, lid, cur, t.From)
		}
		if h := l.holder[lid]; h != "" {
			return fmt.Errorf("%w: transfer %s: letter %d is held by %s", ErrCAS, id, lid, h)
		}
	}
	t.Stage = Committed
	for i, lid := range t.Letters {
		l.owner[lid] = t.FinalOf(i)
		l.holder[lid] = id
	}
	return nil
}

// Activated は受け取る Host が then を適用したことを記録する（Committed → Activated）。
func (l *Ledger) Activated(id string) error {
	t, err := l.get(id)
	if err != nil {
		return err
	}
	return advance(t, Activated)
}

// Collected は送り元の Host が then を適用したことを記録し、手紙を放す（Activated → Collected）。
func (l *Ledger) Collected(id string) error {
	t, err := l.get(id)
	if err != nil {
		return err
	}
	if err := advance(t, Collected); err != nil {
		return err
	}
	for _, lid := range t.Letters {
		if l.holder[lid] == id {
			delete(l.holder, lid)
		}
	}
	return nil
}

// Abort は transfer を取り消す（Proposed・Persisted → Aborted）。committed の後は ErrTransition（取り消さない）。
// 持ち主は変わっていないので、そのまま。
func (l *Ledger) Abort(id string) error {
	t, err := l.get(id)
	if err != nil {
		return err
	}
	if t.Stage != Proposed && t.Stage != Persisted {
		return fmt.Errorf("%w: transfer %s: %s → %s", ErrTransition, id, t.Stage, Aborted)
	}
	t.Stage = Aborted
	return nil
}
