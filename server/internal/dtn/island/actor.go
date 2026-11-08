package island

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/bpv7"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/islandcore"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// bpv7.LetterCodec は dtnproto.LetterCodec を満たす（DTN-06 #163。島は bpv7 の Codec を使う）。
var _ dtnproto.LetterCodec = bpv7.LetterCodec{}

// UnsupportedStage は、分散モードの対象外のステージ（故障・注文ラッシュ・漂流瓶・欠航を定義したもの）の init を
// 断る invalid のメッセージの頭（simulation-time.md §6 のエラー dtn_unsupported_stage）。
const UnsupportedStage = "dtn_unsupported_stage"

// lifetimeMarginMs は Bundle の Lifetime の、ステージの durationMs への足し分（bpv7-profile.md §3）。
const lifetimeMarginMs = 60_000

// Option は New の設定。
type Option func(*Actor)

// WithCodec は Codec を変える（試験用。既定は init のステージから作る bpv7.LetterCodec）。
func WithCodec(c dtnproto.LetterCodec) Option { return func(a *Actor) { a.fixedCodec = c } }

// WithLimits は上限を変える（既定は dtnproto.DefaultLimits。Bundle の大きさに使う）。
func WithLimits(lim dtnproto.Limits) Option { return func(a *Actor) { a.lim = lim } }

// held は港か中継倉庫にある手紙の Bundle（作ったか、下ろされたときの bytes）。
type held struct {
	bundle    []byte
	digest    string
	createdAt int64
}

// note は、まだ窓口に入っていない依頼への requests.note（依頼を知ったときに当てはめる）。
type note struct {
	reqID    string
	created  bool
	resolved bool
}

// Actor は Island Actor（runtime.Actor を満たす）。New で作り、init で島を決める。
// 同時に 2 つ以上のメソッドを呼ばない（Runtime が直列にする）。
type Actor struct {
	stages     []*stage.Stage
	lim        dtnproto.Limits
	fixedCodec dtnproto.LetterCodec // WithCodec。nil なら init で bpv7 を作る

	// init で決まる
	inited   bool
	ref      dtnproto.ActorRef
	id       uint64
	islands  int
	relayCap int
	codec    dtnproto.LetterCodec
	core     *islandcore.Core

	bundles   map[ferry.LetterID]held // 港と中継倉庫の手紙の Bundle。検索だけに使い、走査しない
	delivered []ferry.LetterID        // 配達した手紙の ID（昇順。D-4）
	notes     []note                  // まだ知らない依頼への requests.note（reqID の昇順）
}

// New は stages（init の stage で引く）の Island Actor を作る。
func New(stages []*stage.Stage, opts ...Option) *Actor {
	a := &Actor{stages: slices.Clone(stages), lim: dtnproto.DefaultLimits}
	for _, o := range opts {
		o(a)
	}
	return a
}

func invalid(format string, args ...any) *dtnproto.Error {
	return dtnproto.Errorf(dtnproto.CodeInvalid, format, args...)
}

func internal(format string, args ...any) *dtnproto.Error {
	return dtnproto.Errorf(dtnproto.CodeInternal, format, args...)
}

func (a *Actor) findStage(id string) *stage.Stage {
	for _, st := range a.stages {
		if st != nil && st.ID == id {
			return st
		}
	}
	return nil
}

// Core は包んでいる IslandCore を返す（観察だけに使う。試験と不変条件の検査）。init の前は nil。
func (a *Actor) Core() *islandcore.Core { return a.core }

