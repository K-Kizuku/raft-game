package protocol

import (
	"encoding/json"
	"regexp"
	"strconv"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// payload は種類ごとの payload の型。厳密に読んだ後に validate で中身を確かめる。
type payload interface {
	validate(c *vctx) *Error
}

// vctx は payload の検証の文脈。
type vctx struct {
	lim   Limits
	actor ActorRef
	kind  Kind
}

var (
	digestRe    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	stageIDRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`) // server/internal/stage の stageIDRe と同じ
	maxIDLength = 128                                             // sessionId・transferId・contactId などの文字列の長さの上限
)

// DecodePayload は kind の payload raw を、宛先 actor・上限 lim で厳密に読んで確かめ、型付きの値
// （*InitPayload など。表の型へのポインタ）を返す。
func DecodePayload(kind Kind, raw json.RawMessage, actor ActorRef, lim Limits) (any, *Error) {
	ki, ok := LookupKind(kind)
	if !ok {
		return nil, Errorf(CodeInvalid, "unknown kind %q", kind)
	}
	jv, err := parseJSON(raw)
	if err != nil {
		return nil, err
	}
	return decodePayload(ki, jv, raw, actor, lim)
}

func decodePayload(ki KindInfo, jv jvalue, raw []byte, actor ActorRef, lim Limits) (any, *Error) {
	p := ki.newPayload()
	if err := decodeStrict(jv, raw, p, "payload"); err != nil {
		return nil, err
	}
	if err := p.validate(&vctx{lim: lim, actor: actor, kind: ki.Kind}); err != nil {
		return nil, err
	}
	return p, nil
}

// --- §3.1 ---

// PlannedRequest は依頼の計画 1 件（game.Request に JSON の名前を付けた写し）。
type PlannedRequest struct {
	ID        string `json:"id"`
	Origin    uint64 `json:"origin"`
	Kind      string `json:"kind"`
	Resource  string `json:"resource"`
	Critical  bool   `json:"critical"`
	CreatedAt int64  `json:"createdAt"`
	Deadline  int64  `json:"deadline"`
}

// InitPayload は init の payload。Plan は、その島が発生元の依頼の計画（船には無い）。
type InitPayload struct {
	Stage     string           `json:"stage"`
	StageHash string           `json:"stageHash"`
	Seed      uint64           `json:"seed"`
	Actor     ActorRef         `json:"actor"`
	Plan      []PlannedRequest `json:"plan,omitempty"`
}

func (p *InitPayload) validate(c *vctx) *Error {
	switch {
	case !stageIDRe.MatchString(p.Stage):
		return Errorf(CodeInvalid, "payload.stage: invalid %q", p.Stage)
	case !digestRe.MatchString(p.StageHash):
		return Errorf(CodeInvalid, "payload.stageHash: must be sha256:<64 lowercase hex>")
	case p.Actor != c.actor:
		return Errorf(CodeInvalid, "payload.actor %q differs from envelope actor %q", p.Actor, c.actor)
	}
	island, isIsland := c.actor.IslandID()
	if !isIsland && len(p.Plan) > 0 {
		return Errorf(CodeInvalid, "payload.plan: only islands have a plan")
	}
	for i, r := range p.Plan {
		switch {
		case r.ID == "" || len(r.ID) > maxIDLength:
			return Errorf(CodeInvalid, "payload.plan[%d].id: invalid", i)
		case r.Origin != island:
			return Errorf(CodeInvalid, "payload.plan[%d].origin: must be %d", i, island)
		case r.CreatedAt < 0 || r.Deadline < r.CreatedAt:
			return Errorf(CodeInvalid, "payload.plan[%d]: invalid times", i)
		}
	}
	return nil
}

// AdvancePayload は advance の payload。
type AdvancePayload struct {
	Horizon Mark `json:"horizon"`
}

func (p *AdvancePayload) validate(*vctx) *Error {
	if p.Horizon.T < 0 || p.Horizon.C > MaxMarkClass {
		return Errorf(CodeInvalid, "payload.horizon: t must be >= 0 and c <= %d", MaxMarkClass)
	}
	return nil
}

// Letter は手紙（actor-protocol.md §3.1）。Digest は Bundle の bytes の Digest。
type Letter struct {
	LetterID  ferry.LetterID `json:"letterId"`
	CreatedAt int64          `json:"createdAt"`
	Digest    string         `json:"digest"`
	Bundle    []byte         `json:"bundle"`
}

func (l *Letter) validate(c *vctx, path string, t int64) *Error {
	switch {
	case l.LetterID == 0:
		return Errorf(CodeInvalid, "%s.letterId: must be >= 1", path)
	case l.CreatedAt < 0 || l.CreatedAt > t:
		return Errorf(CodeInvalid, "%s.createdAt: must be in [0, t]", path)
	}
	return checkBundle(c, path, l.Bundle, l.Digest)
}

// checkBundle は Bundle の大きさと digest を確かめる。
func checkBundle(c *vctx, path string, b []byte, digest string) *Error {
	switch {
	case len(b) == 0:
		return Errorf(CodeInvalid, "%s.bundle: empty", path)
	case len(b) > c.lim.MaxBundleBytes:
		return Errorf(CodeTooLarge, "%s.bundle: %d bytes exceeds %d", path, len(b), c.lim.MaxBundleBytes)
	case !digestRe.MatchString(digest):
		return Errorf(CodeInvalid, "%s.digest: must be sha256:<64 lowercase hex>", path)
	case Digest(b) != digest:
		return Errorf(CodeInvalid, "%s.digest: does not match the bundle", path)
	}
	return nil
}

// checkCount は手紙の数（1 以上 MaxLetters 以下）を確かめる。
func checkCount(c *vctx, path string, n int) *Error {
	switch {
	case n == 0:
		return Errorf(CodeInvalid, "%s: empty", path)
	case n > c.lim.MaxLetters:
		return Errorf(CodeTooLarge, "%s: %d letters exceeds %d", path, n, c.lim.MaxLetters)
	}
	return nil
}

// checkIDs は手紙の ID の列（1 以上・重複なし）を確かめる。
func checkIDs(c *vctx, path string, ids []ferry.LetterID) *Error {
	if err := checkCount(c, path, len(ids)); err != nil {
		return err
	}
	seen := make(map[ferry.LetterID]bool, len(ids))
	for _, id := range ids {
		if id == 0 {
			return Errorf(CodeInvalid, "%s: letter id must be >= 1", path)
		}
		if seen[id] {
			return Errorf(CodeInvalid, "%s: duplicate letter id %d", path, id)
		}
		seen[id] = true
	}
	return nil
}

func checkT(t int64) *Error {
	if t < 0 {
		return Errorf(CodeInvalid, "payload.t: must be >= 0")
	}
	return nil
}

// LettersPayload は deliver・cargo.add の payload。手紙はこの順に処理する。
type LettersPayload struct {
	T       int64    `json:"t"`
	Letters []Letter `json:"letters"`
}

func (p *LettersPayload) validate(c *vctx) *Error {
	if err := checkT(p.T); err != nil {
		return err
	}
	if err := checkCount(c, "payload.letters", len(p.Letters)); err != nil {
		return err
	}
	ids := make([]ferry.LetterID, len(p.Letters))
	for i := range p.Letters {
		ids[i] = p.Letters[i].LetterID
	}
	for i := range p.Letters {
		if err := p.Letters[i].validate(c, "payload.letters["+strconv.Itoa(i)+"]", p.T); err != nil {
			return err
		}
	}
	return checkIDs(c, "payload.letters", ids)
}

// 下ろした手紙の置き場所（UnloadLetter.Place）。
const (
	PlacePort  = "port"  // 自島の手紙。港に戻す
	PlaceRelay = "relay" // 他島の手紙。倉庫に入れる
)

// UnloadLetter は unload の手紙 1 通。
type UnloadLetter struct {
	Letter
	Place string `json:"place"`
}

// UnloadPayload は unload の payload。手紙は ID の昇順に並べる。
type UnloadPayload struct {
	T       int64          `json:"t"`
	Letters []UnloadLetter `json:"letters"`
}

func (p *UnloadPayload) validate(c *vctx) *Error {
	if err := checkT(p.T); err != nil {
		return err
	}
	if err := checkCount(c, "payload.letters", len(p.Letters)); err != nil {
		return err
	}
	for i := range p.Letters {
		l := &p.Letters[i]
		path := "payload.letters[" + strconv.Itoa(i) + "]"
		if l.Place != PlacePort && l.Place != PlaceRelay {
			return Errorf(CodeInvalid, "%s.place: must be %q or %q", path, PlacePort, PlaceRelay)
		}
		if err := l.Letter.validate(c, path, p.T); err != nil {
			return err
		}
		if i > 0 && l.LetterID <= p.Letters[i-1].LetterID {
			return Errorf(CodeInvalid, "payload.letters: letter ids must be strictly ascending")
		}
	}
	return nil
}

// LetterIDsPayload は port.remove・relay.remove・cargo.remove の payload。
type LetterIDsPayload struct {
	LetterIDs []ferry.LetterID `json:"letterIds"`
}

func (p *LetterIDsPayload) validate(c *vctx) *Error {
	return checkIDs(c, "payload.letterIds", p.LetterIDs)
}

// ArrivePayload は arrive の payload。
type ArrivePayload struct {
	T      int64  `json:"t"`
	Island uint64 `json:"island"`
}

func (p *ArrivePayload) validate(*vctx) *Error {
	if p.Island == 0 {
		return Errorf(CodeInvalid, "payload.island: must be >= 1")
	}
	return checkT(p.T)
}

// DepartPayload は depart の payload。
type DepartPayload struct {
	T        int64  `json:"t"`
	To       uint64 `json:"to"`
	ArriveAt int64  `json:"arriveAt"`
}

func (p *DepartPayload) validate(*vctx) *Error {
	switch {
	case p.To == 0:
		return Errorf(CodeInvalid, "payload.to: must be >= 1")
	case p.ArriveAt < p.T:
		return Errorf(CodeInvalid, "payload.arriveAt: must be >= t")
	}
	return checkT(p.T)
}

// ExportPayload は bundle.export の payload。
type ExportPayload struct {
	T         int64            `json:"t"`
	LetterIDs []ferry.LetterID `json:"letterIds"`
}

func (p *ExportPayload) validate(c *vctx) *Error {
	if err := checkT(p.T); err != nil {
		return err
	}
	return checkIDs(c, "payload.letterIds", p.LetterIDs)
}

// EmptyPayload は inspect・shutdown・host.status の payload（{}）。
type EmptyPayload struct{}

func (*EmptyPayload) validate(*vctx) *Error { return nil }

// --- §3.2（型と形の検証だけ。処理は DTN-07〜09） ---

// Contact は 1 回の寄港（handoff-and-contact.md §2.1）。署名の確認は Host（DTN-09）。
type Contact struct {
	ContactID   string `json:"contactId"`
	SessionID   string `json:"sessionId"`
	Island      uint64 `json:"island"`
	Ferry       string `json:"ferry"`
	IslandEpoch uint64 `json:"islandEpoch"`
	FerryEpoch  uint64 `json:"ferryEpoch"`
	OpenedAt    int64  `json:"openedAt"`
	Sig         string `json:"sig"`
}

func (k *Contact) validate(path string) *Error {
	switch {
	case !validID(k.ContactID):
		return Errorf(CodeInvalid, "%s.contactId: invalid", path)
	case !validID(k.SessionID):
		return Errorf(CodeInvalid, "%s.sessionId: invalid", path)
	case k.Island == 0:
		return Errorf(CodeInvalid, "%s.island: must be >= 1", path)
	case !ferryIDRe.MatchString(k.Ferry):
		return Errorf(CodeInvalid, "%s.ferry: invalid", path)
	case k.OpenedAt < 0:
		return Errorf(CodeInvalid, "%s.openedAt: must be >= 0", path)
	case k.Sig == "" || len(k.Sig) > 256:
		return Errorf(CodeInvalid, "%s.sig: invalid", path)
	}
	return nil
}

func validID(s string) bool { return s != "" && len(s) <= maxIDLength }

// ContactPayload は contact.open・contact.close の payload。
type ContactPayload struct {
	Contact Contact `json:"contact"`
}

func (p *ContactPayload) validate(*vctx) *Error { return p.Contact.validate("payload.contact") }

// TransferPreparePayload は transfer.prepare の payload。T は送り出す仮想時刻。
type TransferPreparePayload struct {
	TransferID string           `json:"transferId"`
	T          int64            `json:"t"`
	To         ActorRef         `json:"to"`
	Contact    Contact          `json:"contact"`
	LetterIDs  []ferry.LetterID `json:"letterIds"`
}

func (p *TransferPreparePayload) validate(c *vctx) *Error {
	switch {
	case !validID(p.TransferID):
		return Errorf(CodeInvalid, "payload.transferId: invalid")
	case p.To.Role()&roleActors == 0:
		return Errorf(CodeInvalid, "payload.to: must be an island or a ferry")
	}
	if err := checkT(p.T); err != nil {
		return err
	}
	if err := p.Contact.validate("payload.contact"); err != nil {
		return err
	}
	return checkIDs(c, "payload.letterIds", p.LetterIDs)
}

// TransferBundle は transfer.accept の Bundle 1 つ。
type TransferBundle struct {
	LetterID ferry.LetterID `json:"letterId"`
	Bundle   []byte         `json:"bundle"`
	Digest   string         `json:"digest"`
}

// TransferAcceptPayload は transfer.accept の payload（送り元の Host → 受け取る Host）。
type TransferAcceptPayload struct {
	TransferID string           `json:"transferId"`
	From       ActorRef         `json:"from"`
	Contact    Contact          `json:"contact"`
	Bundles    []TransferBundle `json:"bundles"`
}

func (p *TransferAcceptPayload) validate(c *vctx) *Error {
	switch {
	case !validID(p.TransferID):
		return Errorf(CodeInvalid, "payload.transferId: invalid")
	case p.From.Role()&roleActors == 0:
		return Errorf(CodeInvalid, "payload.from: must be an island or a ferry")
	}
	if err := p.Contact.validate("payload.contact"); err != nil {
		return err
	}
	if err := checkCount(c, "payload.bundles", len(p.Bundles)); err != nil {
		return err
	}
	ids := make([]ferry.LetterID, len(p.Bundles))
	for i := range p.Bundles {
		b := &p.Bundles[i]
		ids[i] = b.LetterID
		if err := checkBundle(c, "payload.bundles["+strconv.Itoa(i)+"]", b.Bundle, b.Digest); err != nil {
			return err
		}
	}
	return checkIDs(c, "payload.bundles", ids)
}

// Then は transfer.activate・transfer.gc の後に Container に送る要求。
type Then struct {
	Kind    Kind            `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// TransferThenPayload は transfer.activate・transfer.gc の payload。
//
// transfer.activate の then（deliver・cargo.add・unload）の手紙は bytes を持たない（ThenLetter・
// ThenUnloadLetter）。受け取る Host が pending の行の bytes を付けて Container に送る（handoff-and-contact.md
// §3.4）。だから Session Front と Coordinator は bytes を持たない。transfer.gc の then（port.remove・
// relay.remove・cargo.remove）は letterIds だけなので、Container に送る形のまま。
type TransferThenPayload struct {
	TransferID string `json:"transferId"`
	Then       Then   `json:"then"`
}

// activateThen は transfer.activate の then にできる種類、gcThen は transfer.gc の then にできる種類。
var (
	activateThen = []Kind{KindCargoAdd, KindUnload, KindDeliver}
	gcThen       = []Kind{KindPortRemove, KindRelayRemove, KindCargoRemove}
)

func (p *TransferThenPayload) validate(c *vctx) *Error {
	if !validID(p.TransferID) {
		return Errorf(CodeInvalid, "payload.transferId: invalid")
	}
	ki, ok := LookupKind(p.Then.Kind)
	switch {
	case !ok:
		return Errorf(CodeInvalid, "payload.then.kind: unknown kind %q", p.Then.Kind)
	case ki.HostOnly || !ki.Journaled || ki.Kind == KindInit || ki.Kind == KindShutdown:
		return Errorf(CodeInvalid, "payload.then.kind: %q cannot follow a transfer", p.Then.Kind)
	case !ki.Accepts(c.actor.Role()):
		return Errorf(CodeInvalid, "payload.then.kind: %q is not for %q", p.Then.Kind, c.actor)
	case c.kind == KindTransferActivate && !containsKind(activateThen, ki.Kind),
		c.kind == KindTransferGC && !containsKind(gcThen, ki.Kind):
		return Errorf(CodeInvalid, "payload.then.kind: %q cannot follow %q", p.Then.Kind, c.kind)
	}
	jv, err := parseJSON(p.Then.Payload)
	if err != nil {
		return err
	}
	if c.kind == KindTransferActivate {
		_, err = DecodeThenPayload(ki.Kind, p.Then.Payload, c.actor, c.lim)
		return err
	}
	_, err = decodePayload(ki, jv, p.Then.Payload, c.actor, c.lim)
	return err
}

func containsKind(ks []Kind, k Kind) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}

// ThenLetter は transfer.activate の then の手紙 1 通（bytes を除いた Letter）。Digest は送り元の Host が
// prepare で保存した bytes の digest（ACK の digests の値）。
type ThenLetter struct {
	LetterID  ferry.LetterID `json:"letterId"`
	CreatedAt int64          `json:"createdAt"`
	Digest    string         `json:"digest"`
}

func (l *ThenLetter) validate(path string, t int64) *Error {
	switch {
	case l.LetterID == 0:
		return Errorf(CodeInvalid, "%s.letterId: must be >= 1", path)
	case l.CreatedAt < 0 || l.CreatedAt > t:
		return Errorf(CodeInvalid, "%s.createdAt: must be in [0, t]", path)
	case !digestRe.MatchString(l.Digest):
		return Errorf(CodeInvalid, "%s.digest: must be sha256:<64 lowercase hex>", path)
	}
	return nil
}

// ThenUnloadLetter は transfer.activate の then（unload）の手紙 1 通。
type ThenUnloadLetter struct {
	ThenLetter
	Place string `json:"place"`
}

// ThenLettersPayload は transfer.activate の then（deliver・cargo.add）の payload。
type ThenLettersPayload struct {
	T       int64        `json:"t"`
	Letters []ThenLetter `json:"letters"`
}

func (p *ThenLettersPayload) validate(c *vctx) *Error {
	if err := checkT(p.T); err != nil {
		return err
	}
	if err := checkCount(c, "payload.then.payload.letters", len(p.Letters)); err != nil {
		return err
	}
	ids := make([]ferry.LetterID, len(p.Letters))
	for i := range p.Letters {
		ids[i] = p.Letters[i].LetterID
		if err := p.Letters[i].validate("payload.then.payload.letters["+strconv.Itoa(i)+"]", p.T); err != nil {
			return err
		}
	}
	return checkIDs(c, "payload.then.payload.letters", ids)
}

// ThenUnloadPayload は transfer.activate の then（unload）の payload。手紙は ID の昇順。
type ThenUnloadPayload struct {
	T       int64              `json:"t"`
	Letters []ThenUnloadLetter `json:"letters"`
}

func (p *ThenUnloadPayload) validate(c *vctx) *Error {
	if err := checkT(p.T); err != nil {
		return err
	}
	if err := checkCount(c, "payload.then.payload.letters", len(p.Letters)); err != nil {
		return err
	}
	for i := range p.Letters {
		l := &p.Letters[i]
		path := "payload.then.payload.letters[" + strconv.Itoa(i) + "]"
		if l.Place != PlacePort && l.Place != PlaceRelay {
			return Errorf(CodeInvalid, "%s.place: must be %q or %q", path, PlacePort, PlaceRelay)
		}
		if err := l.ThenLetter.validate(path, p.T); err != nil {
			return err
		}
		if i > 0 && l.LetterID <= p.Letters[i-1].LetterID {
			return Errorf(CodeInvalid, "payload.then.payload.letters: letter ids must be strictly ascending")
		}
	}
	return nil
}

// DecodeThenPayload は transfer.activate の then の payload raw（kind は deliver・cargo.add・unload）を厳密に
// 読んで確かめ、*ThenLettersPayload か *ThenUnloadPayload を返す。
func DecodeThenPayload(kind Kind, raw json.RawMessage, actor ActorRef, lim Limits) (any, *Error) {
	var p payload
	switch kind {
	case KindDeliver, KindCargoAdd:
		p = new(ThenLettersPayload)
	case KindUnload:
		p = new(ThenUnloadPayload)
	default:
		return nil, Errorf(CodeInvalid, "payload.then.kind: %q cannot follow %q", kind, KindTransferActivate)
	}
	jv, err := parseJSON(raw)
	if err != nil {
		return nil, err
	}
	if err := decodeStrict(jv, raw, p, "payload.then.payload"); err != nil {
		return nil, err
	}
	if err := p.validate(&vctx{lim: lim, actor: actor, kind: kind}); err != nil {
		return nil, err
	}
	return p, nil
}

// TransferIDPayload は transfer.abort・transfer.status の payload。
type TransferIDPayload struct {
	TransferID string `json:"transferId"`
}

func (p *TransferIDPayload) validate(*vctx) *Error {
	if !validID(p.TransferID) {
		return Errorf(CodeInvalid, "payload.transferId: invalid")
	}
	return nil
}
