package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
)

// DefaultActorTimeout は Container の応答を待つ実時間の上限（persistence-and-recovery.md §3.1 の手順 4）。
const DefaultActorTimeout = 10 * time.Second

// Point は Hooks.At の止める位置（契約テストの crashAt）。
type Point string

// 止める位置。
const (
	// PointAfterIntent は journal に intent を確定した後・Container に送る前。
	PointAfterIntent Point = "afterIntent"
	// PointAfterActorReply は Container の応答を受けた後・applied を確定する前。
	PointAfterActorReply Point = "afterActorReply"
	// PointAfterCommit は applied を確定した後・応答を返す前。
	PointAfterCommit Point = "afterCommit"
	// PointAfterActorStop は shutdown で Container を止めた後・保存を消す前（Go の試験だけが使う）。
	PointAfterActorStop Point = "afterActorStop"
	// PointAfterPrepare は transfer.prepare で prepared を確定した後・受け取る Host に accept を送る前（DTN-08）。
	PointAfterPrepare Point = "afterPrepare"
	// PointAfterPeerReply は transfer.prepare で受け取る Host の ACK を受けた後・ACK を保存する前（DTN-08）。
	PointAfterPeerReply Point = "afterPeerReply"
)

// Points は journal に入れる要求の止める位置（契約テストの crashAt に書ける）。
var Points = []Point{PointAfterIntent, PointAfterActorReply, PointAfterCommit}

// HandoffPoints は transfer.prepare だけの止める位置（契約テストの crashAt にも書ける）。Host だけで処理する要求
// （transfer.prepare・accept・abort）では、Points のうち afterActorReply は bundle.export の応答の後、afterCommit は
// その要求の最後のトランザクションの後（応答を返す前）。
var HandoffPoints = []Point{PointAfterPrepare, PointAfterPeerReply}

// Hooks は試験用の止める位置と、Actor が止まったときの通知。
type Hooks struct {
	// At は止める位置ごとに呼ぶ（nil なら呼ばない）。誤りを返すと、Host はそこで処理をやめて
	// Handle が ErrStopped を返す（応答を返さない。プロセスが止まったのと同じ扱い。呼び出し側は Store を
	// 開き直して New からやり直す）。
	At func(p Point) error
	// OnActorLost は Container が応答しない・接続が切れた・壊れた応答を返したとき・internal を返したときに
	// 1 回呼ぶ（nil なら呼ばない）。復元は DTN-10。Restart までの新しい要求には not_ready を返す。
	OnActorLost func(err error)
}

// ErrStopped は Hooks.At が処理を止めたときの Handle の誤り。
var ErrStopped = errors.New("host: stopped by hook")

// Config は New の設定。
type Config struct {
	SessionID string            // meta.sessionId（署名済みのセッション ID）
	Actor     dtnproto.ActorRef // meta.actor
	// Limits は上限（ゼロ値なら dtnproto.DefaultLimits）。
	Limits dtnproto.Limits
	// ActorTimeout は Container の応答を待つ上限（0 なら DefaultActorTimeout）。
	ActorTimeout time.Duration
	// InitialEpoch は新しい保存の meta.epoch（0 なら 1）。
	InitialEpoch uint64
	// Now は meta.createdAt の時刻（nil なら time.Now）。
	Now   func() time.Time
	Hooks Hooks
	// Peer は CLA（transfer.accept を受け取る Host に届ける。DTN-08）。nil なら transfer.prepare は internal。
	Peer Peer
	// Contacts は transfer.prepare・transfer.accept の Contact の確認（DTN-09 が署名などを足す）。nil なら
	// transfer.prepare・transfer.accept は internal（確認なしでは転送しない）。
	Contacts ContactVerifier
}

// Container の状態（host.status の container）。
const (
	ContainerRunning = "running"
	ContainerLost    = "lost"
	ContainerStopped = "stopped"
)

// HostStatusOutputType は host.status の応答の outputs の 1 つの type。data は HostStatus。
const HostStatusOutputType = "host.status"

// HostStatus は host.status の応答の data（actor-protocol.md §3.2）。LastEventSeq は applied の行の最大の
// event_seq（intent の行は数えない。Session Front はそれより後の dispatch を同じ eventSeq で送り直す）。
type HostStatus struct {
	Epoch        uint64 `json:"epoch"`
	LastEventSeq uint64 `json:"lastEventSeq"`
	Container    string `json:"container"`
}

