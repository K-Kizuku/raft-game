package host

import (
	"encoding/json"
	"fmt"
	"strconv"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
)

// SchemaVersion は保存の形の版（meta.schemaVersion）。違う版の保存は unsupported_version で開かない。
// 保存は 1 セッションの間だけなので、移行（migration）は作らない。
const SchemaVersion = 1

// journal の status の値。
const (
	StatusIntent  = "intent"
	StatusApplied = "applied"
)

// bundles の state の値（persistence-and-recovery.md §2）。
const (
	BundleHeld     = "held"
	BundlePrepared = "prepared"
	BundlePending  = "pending"
	BundleGone     = "gone"
)

// bundles の place の値（persistence-and-recovery.md §2）。
const (
	PlacePort      = "port"
	PlaceRelay     = "relay"
	PlaceCargo     = "cargo"
	PlaceDelivered = "delivered"
	PlaceDropped   = "dropped"
	PlaceMoved     = "moved"
)

// Meta は meta の表（k → v の TEXT）。JournalRows・StoredBytes は上限（§6）を数えるための値で、
// トランザクションごとに更新する（毎回すべてを数え直さない）。
type Meta struct {
	SchemaVersion int
	SessionID     string
	Actor         dtnproto.ActorRef
	Epoch         uint64 // Actor の世代。Container を起動するたびに 1 増やす（DTN-10）
	MaxCoordEpoch uint64 // 受けた最大の coordEpoch
	InitPayload   json.RawMessage
	CreatedAt     string // RFC 3339（UTC）
	JournalRows   int64  // journal の行の数
	StoredBytes   int64  // bundles の bytes ＋ journal の payload ＋ response の長さの和
}

// meta の k。
const (
	MetaSchemaVersion = "schemaVersion"
	MetaSessionID     = "sessionId"
	MetaActor         = "actor"
	MetaEpoch         = "epoch"
	MetaMaxCoordEpoch = "maxCoordEpoch"
	MetaInitPayload   = "initPayload"
	MetaCreatedAt     = "createdAt"
	MetaJournalRows   = "journalRows"
	MetaStoredBytes   = "storedBytes"
)

// KV は meta の表の行（k → v）を返す。InitPayload が空なら initPayload の行を作らない。
func (m Meta) KV() map[string]string {
	kv := map[string]string{
		MetaSchemaVersion: strconv.Itoa(m.SchemaVersion),
		MetaSessionID:     m.SessionID,
		MetaActor:         string(m.Actor),
		MetaEpoch:         strconv.FormatUint(m.Epoch, 10),
		MetaMaxCoordEpoch: strconv.FormatUint(m.MaxCoordEpoch, 10),
		MetaCreatedAt:     m.CreatedAt,
		MetaJournalRows:   strconv.FormatInt(m.JournalRows, 10),
		MetaStoredBytes:   strconv.FormatInt(m.StoredBytes, 10),
	}
	if len(m.InitPayload) > 0 {
		kv[MetaInitPayload] = string(m.InitPayload)
	}
	return kv
}

// MetaFromKV は meta の表の行から Meta を作る。schemaVersion が無いか読めなければ誤り。
// schemaVersion が違うときは、それ以外を読まずに SchemaVersion だけを入れて返す（呼び出し側が断る）。
func MetaFromKV(kv map[string]string) (Meta, error) {
	var m Meta
	v, ok := kv[MetaSchemaVersion]
	if !ok {
		return m, fmt.Errorf("meta: no %s", MetaSchemaVersion)
	}
	sv, err := strconv.Atoi(v)
	if err != nil {
		return m, fmt.Errorf("meta: %s: %w", MetaSchemaVersion, err)
	}
	m.SchemaVersion = sv
	if sv != SchemaVersion {
		return m, nil
	}
	m.SessionID = kv[MetaSessionID]
	m.Actor = dtnproto.ActorRef(kv[MetaActor])
	m.CreatedAt = kv[MetaCreatedAt]
	if p, ok := kv[MetaInitPayload]; ok {
		m.InitPayload = json.RawMessage(p)
	}
	for _, f := range []struct {
		k string
		u *uint64
		i *int64
	}{
		{MetaEpoch, &m.Epoch, nil},
		{MetaMaxCoordEpoch, &m.MaxCoordEpoch, nil},
		{MetaJournalRows, nil, &m.JournalRows},
		{MetaStoredBytes, nil, &m.StoredBytes},
	} {
		s, ok := kv[f.k]
		if !ok {
			return m, fmt.Errorf("meta: no %s", f.k)
		}
		if f.u != nil {
			*f.u, err = strconv.ParseUint(s, 10, 64)
		} else {
			*f.i, err = strconv.ParseInt(s, 10, 64)
		}
		if err != nil {
			return m, fmt.Errorf("meta: %s: %w", f.k, err)
		}
	}
	return m, nil
}

// JournalRow は journal の 1 行。Payload は要求の payload の JSON を受けたとおりに持つ（Bundle の bytes を
// 含む。復元の再生はこれだけを使う）。Response・RespDigest は applied の行だけ（intent の行は nil・""）。
// Response は応答の JSON で、outputs[].bundle を除いたもの。
type JournalRow struct {
	EventSeq   uint64
	Kind       dtnproto.Kind
	ReqDigest  string
	Payload    json.RawMessage
	Status     string
	Response   json.RawMessage
	RespDigest string
	Epoch      uint64
}

// storedBytes は行が保存の合計に足す長さ（payload ＋ response）。
func (r JournalRow) storedBytes() int64 { return int64(len(r.Payload) + len(r.Response)) }

// BundleRow は bundles の 1 行（「今その Actor が持っている bytes」）。Bundle が nil は NULL
// （collected の後など。行は tombstone として残す）。TransferID が "" は NULL。
type BundleRow struct {
	LetterID   uint64
	Bundle     []byte
	Digest     string
	Size       int64
	State      string
	Place      string
	TransferID string
	UpdatedSeq uint64
}

// TransferRow は transfers の 1 行（DTN-08 が使う）。LetterIDs・Digests は JSON の配列、Ack は JSON か nil（NULL）。
type TransferRow struct {
	TransferID string
	Role       string // 'source' | 'dest'
	Peer       string
	ContactID  string
	LetterIDs  json.RawMessage
	Digests    json.RawMessage
	State      string // 'prepared' | 'pending' | 'activated' | 'collected' | 'aborted'
	Ack        json.RawMessage
}

// ContactRow は contacts の 1 行（DTN-09 が使う）。Body は Contact の JSON。
type ContactRow struct {
	ContactID string
	Body      json.RawMessage
	State     string
}

// Snapshot は保存のすべての行（主キーの順）。契約テストと復元（DTN-10）が読む。
type Snapshot struct {
	Meta      map[string]string
	Journal   []JournalRow
	Bundles   []BundleRow
	Transfers []TransferRow
	Contacts  []ContactRow
}
