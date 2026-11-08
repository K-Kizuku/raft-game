// Package simtrace は、参照モデル（sim.Engine）と分散版（Coordinator の出力）のドメインイベントの列を
// 比べるための正規化トレースを作る（DTN-00 段階 C、docs/dtn/simulation-time.md §5.2・§5.3）。
//
// 全体の 1 本の seq と、島をまたぐイベントの前後関係は、分散版では参照モデルと一致しない。そこで:
//
//   - 島・船ごとの列（island:<id>・ferry:<id>）は、その列の中の (t, type, 中身) の並びで比べる
//   - global の累積しないイベントは、同じ t ごとに、中身の正準の JSON の多重集合で比べる
//   - score.updated は、同じ t ごとに、その時刻の最後の値で比べる
//   - 最後の結果（round.result・各島の台帳・各依頼と各島の最終の状態）を比べる
//
// 手紙の ID の採番の規則（全体の連番・島ごとの番号）には依らない。列の振り分けに要る島・船は、
// イベントの中身（letter.created の送信元と、letter.moved の位置の履歴）から求める。
// 出力はイベントの列だけで決まり、map の走査順に依らない。
package simtrace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// 列の名前。
const (
	islandPrefix = "island:"
	ferryPrefix  = "ferry:"
	// UnknownStream は、持ち主の島・船を決められなかったイベントの列（letter.created のない手紙の
	// letter.dropped・letter.moved など）。参照モデルの完全な列では空になる。
	UnknownStream = "unknown"
)