// Host は 1 Actor の Actor Host。New で作る。Handle は並行に呼んでよい（Actor ごとに直列にする）。
type Host struct {
	mu       sync.Mutex
	store    Store
	conn     ActorConn
	lim      dtnproto.Limits
	timeout  time.Duration
	hooks    Hooks
	peer     Peer
	contacts ContactVerifier

	meta      Meta // 確定した meta の写し
	lost      bool // Container を失った（Restart まで not_ready）
	destroyed bool // shutdown で保存を消した
	closed    bool
}

// New は store と conn の Host を作る。store が空なら meta を書く。meta があれば、版・セッション・Actor が
// 合うかを確かめる（違えば unsupported_version・session_mismatch・unknown_actor の *dtnproto.Error）。
// Host は conn を shutdown と Restart のときだけ閉じる。
func New(store Store, conn ActorConn, cfg Config) (*Host, error) {
	if cfg.SessionID == "" {
		return nil, errors.New("host: empty session id")
	}
	if !cfg.Actor.Valid() {
		return nil, fmt.Errorf("host: invalid actor %q", cfg.Actor)
	}
	h := &Host{store: store, conn: conn, lim: cfg.Limits, timeout: cfg.ActorTimeout, hooks: cfg.Hooks,
		peer: cfg.Peer, contacts: cfg.Contacts}
	if h.lim == (dtnproto.Limits{}) {
		h.lim = dtnproto.DefaultLimits
	}
	if h.timeout <= 0 {
		h.timeout = DefaultActorTimeout
	}
	var (
		meta Meta
		have bool
	)
	if err := store.View(func(tx Tx) (err error) { meta, have, err = tx.Meta(); return err }); err != nil {
		return nil, fmt.Errorf("host: read meta: %w", err)
	}
	if have {
		switch {
		case meta.SchemaVersion != SchemaVersion:
			return nil, dtnproto.Errorf(dtnproto.CodeUnsupportedVersion, "store schema version %d (want %d)", meta.SchemaVersion, SchemaVersion)
		case meta.SessionID != cfg.SessionID:
			return nil, dtnproto.Errorf(dtnproto.CodeSessionMismatch, "store is for session %q", meta.SessionID)
		case meta.Actor != cfg.Actor:
			return nil, dtnproto.Errorf(dtnproto.CodeUnknownActor, "store is for %q", meta.Actor)
		}
		h.meta = meta
		return h, nil
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	meta = Meta{SchemaVersion: SchemaVersion, SessionID: cfg.SessionID, Actor: cfg.Actor,
		Epoch: max(cfg.InitialEpoch, 1), CreatedAt: now().UTC().Format(time.RFC3339Nano)}
	if err := store.Update(func(tx Tx) error { return tx.PutMeta(meta) }); err != nil {
		return nil, fmt.Errorf("host: write meta: %w", err)
	}
	h.meta = meta
	return h, nil
}

// Meta は確定した meta の写しを返す。
func (h *Host) Meta() Meta {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.meta
}

// Container は Container の状態（ContainerRunning・ContainerLost・ContainerStopped）を返す。
func (h *Host) Container() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.containerState()
}

func (h *Host) containerState() string {
	switch {
	case h.destroyed:
		return ContainerStopped
	case h.lost:
		return ContainerLost
	}
	return ContainerRunning
}

// Restart は Container をつなぎ替える（復元の DTN-10 の拡張点）。古い conn を閉じてから meta.epoch を 1 増やして
// 保存し、conn を新しいものにして not_ready をやめる。journal の再生は呼び出し側（DTN-10）が行う。
func (h *Host) Restart(conn ActorConn) (uint64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.destroyed {
		return 0, ErrClosed
	}
	if h.conn != nil {
		_ = h.conn.Close()
	}
	if err := h.update(func(_ Tx, m *Meta) error { m.Epoch++; return nil }); err != nil {
		return 0, err
	}
	h.conn, h.lost = conn, false
	return h.meta.Epoch, nil
}

// Close は Store を閉じる（消さない）。2 回目は何もしない。
func (h *Host) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	if h.destroyed {
		return nil
	}
	return h.store.Close()
}