// Init は init の payload で島を作る（actor-protocol.md §3.1、simulation-time.md §4）。
// 全ノードで開幕式を計算して自島のノードだけを残し、plan（自島が依頼元の依頼）の発生と最初の RaftTick を積む。
// 出力は開幕式の後の最初の出力（参照モデルの cmd.game.start の、この島の分）で、t = 0・class 0。
func (a *Actor) Init(p dtnproto.InitPayload) ([]dtnproto.Output, error) {
	if a.inited {
		return nil, invalid("already initialized")
	}
	id, ok := p.Actor.IslandID()
	if !ok {
		return nil, dtnproto.Errorf(dtnproto.CodeUnknownActor, "%q is not an island", p.Actor)
	}
	st := a.findStage(p.Stage)
	if st == nil {
		return nil, invalid("payload.stage: unknown stage %q", p.Stage)
	}
	if p.StageHash != st.Hash {
		return nil, invalid("payload.stageHash: %s does not match stage %q (%s)", p.StageHash, st.ID, st.Hash)
	}
	cfg, err := stage.SimConfig(st)
	if err != nil {
		return nil, invalid("stage %q: %v", st.ID, err)
	}
	if kinds := unsupportedEvents(cfg); len(kinds) > 0 {
		return nil, invalid("%s: stage %q defines %s", UnsupportedStage, st.ID, strings.Join(kinds, ", "))
	}
	if id > uint64(cfg.Islands) {
		return nil, dtnproto.Errorf(dtnproto.CodeUnknownActor, "stage %q has no island %d", st.ID, id)
	}
	plan, perr := planOf(p.Plan, id)
	if perr != nil {
		return nil, perr
	}
	codec := a.fixedCodec
	if codec == nil {
		codec = bpv7.LetterCodec{LifetimeMs: uint64(int64(cfg.DurationMs) + lifetimeMarginMs)}
	}

	node, l, rcs, err := islandcore.Opening(islandcore.NodeParams{
		Islands:         cfg.Islands,
		InitialLeader:   cfg.InitialLeader,
		ElectionTick:    cfg.Raft.ElectionTick,
		HeartbeatTick:   cfg.Raft.HeartbeatTick,
		MaxSizePerMsg:   cfg.Raft.MaxSizePerMsg,
		MaxInflightMsgs: cfg.Raft.MaxInflightMsgs,
		Resources:       cfg.Resources,
		RandIntn:        func(nid uint64) func(int) int { return sim.RaftRandIntn(p.Seed, nid) },
	}, id)
	if err != nil {
		return nil, invalid("stage %q: opening: %v", st.ID, err)
	}
	core, err := islandcore.New(islandcore.Config{
		Island:        id,
		Islands:       cfg.Islands,
		TickMs:        int64(cfg.Raft.TickMs),
		RetryMs:       cfg.Requests.RetryMs,
		PortCap:       cfg.PortCap,
		PerLinkAppCap: cfg.PerLinkAppCap,
		RelayCap:      cfg.RelayCap,
		Requests:      plan,
	}, node, l)
	if err != nil {
		return nil, invalid("stage %q: %v", st.ID, err)
	}

	a.inited = true
	a.ref, a.id, a.islands, a.relayCap, a.codec, a.core = p.Actor, id, cfg.Islands, cfg.RelayCap, codec, core
	a.bundles = make(map[ferry.LetterID]held)
	a.delivered, a.notes = nil, nil

	// 参照モデルの start() と同じ順: 開幕式の役割の変化 → Process → node.state（どれも t = 0・ClassStart）、
	// そのあと最初の RaftTick と依頼の発生を積む（ゲームの開始は常に t = 0）。
	for _, rc := range rcs {
		core.ApplyRoleChange(0, rc)
	}
	core.Process(0, islandcore.ClassStart)
	core.ReportState(0, islandcore.ClassStart)
	outs, cerr := a.convert(core.Drain())
	core.Start(0)
	if cerr != nil {
		return nil, cerr
	}
	return outs, nil
}

// unsupportedEvents は、分散モードの対象外のイベントのうちステージが定義したものの名前を返す（simulation-time.md §6）。
func unsupportedEvents(cfg sim.Config) []string {
	var out []string
	if len(cfg.Breakdowns) > 0 {
		out = append(out, "breakdown")
	}
	if len(cfg.Rushes) > 0 {
		out = append(out, "rush")
	}
	if len(cfg.Drifts) > 0 {
		out = append(out, "drift")
	}
	if len(cfg.Cancellations) > 0 {
		out = append(out, "cancellation")
	}
	return out
}