// Entry は正規化したイベント 1 件。seq は持たない。Data は中身の正準の JSON（キーを辞書順に並べたもの）。
type Entry struct {
	T    sim.SimTime     `json:"t"`
	Type sim.EventType   `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Stream は島・船の列 1 本（§5.3 の 1）。Events は元の列の順。
type Stream struct {
	Name   string  `json:"name"` // "island:<id>" | "ferry:<id>" | "unknown"
	Events []Entry `json:"events"`
}

// GlobalAt は、ある t の global の累積しないイベントの多重集合（§5.3 の 2）。Events は (type, data) の昇順。
type GlobalAt struct {
	T      sim.SimTime `json:"t"`
	Events []Entry     `json:"events"`
}

// ScoreAt は、ある t の最後の score.updated の中身（§5.3 の 3）。
type ScoreAt struct {
	T    sim.SimTime     `json:"t"`
	Data json.RawMessage `json:"data"`
}

// LedgerFinal は 1 島の台帳の内容（ledger.applied の中身を、適用した順に並べたもの）。
type LedgerFinal struct {
	IslandID uint64            `json:"islandId"`
	Applied  []json.RawMessage `json:"applied"`
}

// KeyedState は、ID ごとの最終の状態（依頼・島）。State は正準の JSON。
type KeyedState struct {
	ID    string          `json:"id"`
	State json.RawMessage `json:"state"`
}

// Final は最後の結果（§5.3 の 4）。
type Final struct {
	// Result は round.result（sim.Engine.Result）の正準の JSON。ドメインイベントではないので、
	// Trace.WithResult で渡したときだけ入る。渡していなければ null。
	Result json.RawMessage `json:"result"`
	// Ledgers は台帳のある島の ID の昇順。
	Ledgers []LedgerFinal `json:"ledgers"`
	// Requests は request.created に request.updated を順に重ねた最終の状態。ID の昇順（文字列の比較）。
	Requests []KeyedState `json:"requests"`
	// Islands は各島の最後の island.updated の中身。島の ID の昇順。
	Islands []KeyedState `json:"islands"`
}

// Trace は正規化トレース。JSON にすると simcli --normalized-trace の出力になる。
type Trace struct {
	Streams []Stream   `json:"streams"` // 島の ID の昇順、船の ID の昇順、最後に unknown（あれば）
	Global  []GlobalAt `json:"global"`  // t の昇順
	Score   []ScoreAt  `json:"score"`   // t の昇順
	Final   Final      `json:"final"`
}

// WithResult は round.result を Final.Result に入れた複製を返す（t は変えない）。
func (t Trace) WithResult(r sim.RoundResult) Trace {
	t.Final.Result = mustCanonical(r)
	return t
}

// probe は、列の振り分けに要るフィールドだけを中身の JSON から読む（中身の具体的な型に依らない）。
// ID の欄は数でも文字列でもよいよう json.RawMessage で受け、idOf で読む。
type probe struct {
	ID       json.RawMessage `json:"id"`
	IslandID json.RawMessage `json:"islandId"`
	FerryID  string          `json:"ferryId"`
	LetterID json.RawMessage `json:"letterId"`
	Letter   *struct {
		ID   json.RawMessage `json:"id"`
		From json.RawMessage `json:"from"`
	} `json:"letter"`
	Loc *struct {
		Type     string          `json:"type"`
		IslandID json.RawMessage `json:"islandId"`
		FerryID  string          `json:"ferryId"`
	} `json:"loc"`
	Request json.RawMessage `json:"request"`
}

// idOf は ID の欄の値を文字列にする（数はその表記、文字列は中身）。欄がなければ ""。
func idOf(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// letterInfo は手紙の送信元と今の位置（letter.created・letter.moved から追う）。
type letterInfo struct {
	from    string // 送信元の島の ID（10 進）
	locType string // "port" | "ferry" | "relay" | ...
	island  string // locType が port・relay のときの島
	ferry   string // locType が ferry のときの船
}

// streamKey は列の並べ順。kind は 0 = 島・1 = 船・2 = unknown。
type streamKey struct {
	kind int
	num  uint64
	str  string
}

// Normalize は、ドメインイベントの列（seq の順）を §5.2 の列に振り分けて正規化トレースを作る。
// 入力の Seq は使わない（並びだけを使う）。round.result は WithResult で足す。
// 知らない type のイベントは global（累積しない）に入れる。
func Normalize(events []sim.DomainEvent) Trace {
	n := newNormalizer()
	for _, ev := range events {
		n.add(ev)
	}
	return n.finish()
}

func newNormalizer() *normalizer {
	return &normalizer{
		streamIdx: map[string]int{},
		letters:   map[string]*letterInfo{},
		ledgerIdx: map[string]int{},
		reqIdx:    map[string]int{},
		islIdx:    map[string]int{},
	}
}

type normalizer struct {
	streams   []Stream
	keys      []streamKey
	streamIdx map[string]int // 検索だけに使い、走査しない

	letters map[string]*letterInfo // 手紙の ID（10 進）→ 情報。検索だけ

	global []Entry
	score  []ScoreAt

	ledgers   []LedgerFinal
	ledgerIdx map[string]int

	requests []KeyedState
	reqState []map[string]json.RawMessage
	reqIdx   map[string]int

	islands []KeyedState
	islIdx  map[string]int
}

// add は ev を振り分ける。島・船の列に入れたら、その列の名前を返す（global・score なら ""）。
func (n *normalizer) add(ev sim.DomainEvent) string {
	raw := mustMarshal(ev.Data)
	e := Entry{T: ev.T, Type: ev.Type, Data: canonical(raw)}
	var p probe
	_ = json.Unmarshal(raw, &p) // 形の合わないフィールドは空のまま（振り分けは unknown になる）

	switch ev.Type {
	case sim.EvNodeState, sim.EvLeaderElected, sim.EvSteppedDown:
		return n.toIsland(idOf(p.ID), e)
	case sim.EvLedgerApplied:
		n.ledger(idOf(p.IslandID), e.Data)
		return n.toIsland(idOf(p.IslandID), e)
	case sim.EvLetterCreated:
		var id, from string
		if p.Letter != nil {
			// 作った時点の位置は送信元の港（LetterView の loc は {port, from}）。
			id, from = idOf(p.Letter.ID), idOf(p.Letter.From)
			n.letters[id] = &letterInfo{from: from, locType: "port", island: from}
		}
		return n.toIsland(from, e)
	case sim.EvLetterDropped:
		// 港の規則での破棄。港にあるのは、その島が送信元の手紙だけ（倉庫の手紙は港の規則の対象外）。
		island := ""
		if li := n.letters[idOf(p.LetterID)]; li != nil {
			island = li.from
			if (li.locType == "port" || li.locType == "relay") && li.island != "" {
				island = li.island
			}
			li.locType, li.island, li.ferry = "dropped", "", ""
		}
		return n.toIsland(island, e)
	case sim.EvLetterDelivered:
		if li := n.letters[idOf(p.LetterID)]; li != nil {
			li.locType, li.island, li.ferry = "delivered", idOf(p.IslandID), ""
		}
		return n.toIsland(idOf(p.IslandID), e)
	case sim.EvLetterMoved:
		// 積む（loc = ferry）はその船の列。下ろす（loc = port・relay）は、下ろす前にいた船の列。
		ferryID := ""
		li := n.letters[idOf(p.LetterID)]
		if p.Loc != nil && p.Loc.Type == "ferry" {
			ferryID = p.Loc.FerryID
		} else if li != nil && li.locType == "ferry" {
			ferryID = li.ferry
		}
		if li != nil && p.Loc != nil {
			li.locType, li.island, li.ferry = p.Loc.Type, idOf(p.Loc.IslandID), p.Loc.FerryID
		}
		return n.toFerry(ferryID, e)
	case sim.EvFerryDeparted, sim.EvFerryArrived, sim.EvFerryBroken, sim.EvFerryRepaired:
		return n.toFerry(p.FerryID, e)
	case sim.EvScoreUpdated:
		if k := len(n.score); k > 0 && n.score[k-1].T == e.T {
			n.score[k-1].Data = e.Data
		} else {
			n.score = append(n.score, ScoreAt{T: e.T, Data: e.Data})
		}
	case sim.EvRequestCreated:
		n.global = append(n.global, e)
		n.requestCreated(p.Request)
	case sim.EvRequestUpdated:
		n.global = append(n.global, e)
		n.requestUpdated(raw)
	case sim.EvIslandUpdated:
		n.global = append(n.global, e)
		id := idOf(p.ID)
		if i, ok := n.islIdx[id]; ok {
			n.islands[i].State = e.Data
		} else {
			n.islIdx[id] = len(n.islands)
			n.islands = append(n.islands, KeyedState{ID: id, State: e.Data})
		}
	default:
		// phase.changed・forecast・route.blocked・route.unblocked・letter.stale と、知らない type。
		n.global = append(n.global, e)
	}
	return ""
}

// toIsland は島 id（10 進）の列に e を足す。id が分からなければ unknown。
func (n *normalizer) toIsland(id string, e Entry) string {
	v, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return n.toStream(UnknownStream, streamKey{kind: 2}, e)
	}
	// 表記を揃える（"01" などを "1" に）。
	return n.toStream(islandPrefix+strconv.FormatUint(v, 10), streamKey{kind: 0, num: v}, e)
}

// toFerry は船 id の列に e を足す。id が空なら unknown。
func (n *normalizer) toFerry(id string, e Entry) string {
	if id == "" {
		return n.toStream(UnknownStream, streamKey{kind: 2}, e)
	}
	return n.toStream(ferryPrefix+id, streamKey{kind: 1, str: id}, e)
}

func (n *normalizer) toStream(name string, key streamKey, e Entry) string {
	i, ok := n.streamIdx[name]
	if !ok {
		i = len(n.streams)
		n.streamIdx[name] = i
		n.streams = append(n.streams, Stream{Name: name})
		n.keys = append(n.keys, key)
	}
	n.streams[i].Events = append(n.streams[i].Events, e)
	return name
}

func (n *normalizer) ledger(id string, data json.RawMessage) {
	i, ok := n.ledgerIdx[id]
	if !ok {
		v, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			return // 島の分からない ledger.applied は unknown の列でだけ比べる
		}
		i = len(n.ledgers)
		n.ledgerIdx[id] = i
		n.ledgers = append(n.ledgers, LedgerFinal{IslandID: v})
	}
	n.ledgers[i].Applied = append(n.ledgers[i].Applied, data)
}

// requestCreated は request.created の request（RequestState）を最終の状態の初期値にする。
func (n *normalizer) requestCreated(req json.RawMessage) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(req, &fields); err != nil {
		return
	}
	var id string
	if err := json.Unmarshal(fields["id"], &id); err != nil {
		return
	}
	n.requestFields(id, fields)
}

// requestUpdated は request.updated の id 以外のフィールド（status・expired・result）を最終の状態に重ねる。
func (n *normalizer) requestUpdated(raw json.RawMessage) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return
	}
	var id string
	if err := json.Unmarshal(fields["id"], &id); err != nil {
		return
	}
	n.requestFields(id, fields)
}

func (n *normalizer) requestFields(id string, fields map[string]json.RawMessage) {
	i, ok := n.reqIdx[id]
	if !ok {
		i = len(n.requests)
		n.reqIdx[id] = i
		n.requests = append(n.requests, KeyedState{ID: id})
		n.reqState = append(n.reqState, map[string]json.RawMessage{})
	}
	st := n.reqState[i]
	for k, v := range fields { // 上書きだけなので、走査の順は結果に効かない
		st[k] = v
	}
}

func (n *normalizer) finish() Trace {
	// 列: 島の ID の昇順 → 船の ID の昇順 → unknown。
	order := make([]int, len(n.streams))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int {
		ka, kb := n.keys[a], n.keys[b]
		if ka.kind != kb.kind {
			return ka.kind - kb.kind
		}
		if ka.num != kb.num {
			if ka.num < kb.num {
				return -1
			}
			return 1
		}
		return strings.Compare(ka.str, kb.str)
	})
	tr := Trace{
		Streams: make([]Stream, 0, len(order)),
		Global:  groupGlobal(n.global),
		Score:   n.score,
	}
	for _, i := range order {
		tr.Streams = append(tr.Streams, n.streams[i])
	}
	if tr.Score == nil {
		tr.Score = []ScoreAt{}
	}

	tr.Final.Ledgers = n.ledgers
	slices.SortFunc(tr.Final.Ledgers, func(a, b LedgerFinal) int { return cmpUint(a.IslandID, b.IslandID) })
	if tr.Final.Ledgers == nil {
		tr.Final.Ledgers = []LedgerFinal{}
	}
	tr.Final.Requests = make([]KeyedState, len(n.requests))
	for i, r := range n.requests {
		tr.Final.Requests[i] = KeyedState{ID: r.ID, State: mustCanonical(n.reqState[i])}
	}
	slices.SortFunc(tr.Final.Requests, func(a, b KeyedState) int { return strings.Compare(a.ID, b.ID) })
	tr.Final.Islands = n.islands
	slices.SortFunc(tr.Final.Islands, func(a, b KeyedState) int { return cmpNumericID(a.ID, b.ID) })
	if tr.Final.Islands == nil {
		tr.Final.Islands = []KeyedState{}
	}
	return tr
}

// groupGlobal は global のイベントを t ごとにまとめ、各 t の中を (type, data) の昇順に並べる。
func groupGlobal(evs []Entry) []GlobalAt {
	out := []GlobalAt{}
	for _, e := range evs {
		if k := len(out); k > 0 && out[k-1].T == e.T {
			out[k-1].Events = append(out[k-1].Events, e)
			continue
		}
		// 入力は t の昇順（Engine の列）。昇順でない入力も、同じ t をまとめて比べられるようにする。
		if i := slices.IndexFunc(out, func(g GlobalAt) bool { return g.T == e.T }); i >= 0 {
			out[i].Events = append(out[i].Events, e)
			continue
		}
		out = append(out, GlobalAt{T: e.T, Events: []Entry{e}})
	}
	slices.SortStableFunc(out, func(a, b GlobalAt) int { return cmpInt(int64(a.T), int64(b.T)) })
	for i := range out {
		slices.SortFunc(out[i].Events, cmpEntry)
	}
	return out
}

// cmpEntry は (type, data) の順。
func cmpEntry(a, b Entry) int {
	if c := strings.Compare(string(a.Type), string(b.Type)); c != 0 {
		return c
	}
	return bytes.Compare(a.Data, b.Data)
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpNumericID は 10 進の ID を数として比べる（読めなければ文字列の比較）。
func cmpNumericID(a, b string) int {
	x, errA := strconv.ParseUint(a, 10, 64)
	y, errB := strconv.ParseUint(b, 10, 64)
	if errA == nil && errB == nil {
		return cmpUint(x, y)
	}
	return strings.Compare(a, b)
}

// mustMarshal は v を JSON にする。ドメインイベントの中身は必ず JSON にできるので、誤りは panic する。
func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("simtrace: marshal %T: %v", v, err))
	}
	return b
}

// mustCanonical は v の正準の JSON を返す。
func mustCanonical(v any) json.RawMessage { return canonical(mustMarshal(v)) }

// canonical は JSON を正準の形（オブジェクトのキーを辞書順、数は元の表記のまま、空白なし）にする。
// 構造体のフィールドの順・map の順・整形に依らず、同じ値は同じバイト列になる。
func canonical(raw json.RawMessage) json.RawMessage {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		panic(fmt.Sprintf("simtrace: canonical: %v", err))
	}
	return mustMarshal(v) // encoding/json は map のキーを辞書順に出す
}