// Handle は要求の電文 raw を処理し、応答の電文を返す（persistence-and-recovery.md §3.1）。誤りの応答も
// 電文で返す。誤り（error）を返すのは、Hooks.At が止めたとき（ErrStopped）と Close の後（ErrClosed）だけ。
func (h *Host) Handle(ctx context.Context, raw []byte) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrClosed
	}
	req, verr := dtnproto.Validate(raw, h.lim)
	if verr != nil {
		return h.fail(req, verr), nil
	}
	if h.destroyed {
		// 2 回目の shutdown は何もしないで成功する（D-8）
		if req.Kind == dtnproto.KindShutdown {
			return encode(okResponse(req, h.meta.Epoch, nil)), nil
		}
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeNotReady, "shut down")), nil
	}
	switch {
	case req.SessionID != h.meta.SessionID:
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeSessionMismatch, "session %q is not this host's", req.SessionID)), nil
	case req.Actor != h.meta.Actor:
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeUnknownActor, "this is %q, not %q", h.meta.Actor, req.Actor)), nil
	case req.CoordEpoch < h.meta.MaxCoordEpoch:
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeStaleCoordEpoch, "coordEpoch %d < %d", req.CoordEpoch, h.meta.MaxCoordEpoch)), nil
	}
	ki, _ := dtnproto.LookupKind(req.Kind)
	switch {
	case req.Kind == dtnproto.KindShutdown:
		return h.shutdown(ctx, req)
	case req.Kind == dtnproto.KindHostStatus:
		return h.hostStatus(req), nil
	case req.Kind == dtnproto.KindTransferActivate:
		return h.journaled(ctx, req, h.activatePlan(req))
	case req.Kind == dtnproto.KindTransferGC:
		return h.journaled(ctx, req, h.gcPlan(req))
	case ki.HostOnly:
		return h.handleHostOnly(ctx, req)
	case !ki.Journaled:
		return h.forward(ctx, req), nil
	}
	return h.journaled(ctx, req, nil)
}

// thenPlan は、transfer.activate・transfer.gc を journal に入れる種類として処理するときの差分（DTN-08）。
// journal の行の kind・payload は Container に送る then（activate なら bytes を付けたもの）、req_digest は
// transfer.* の要求の digest（同じ要求の送り直しを見分ける）。
type thenPlan struct {
	// build は新しい intent の行の kind と payload を作る（transfers・bundles の行を確かめる）。
	build func(tx Tx) (dtnproto.Kind, json.RawMessage, *dtnproto.Error)
	// effects は ok の応答を確定するトランザクションで、bundles の効果の後に行う（transfers の状態）。
	effects func(tx Tx, m *Meta) error
}