// planOf は init の plan を依頼の列にする。依頼元が自島でない・ID の重複・台帳のコマンドにできない依頼は invalid。
func planOf(plan []dtnproto.PlannedRequest, island uint64) ([]game.Request, *dtnproto.Error) {
	out := make([]game.Request, 0, len(plan))
	for i, pr := range plan {
		r := game.Request{ID: pr.ID, Origin: pr.Origin, Kind: pr.Kind, Resource: pr.Resource,
			Critical: pr.Critical, CreatedAt: pr.CreatedAt, Deadline: pr.Deadline}
		switch {
		case r.Origin != island:
			return nil, invalid("payload.plan[%d].origin: %d is not this island %d", i, r.Origin, island)
		case r.ID == "":
			return nil, invalid("payload.plan[%d].id: empty", i)
		case r.CreatedAt < 0 || r.Deadline < r.CreatedAt:
			return nil, invalid("payload.plan[%d]: invalid times", i)
		case slices.ContainsFunc(out, func(o game.Request) bool { return o.ID == r.ID }):
			return nil, invalid("payload.plan[%d].id: duplicate %q", i, r.ID)
		}
		if _, err := ledger.Encode(r.Command()); err != nil {
			return nil, invalid("payload.plan[%d]: %v", i, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// Handle は advance・deliver・port.remove・unload・relay.remove・requests.note・bundle.export を処理する。
func (a *Actor) Handle(kind dtnproto.Kind, raw json.RawMessage) ([]dtnproto.Output, error) {
	if !a.inited {
		return nil, dtnproto.Errorf(dtnproto.CodeNotReady, "not initialized")
	}
	pv, perr := dtnproto.DecodePayload(kind, raw, a.ref, a.lim)
	if perr != nil {
		return nil, perr
	}
	switch p := pv.(type) {
	case *dtnproto.AdvancePayload:
		return a.advance(p)
	case *dtnproto.LettersPayload:
		if kind == dtnproto.KindDeliver {
			return a.deliver(p)
		}
	case *dtnproto.LetterIDsPayload:
		switch kind {
		case dtnproto.KindPortRemove:
			return a.remove(p.LetterIDs, false)
		case dtnproto.KindRelayRemove:
			return a.remove(p.LetterIDs, true)
		}
	case *dtnproto.UnloadPayload:
		return a.unload(p)
	case *dtnproto.RequestsNotePayload:
		return a.note(p)
	case *dtnproto.ExportPayload:
		return a.export(p)
	}
	return nil, invalid("%q is not for an island", kind)
}

func toMark(m dtnproto.Mark) islandcore.Mark {
	return islandcore.Mark{T: m.T, Class: islandcore.Class(m.C)}
}

// advance は上限まで島のイベントを処理する（simulation-time.md §2.1）。減る上限は invalid、同じ上限は出力なし。
func (a *Actor) advance(p *dtnproto.AdvancePayload) ([]dtnproto.Output, error) {
	h, cur := toMark(p.Horizon), a.core.Horizon()
	switch {
	case h.Less(cur):
		return nil, invalid("advance: horizon [%d, %d] is before [%d, %d]", h.T, h.Class, cur.T, cur.Class)
	case h == cur:
		return nil, nil
	}
	if err := a.core.AdvanceTo(h); err != nil {
		return nil, invalid("advance: %v", err)
	}
	return a.convert(a.core.Drain())
}

// deliver は到着した手紙を、この順に島に配達する（actor-protocol.md §3.1）。
// 上限がちょうど (t, 1)・宛先が自島・まだ配達していない（D-4）こと。
func (a *Actor) deliver(p *dtnproto.LettersPayload) ([]dtnproto.Output, error) {
	if h, want := a.core.Horizon(), (islandcore.Mark{T: p.T, Class: islandcore.ClassArrive}); h != want {
		return nil, invalid("deliver: horizon [%d, %d] is not [%d, %d]", h.T, h.Class, want.T, want.Class)
	}
	letters := make([]ferry.Letter, 0, len(p.Letters))
	var reqs []game.Request // 依頼の手紙の依頼（手紙の順。同じ依頼は 1 つ）
	for i := range p.Letters {
		l, cmd, err := a.receive(&p.Letters[i], p.T)
		if err != nil {
			return nil, err
		}
		switch {
		case l.To != a.id:
			return nil, invalid("deliver: letter %d is for island %d, not %d", l.ID, l.To, a.id)
		case a.wasDelivered(l.ID):
			return nil, invalid("deliver: letter %d was already delivered", l.ID)
		}
		if l.Kind == ferry.LetterRequest {
			r, err := a.knownOrNew(l, cmd, reqs)
			if err != nil {
				return nil, err
			}
			if !slices.ContainsFunc(reqs, func(o game.Request) bool { return o.ID == r.ID }) {
				reqs = append(reqs, r)
			}
		}
		l.Location = ferry.Location{Kind: ferry.LocDelivered, Island: a.id}
		letters = append(letters, l)
	}

	// ここから状態を変える。依頼の手紙が届いたことは、その依頼が発生したことを意味する（参照モデルでは
	// 発生の時点で全島に MarkCreated を呼ぶので、依頼の手紙が届いた島では必ず created になっている）。
	for _, r := range reqs {
		a.learn(r)
		a.core.MarkCreated(r.ID)
	}
	for _, l := range letters {
		i, _ := slices.BinarySearch(a.delivered, l.ID)
		a.delivered = slices.Insert(a.delivered, i, l.ID)
	}
	a.core.Deliver(p.T, letters)
	return a.convert(a.core.Drain())
}

// knownOrNew は依頼の手紙 l の台帳のコマンド cmd を確かめ、その依頼を返す。窓口が知っている依頼・同じ要求の
// ほかの手紙の依頼（seen）と食い違えば invalid。自島が依頼元なのに計画にない依頼も invalid。
func (a *Actor) knownOrNew(l ferry.Letter, cmd []byte, seen []game.Request) (game.Request, *dtnproto.Error) {
	r, err := requestFromCommand(l.Req.ReqID, cmd, a.islands)
	if err != nil {
		return game.Request{}, invalid("letter %d: %v", l.ID, err)
	}
	if k, ok := a.core.Request(r.ID); ok {
		if k.Command() != r.Command() {
			return game.Request{}, invalid("letter %d: request %s differs from the known one", l.ID, r.ID)
		}
		return k, nil
	}
	if r.Origin == a.id {
		return game.Request{}, invalid("letter %d: request %s of this island is not in the plan", l.ID, r.ID)
	}
	for _, s := range seen {
		if s.ID == r.ID && s.Command() != r.Command() {
			return game.Request{}, invalid("letter %d: request %s differs from another letter's", l.ID, r.ID)
		}
	}
	return r, nil
}

// learn は依頼 r を窓口に入れ（知っていれば何もしない）、その依頼への requests.note があれば当てはめる。
func (a *Actor) learn(r game.Request) {
	added, err := a.core.LearnRequest(r)
	if err != nil {
		panic(fmt.Sprintf("island: %v", err)) // knownOrNew で確かめたので起きない
	}
	if !added {
		return
	}
	i, ok := slices.BinarySearchFunc(a.notes, r.ID, func(n note, id string) int { return strings.Compare(n.reqID, id) })
	if !ok {
		return
	}
	n := a.notes[i]
	a.notes = slices.Delete(a.notes, i, i+1)
	if n.created {
		a.core.MarkCreated(r.ID)
	}
	if n.resolved {
		a.core.MarkResolved(r.ID)
	}
}

func (a *Actor) wasDelivered(id ferry.LetterID) bool {
	_, ok := slices.BinarySearch(a.delivered, id)
	return ok
}

// remove は港（relay なら中継倉庫）から手紙を除く（船に積まれた。port.remove・relay.remove）。出力はない。
func (a *Actor) remove(ids []ferry.LetterID, relay bool) ([]dtnproto.Output, error) {
	place, list := "port", a.core.Port().Port()
	if relay {
		place, list = "relay", a.core.Port().Relay()
	}
	for _, id := range ids {
		if _, ok := slices.BinarySearch(list, id); !ok {
			return nil, invalid("%s.remove: letter %d is not in the %s of island %d", place, id, place, a.id)
		}
	}
	var err error
	if relay {
		_, err = a.core.TakeRelay(ids)
	} else {
		_, err = a.core.Take(ids)
	}
	if err != nil {
		return nil, internal("%s.remove: %v", place, err) // 上で確かめたので起きない
	}
	for _, id := range ids {
		delete(a.bundles, id)
	}
	return nil, nil
}

// unload は船から下ろした手紙を、ID の昇順（payload の順）に港か中継倉庫に入れる（actor-protocol.md §3.1）。
// 上限がちょうど (t, 8)（コマンドの位置）であること。place の食い違い・倉庫の上限を超えるものは invalid。
func (a *Actor) unload(p *dtnproto.UnloadPayload) ([]dtnproto.Output, error) {
	if h, want := a.core.Horizon(), (islandcore.Mark{T: p.T, Class: islandcore.ClassCommand}); h != want {
		return nil, invalid("unload: horizon [%d, %d] is not [%d, %d]", h.T, h.Class, want.T, want.Class)
	}
	port := a.core.Port()
	inRelay := len(port.Relay())
	letters := make([]ferry.Letter, 0, len(p.Letters))
	keep := make([]held, 0, len(p.Letters))
	for i := range p.Letters {
		ul := &p.Letters[i]
		l, cmd, err := a.receive(&ul.Letter, p.T)
		if err != nil {
			return nil, err
		}
		if _, here := port.Letter(l.ID); here {
			return nil, invalid("unload: letter %d is already in island %d", l.ID, a.id)
		}
		if a.wasDelivered(l.ID) {
			return nil, invalid("unload: letter %d was already delivered", l.ID)
		}
		if l.Kind == ferry.LetterRequest {
			if _, err := requestFromCommand(l.Req.ReqID, cmd, a.islands); err != nil {
				return nil, invalid("unload: letter %d: %v", l.ID, err)
			}
		}
		switch ul.Place {
		case dtnproto.PlacePort:
			if l.From != a.id {
				return nil, invalid("unload: letter %d is from island %d; only this island's letters go to the port", l.ID, l.From)
			}
			l.Location = ferry.Location{Kind: ferry.LocPort, Island: a.id}
		case dtnproto.PlaceRelay:
			if l.From == a.id || l.To == a.id {
				return nil, invalid("unload: letter %d (%d→%d) cannot be relayed at island %d", l.ID, l.From, l.To, a.id)
			}
			inRelay++
			l.Location = ferry.Location{Kind: ferry.LocRelay, Island: a.id}
		default:
			return nil, invalid("unload: letter %d: unknown place %q", l.ID, ul.Place)
		}
		letters = append(letters, l)
		keep = append(keep, held{bundle: slices.Clone(ul.Bundle), digest: ul.Digest, createdAt: ul.CreatedAt})
	}
	if inRelay > a.relayCap {
		return nil, invalid("unload: relay of island %d would hold %d of %d", a.id, inRelay, a.relayCap)
	}

	a.core.Unload(p.T, letters)
	for i, l := range letters {
		a.bundles[l.ID] = keep[i] // 規則 R1〜R6 で破棄されたものは、convert の letter.dropped で除く
	}
	return a.convert(a.core.Drain())
}

// note は requests.note を当てはめる（依頼の発生・依頼元の台帳の結果）。窓口がまだ知らない依頼は覚えておき、
// 依頼の手紙で知ったときに当てはめる。出力はない。
func (a *Actor) note(p *dtnproto.RequestsNotePayload) ([]dtnproto.Output, error) {
	for _, id := range p.Created {
		a.mark(id, true)
	}
	for _, id := range p.Resolved {
		a.mark(id, false)
	}
	return nil, nil
}

func (a *Actor) mark(reqID string, created bool) {
	if _, ok := a.core.Request(reqID); ok {
		if created {
			a.core.MarkCreated(reqID)
		} else {
			a.core.MarkResolved(reqID)
		}
		return
	}
	i, ok := slices.BinarySearchFunc(a.notes, reqID, func(n note, id string) int { return strings.Compare(n.reqID, id) })
	if !ok {
		a.notes = slices.Insert(a.notes, i, note{reqID: reqID})
	}
	if created {
		a.notes[i].created = true
	} else {
		a.notes[i].resolved = true
	}
}

// ExportedData は bundle.exported の data（actor-protocol.md §4）。
type ExportedData struct {
	LetterID ferry.LetterID `json:"letterId"`
	Digest   string         `json:"digest"`
	Size     int            `json:"size"`
}

// export は、港か中継倉庫の手紙ごとに Bundle Age を t − createdAt にした bytes を、要求の順に bundle.exported で
// 返す。状態は変えない（journal に入らないので出力の seq は 0、class は 8）。
func (a *Actor) export(p *dtnproto.ExportPayload) ([]dtnproto.Output, error) {
	outs := make([]dtnproto.Output, 0, len(p.LetterIDs))
	for _, id := range p.LetterIDs {
		h, ok := a.bundles[id]
		if !ok {
			return nil, invalid("bundle.export: letter %d is not in the port or relay of island %d", id, a.id)
		}
		if p.T < h.createdAt {
			return nil, invalid("bundle.export: t %d is before letter %d was created (%d)", p.T, id, h.createdAt)
		}
		b, err := a.codec.WithAge(h.bundle, p.T-h.createdAt)
		if err != nil {
			return nil, invalid("bundle.export: letter %d: %v", id, err)
		}
		data, err := json.Marshal(ExportedData{LetterID: id, Digest: dtnproto.Digest(b), Size: len(b)})
		if err != nil {
			return nil, err
		}
		outs = append(outs, dtnproto.Output{T: p.T, Class: uint8(islandcore.ClassCommand), Type: OutputBundleExported, Data: data, Bundle: b})
	}
	return outs, nil
}