// journaled は journal に入れる種類（Container に送る）を処理する（§3.1 の手順 2〜6）。plan が nil でなければ
// transfer.activate・transfer.gc（Container には then を送る）。
func (h *Host) journaled(ctx context.Context, req dtnproto.Request, plan *thenPlan) ([]byte, error) {
	digest, derr := dtnproto.RequestDigest(req)
	if derr != nil {
		return h.fail(req, asProtoError(derr, dtnproto.CodeInvalid)), nil
	}
	var (
		row, last      JournalRow
		have, haveLast bool
	)
	if err := h.store.View(func(tx Tx) (err error) {
		if row, have, err = tx.Journal(req.EventSeq); err != nil {
			return err
		}
		last, haveLast, err = tx.LastJournal()
		return err
	}); err != nil {
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "read journal: %v", err)), nil
	}

	// send は Container に送る要求（plan があれば then。kind と payload は journal の行と同じ）
	send := req
	// 手順 2: 冪等の確認（actor-protocol.md §5）
	switch {
	case have && row.ReqDigest != digest:
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeConflict, "eventSeq %d was recorded with another request", req.EventSeq)), nil
	case have && row.Status == StatusApplied:
		if err := h.bumpCoordEpoch(req); err != nil {
			return h.fail(req, err), nil
		}
		return row.Response, nil
	case have: // intent: 止まった後の送り直し。手順 4 から続ける
		if err := h.bumpCoordEpoch(req); err != nil {
			return h.fail(req, err), nil
		}
		send.Kind, send.Payload = row.Kind, row.Payload
	case haveLast && req.EventSeq < last.EventSeq:
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeStaleEvent, "eventSeq %d < %d", req.EventSeq, last.EventSeq)), nil
	case haveLast && last.Status == StatusIntent:
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeNotReady, "eventSeq %d is pending; resend it first", last.EventSeq)), nil
	case h.lost:
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeNotReady, "actor lost")), nil
	default:
		if plan != nil {
			if err := h.bumpCoordEpoch(req); err != nil {
				return h.fail(req, err), nil
			}
			var berr *dtnproto.Error
			if err := h.store.View(func(tx Tx) error {
				send.Kind, send.Payload, berr = plan.build(tx)
				return nil
			}); err != nil {
				return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "read transfer: %v", err)), nil
			}
			if berr != nil {
				return h.fail(req, berr), nil
			}
		}
		// 手順 3: journal に intent で書く
		if h.meta.JournalRows+1 > int64(h.lim.MaxJournalRows) {
			return h.fail(req, dtnproto.Errorf(dtnproto.CodeTooLarge, "journal: %d rows reaches the limit %d", h.meta.JournalRows, h.lim.MaxJournalRows)), nil
		}
		if h.meta.StoredBytes+int64(len(send.Payload)) > int64(h.lim.MaxStoredBytes) {
			return h.fail(req, dtnproto.Errorf(dtnproto.CodeTooLarge, "store: %d+%d bytes exceeds %d", h.meta.StoredBytes, len(send.Payload), h.lim.MaxStoredBytes)), nil
		}
		row = JournalRow{EventSeq: req.EventSeq, Kind: send.Kind, ReqDigest: digest,
			Payload: append(json.RawMessage(nil), send.Payload...), Status: StatusIntent, Epoch: h.meta.Epoch}
		if err := h.update(func(tx Tx, m *Meta) error {
			m.MaxCoordEpoch = max(m.MaxCoordEpoch, req.CoordEpoch)
			m.JournalRows++
			m.StoredBytes += row.storedBytes()
			if req.Kind == dtnproto.KindInit && len(m.InitPayload) == 0 {
				m.InitPayload = row.Payload // 復元（§4.1 の手順 4）はこれで init を送る
			}
			return tx.PutJournal(row)
		}); err != nil {
			return h.fail(req, asProtoError(err, dtnproto.CodeInternal)), nil
		}
	}
	if err := h.at(PointAfterIntent); err != nil {
		return nil, err
	}
	if h.lost {
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeNotReady, "actor lost")), nil
	}

	// 手順 4: Container に送る
	resp, cerr := h.call(ctx, send)
	if cerr != nil {
		return h.fail(req, cerr), nil
	}
	if err := h.at(PointAfterActorReply); err != nil {
		return nil, err
	}
	stored := stripBundles(resp)
	if !commitsResponse(resp) {
		// Container が処理しなかった（not_ready など）か、作り直しが要る（internal）。行は intent のまま
		if resp.Error != nil && resp.Error.Code == dtnproto.CodeInternal {
			h.markLost(fmt.Errorf("actor returned internal: %s", resp.Error.Message))
		}
		return encode(stored), nil
	}

	// 手順 5: 1 つのトランザクションで applied にする
	storedRaw := encode(stored)
	var extra func(Tx, *Meta) error
	if plan != nil {
		extra = plan.effects
	}
	if err := h.update(func(tx Tx, m *Meta) error {
		return h.commit(tx, m, send, resp, stored, storedRaw, extra)
	}); err != nil {
		return h.fail(req, asProtoError(err, dtnproto.CodeInternal)), nil
	}
	if err := h.at(PointAfterCommit); err != nil {
		return nil, err
	}
	// 手順 6: 応答を返す（bytes を除く）。まとめた行でも、返すのはこの要求の応答
	return storedRaw, nil
}

// commit は §3.1 の手順 5 をトランザクション tx の中で行う。extra は ok の応答のときに bundles の効果の後に行う。
func (h *Host) commit(tx Tx, m *Meta, req dtnproto.Request, resp, stored dtnproto.Response, storedRaw []byte, extra func(Tx, *Meta) error) error {
	intent, ok, err := tx.Journal(req.EventSeq)
	if err != nil {
		return err
	}
	if !ok || intent.Status != StatusIntent {
		return fmt.Errorf("journal: eventSeq %d is not an intent", req.EventSeq)
	}
	respDigest, err := dtnproto.ResponseDigest(stored)
	if err != nil {
		return dtnproto.Errorf(dtnproto.CodeInvalid, "response digest: %v", err)
	}
	final := intent
	final.Status, final.Response, final.RespDigest, final.Epoch = StatusApplied, storedRaw, respDigest, h.meta.Epoch

	if resp.Status == dtnproto.StatusOK {
		if err := h.applyEffects(tx, m, req, resp); err != nil {
			return err
		}
		if extra != nil {
			if err := extra(tx, m); err != nil {
				return err
			}
		}
		if req.Kind == dtnproto.KindAdvance {
			if err := h.mergeAdvance(tx, m, &final, stored); err != nil {
				return err
			}
		}
	}
	m.StoredBytes += final.storedBytes() - intent.storedBytes()
	if m.StoredBytes > int64(h.lim.MaxStoredBytes) {
		return dtnproto.Errorf(dtnproto.CodeTooLarge, "store: %d bytes exceeds %d", m.StoredBytes, h.lim.MaxStoredBytes)
	}
	return tx.PutJournal(final)
}

// mergeAdvance は、直前の行も applied の ok の advance なら、2 つを 1 つの行 final にまとめる
// （persistence-and-recovery.md §2・D-9）。final は新しい event_seq・新しい上限（payload）を持ち、response は
// 2 つの応答の outputs をつなげたもの、resp_digest はそれで計算し直す。古い行は消す。
// まとめた上限への 1 回の再生が応答の上限（outputs の数・電文の長さ）を超えるときは、まとめない。
func (h *Host) mergeAdvance(tx Tx, m *Meta, final *JournalRow, stored dtnproto.Response) error {
	prev, ok, err := tx.JournalBefore(final.EventSeq)
	if err != nil || !ok || prev.Kind != dtnproto.KindAdvance || prev.Status != StatusApplied {
		return err
	}
	var prevResp dtnproto.Response
	if err := json.Unmarshal(prev.Response, &prevResp); err != nil {
		return fmt.Errorf("journal: eventSeq %d: response: %w", prev.EventSeq, err)
	}
	if prevResp.Status != dtnproto.StatusOK {
		return nil
	}
	merged := stored
	merged.Outputs = append(append(make([]dtnproto.Output, 0, len(prevResp.Outputs)+len(stored.Outputs)), prevResp.Outputs...), stored.Outputs...)
	if len(merged.Outputs) > h.lim.MaxOutputs {
		return nil
	}
	mergedRaw := encode(merged)
	if replaySize(mergedRaw, merged.Outputs) > int64(h.lim.MaxMessageBytes) {
		return nil
	}
	d, err := dtnproto.ResponseDigest(merged)
	if err != nil {
		return dtnproto.Errorf(dtnproto.CodeInvalid, "response digest: %v", err)
	}
	if err := tx.DeleteJournal(prev.EventSeq); err != nil {
		return err
	}
	m.JournalRows--
	m.StoredBytes -= prev.storedBytes()
	final.Response, final.RespDigest = mergedRaw, d
	return nil
}

// replaySize は、bytes を除いた応答 raw に、outputs の Bundle の bytes（data.size）を base64 で足した長さ
// （再生のときの Container の応答の長さの見積もり）。
func replaySize(raw []byte, outs []dtnproto.Output) int64 {
	n := int64(len(raw))
	for _, o := range outs {
		var d struct {
			Size int64 `json:"size"`
		}
		if (o.Type == OutputLetterCreated || o.Type == OutputBundleExported) && json.Unmarshal(o.Data, &d) == nil && d.Size > 0 {
			n += 4*((d.Size+2)/3) + int64(len(`,"bundle":""`))
		}
	}
	return n
}

// forward は journal に入れない種類（inspect・bundle.export）を Container に送る。状態を変えない。
func (h *Host) forward(ctx context.Context, req dtnproto.Request) []byte {
	if err := h.bumpCoordEpoch(req); err != nil {
		return h.fail(req, err)
	}
	if h.lost {
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeNotReady, "actor lost"))
	}
	resp, cerr := h.call(ctx, req)
	if cerr != nil {
		return h.fail(req, cerr)
	}
	if resp.Error != nil && resp.Error.Code == dtnproto.CodeInternal {
		h.markLost(fmt.Errorf("actor returned internal: %s", resp.Error.Message))
	}
	return encode(stripBundles(resp))
}

// hostStatus は host.status に {epoch, lastEventSeq, container} を返す（journal に入れない）。
func (h *Host) hostStatus(req dtnproto.Request) []byte {
	if err := h.bumpCoordEpoch(req); err != nil {
		return h.fail(req, err)
	}
	var (
		last JournalRow
		ok   bool
	)
	if err := h.store.View(func(tx Tx) (err error) { last, ok, err = tx.LastApplied(); return err }); err != nil {
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "read journal: %v", err))
	}
	st := HostStatus{Epoch: h.meta.Epoch, Container: h.containerState()}
	if ok {
		st.LastEventSeq = last.EventSeq
	}
	data, _ := json.Marshal(st)
	return encode(okResponse(req, h.meta.Epoch, []dtnproto.Output{{Type: HostStatusOutputType, Data: data}}))
}

// handleHostOnly は Host だけで処理する種類（transfer.*・contact.*）を処理する（persistence-and-recovery.md §3.2）。
// transfer.activate・transfer.gc は Handle が journaled に回す。contact.* は DTN-09。
func (h *Host) handleHostOnly(ctx context.Context, req dtnproto.Request) ([]byte, error) {
	if err := h.bumpCoordEpoch(req); err != nil {
		return h.fail(req, err), nil
	}
	switch req.Kind {
	case dtnproto.KindTransferPrepare:
		return h.transferPrepare(ctx, req)
	case dtnproto.KindTransferAccept:
		return h.transferAccept(req)
	case dtnproto.KindTransferAbort:
		return h.transferAbort(req)
	case dtnproto.KindTransferStatus:
		return h.transferStatus(req), nil
	}
	return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "%q is not implemented yet (DTN-09)", req.Kind)), nil
}

// shutdown は Container を止めてから保存をすべて消す（persistence-and-recovery.md §6）。2 回目は Handle が
// 何もしないで成功を返す。保存を消す前に止まっても、もう一度 shutdown すれば消える。
func (h *Host) shutdown(ctx context.Context, req dtnproto.Request) ([]byte, error) {
	if h.conn != nil {
		if !h.lost {
			// Container が止まっていても続ける（失ったとはみなさない。OnActorLost を呼ばない）
			out := req
			out.Epoch = h.meta.Epoch
			if raw, err := json.Marshal(out); err == nil {
				cctx, cancel := context.WithTimeout(ctx, h.timeout)
				_, _ = h.conn.Call(cctx, raw)
				cancel()
			}
		}
		_ = h.conn.Close()
	}
	if err := h.at(PointAfterActorStop); err != nil {
		return nil, err
	}
	if err := h.store.Destroy(); err != nil {
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "destroy store: %v", err)), nil
	}
	h.destroyed = true
	return encode(okResponse(req, h.meta.Epoch, nil)), nil
}

// call は req を、epoch を meta.epoch で上書きして Container に送り、応答を読む。
// 応答の epoch が meta.epoch と違えば捨てて stale_epoch。Container が応答しない・壊れた応答は not_ready
// （Hooks.OnActorLost を呼ぶ）。
func (h *Host) call(ctx context.Context, req dtnproto.Request) (dtnproto.Response, *dtnproto.Error) {
	out := req
	out.Epoch = h.meta.Epoch
	raw, err := json.Marshal(out)
	if err != nil {
		return dtnproto.Response{}, dtnproto.Errorf(dtnproto.CodeInternal, "encode request: %v", err)
	}
	cctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	b, err := h.conn.Call(cctx, raw)
	if err != nil {
		if ctx.Err() != nil { // 呼び出し側の取り消し（Container のせいではない）
			return dtnproto.Response{}, dtnproto.Errorf(dtnproto.CodeInternal, "canceled: %v", ctx.Err())
		}
		h.markLost(err)
		return dtnproto.Response{}, dtnproto.Errorf(dtnproto.CodeNotReady, "actor did not answer: %v", err)
	}
	var resp dtnproto.Response
	if err := json.Unmarshal(b, &resp); err != nil {
		h.markLost(fmt.Errorf("malformed response: %w", err))
		return dtnproto.Response{}, dtnproto.Errorf(dtnproto.CodeNotReady, "actor returned a malformed response")
	}
	if resp.Epoch != h.meta.Epoch {
		return dtnproto.Response{}, dtnproto.Errorf(dtnproto.CodeStaleEpoch, "response epoch %d is not %d", resp.Epoch, h.meta.Epoch)
	}
	if resp.V != dtnproto.Version || resp.Actor != req.Actor || resp.EventSeq != req.EventSeq ||
		(resp.Status != dtnproto.StatusOK && resp.Status != dtnproto.StatusError) || (resp.Status == dtnproto.StatusError) != (resp.Error != nil) {
		h.markLost(errors.New("response does not match the request"))
		return dtnproto.Response{}, dtnproto.Errorf(dtnproto.CodeNotReady, "actor returned a response for another request")
	}
	return resp, nil
}

// commitsResponse は、Container の応答を applied として確定するか。ok と、Actor が決定的に出した誤り
// （invalid・too_large。再生でも同じ誤りになる）は確定する。それ以外の誤り（not_ready・stale_*・internal など。
// Container が要求を適用していない）は確定しない（行は intent のまま。同じ eventSeq で送り直す）。
func commitsResponse(resp dtnproto.Response) bool {
	if resp.Status == dtnproto.StatusOK {
		return true
	}
	return resp.Error != nil && (resp.Error.Code == dtnproto.CodeInvalid || resp.Error.Code == dtnproto.CodeTooLarge)
}

func (h *Host) at(p Point) error {
	if h.hooks.At == nil {
		return nil
	}
	if err := h.hooks.At(p); err != nil {
		return fmt.Errorf("%w at %s: %w", ErrStopped, p, err)
	}
	return nil
}

func (h *Host) markLost(err error) {
	if h.lost {
		return
	}
	h.lost = true
	if h.hooks.OnActorLost != nil {
		h.hooks.OnActorLost(err)
	}
}

// update は f を 1 つのトランザクションで実行し、f が変えた meta を同じトランザクションで書く。
// 確定したら h.meta を更新する。
func (h *Host) update(f func(tx Tx, m *Meta) error) error {
	m := h.meta
	if err := h.store.Update(func(tx Tx) error {
		if err := f(tx, &m); err != nil {
			return err
		}
		return tx.PutMeta(m)
	}); err != nil {
		return err
	}
	h.meta = m
	return nil
}

// bumpCoordEpoch は req.coordEpoch が meta.maxCoordEpoch より大きければ更新する。
func (h *Host) bumpCoordEpoch(req dtnproto.Request) *dtnproto.Error {
	if req.CoordEpoch <= h.meta.MaxCoordEpoch {
		return nil
	}
	if err := h.update(func(_ Tx, m *Meta) error { m.MaxCoordEpoch = req.CoordEpoch; return nil }); err != nil {
		return dtnproto.Errorf(dtnproto.CodeInternal, "write meta: %v", err)
	}
	return nil
}

func (h *Host) fail(req dtnproto.Request, e *dtnproto.Error) []byte {
	return encode(dtnproto.Response{V: dtnproto.Version, Actor: req.Actor, Epoch: h.meta.Epoch, EventSeq: req.EventSeq,
		Status: dtnproto.StatusError, Outputs: []dtnproto.Output{}, Error: e})
}

func okResponse(req dtnproto.Request, epoch uint64, outs []dtnproto.Output) dtnproto.Response {
	if outs == nil {
		outs = []dtnproto.Output{}
	}
	return dtnproto.Response{V: dtnproto.Version, Actor: req.Actor, Epoch: epoch, EventSeq: req.EventSeq,
		Status: dtnproto.StatusOK, Outputs: outs}
}

// stripBundles は outputs[].bundle を除いた応答を返す（Session Front・Coordinator への応答と保存の形）。
func stripBundles(resp dtnproto.Response) dtnproto.Response {
	outs := make([]dtnproto.Output, len(resp.Outputs))
	for i, o := range resp.Outputs {
		o.Bundle = nil
		outs[i] = o
	}
	resp.Outputs = outs
	return resp
}

func encode(resp dtnproto.Response) []byte {
	b, err := json.Marshal(resp)
	if err != nil {
		// 応答は読めた JSON から作るので、ここには来ない
		panic(fmt.Sprintf("host: encode response: %v", err))
	}
	return b
}

// asProtoError は err が *dtnproto.Error ならそれを、違えば code の *dtnproto.Error を返す。
func asProtoError(err error, code dtnproto.Code) *dtnproto.Error {
	if pe, ok := errors.AsType[*dtnproto.Error](err); ok && pe != nil {
		return pe
	}
	return dtnproto.Errorf(code, "%v", err)
}
