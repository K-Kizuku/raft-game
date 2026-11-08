package simtrace

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// Diff の Section。
const (
	SectionStream = "stream" // 島・船の列（§5.3 の 1）
	SectionGlobal = "global" // global の累積しないイベント（§5.3 の 2）
	SectionScore  = "score"  // score.updated（§5.3 の 3）
	SectionFinal  = "final"  // 最後の結果（§5.3 の 4）
)

// FinalT は、Section が final の Diff の T（時刻を持たない）。
const FinalT sim.SimTime = -1

// Diff は 2 つの正規化トレースの最初の食い違い。
type Diff struct {
	Section string      `json:"section"`
	Where   string      `json:"where"` // 列の名前、"global"・"score"、final では "result"・"ledger:<島>"・"request:<id>"・"island:<id>"
	T       sim.SimTime `json:"t"`     // 食い違った時刻。final では FinalT
	Index   int         `json:"index"` // 列・その時刻の集合・台帳の中の位置。比べる位置がなければ -1
	// Ref・Got は食い違った要素（Entry・中身など）の JSON。片方にしかなければ、もう片方は null。
	Ref    json.RawMessage `json:"ref"`
	Got    json.RawMessage `json:"got"`
	Reason string          `json:"reason"`
}

// String は 1 行の説明を返す。
func (d *Diff) String() string {
	if d == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s %s t=%d index=%d: %s (ref=%s got=%s)", d.Section, d.Where, d.T, d.Index, d.Reason, orNull(d.Ref), orNull(d.Got))
}

func orNull(b json.RawMessage) string {
	if len(b) == 0 {
		return "null"
	}
	return string(b)
}

// Compare は ref（参照モデル）と got（比べる相手）を §5.3 のとおりに比べ、一致すれば nil を返す。
// 食い違いが島・船の列・global・score にあれば、その中で t の最も早いものを返す（同じ t なら列 → global →
// score の順、列どうしは ref の列の順）。それらが一致したときだけ、最後の結果（round.result → 台帳 → 依頼 →
// 島の順）を比べる。
func Compare(ref, got Trace) *Diff {
	var best *Diff
	consider := func(d *Diff) {
		if d != nil && (best == nil || d.T < best.T) {
			best = d
		}
	}
	for _, d := range compareStreams(ref.Streams, got.Streams) {
		consider(d)
	}
	consider(compareGlobal(ref.Global, got.Global))
	consider(compareScore(ref.Score, got.Score))
	if best != nil {
		return best
	}
	return compareFinal(ref.Final, got.Final)
}

// compareStreams は列ごとの最初の食い違いを、ref の列の順（そのあと got にだけある列）に返す。
func compareStreams(ref, got []Stream) []*Diff {
	gotIdx := make(map[string]int, len(got)) // 検索だけ
	for i, s := range got {
		gotIdx[s.Name] = i
	}
	seen := make(map[string]bool, len(ref))
	var out []*Diff
	for _, r := range ref {
		seen[r.Name] = true
		var g []Entry
		if i, ok := gotIdx[r.Name]; ok {
			g = got[i].Events
		}
		if d := compareEntries(r.Name, r.Events, g); d != nil {
			out = append(out, d)
		}
	}
	for _, g := range got {
		if !seen[g.Name] {
			if d := compareEntries(g.Name, nil, g.Events); d != nil {
				out = append(out, d)
			}
		}
	}
	return out
}

// compareEntries は 1 本の列を先頭から比べる。
func compareEntries(name string, ref, got []Entry) *Diff {
	for i := range max(len(ref), len(got)) {
		switch {
		case i >= len(got):
			return &Diff{Section: SectionStream, Where: name, T: ref[i].T, Index: i, Ref: mustMarshal(ref[i]), Reason: "got の列が短い"}
		case i >= len(ref):
			return &Diff{Section: SectionStream, Where: name, T: got[i].T, Index: i, Got: mustMarshal(got[i]), Reason: "got の列が長い"}
		case !entryEqual(ref[i], got[i]):
			return &Diff{Section: SectionStream, Where: name, T: min(ref[i].T, got[i].T), Index: i,
				Ref: mustMarshal(ref[i]), Got: mustMarshal(got[i]), Reason: "(t, type, 中身) が違う"}
		}
	}
	return nil
}

func entryEqual(a, b Entry) bool {
	return a.T == b.T && a.Type == b.Type && bytes.Equal(a.Data, b.Data)
}

// compareGlobal は t ごとの多重集合を比べる。片方にしかない t は、その t の食い違いにする。
func compareGlobal(ref, got []GlobalAt) *Diff {
	i, j := 0, 0
	for i < len(ref) || j < len(got) {
		switch {
		case j >= len(got) || (i < len(ref) && ref[i].T < got[j].T):
			return &Diff{Section: SectionGlobal, Where: "global", T: ref[i].T, Index: 0, Ref: mustMarshal(ref[i].Events[0]),
				Reason: "got にこの時刻の global のイベントがない"}
		case i >= len(ref) || got[j].T < ref[i].T:
			return &Diff{Section: SectionGlobal, Where: "global", T: got[j].T, Index: 0, Got: mustMarshal(got[j].Events[0]),
				Reason: "ref にこの時刻の global のイベントがない"}
		}
		r, g := ref[i].Events, got[j].Events
		for k := range max(len(r), len(g)) {
			d := &Diff{Section: SectionGlobal, Where: "global", T: ref[i].T, Index: k}
			switch {
			case k >= len(g):
				d.Ref, d.Reason = mustMarshal(r[k]), "got のこの時刻の集合が小さい"
			case k >= len(r):
				d.Got, d.Reason = mustMarshal(g[k]), "got のこの時刻の集合が大きい"
			case !entryEqual(r[k], g[k]):
				d.Ref, d.Got, d.Reason = mustMarshal(r[k]), mustMarshal(g[k]), "この時刻の集合が違う（並べた位置の要素）"
			default:
				continue
			}
			return d
		}
		i++
		j++
	}
	return nil
}

// compareScore は t ごとの最後の score.updated を比べる。
func compareScore(ref, got []ScoreAt) *Diff {
	i, j := 0, 0
	for i < len(ref) || j < len(got) {
		switch {
		case j >= len(got) || (i < len(ref) && ref[i].T < got[j].T):
			return &Diff{Section: SectionScore, Where: "score", T: ref[i].T, Index: -1, Ref: ref[i].Data, Reason: "got にこの時刻の score.updated がない"}
		case i >= len(ref) || got[j].T < ref[i].T:
			return &Diff{Section: SectionScore, Where: "score", T: got[j].T, Index: -1, Got: got[j].Data, Reason: "ref にこの時刻の score.updated がない"}
		case !bytes.Equal(ref[i].Data, got[j].Data):
			return &Diff{Section: SectionScore, Where: "score", T: ref[i].T, Index: -1, Ref: ref[i].Data, Got: got[j].Data, Reason: "この時刻の最後の score.updated が違う"}
		}
		i++
		j++
	}
	return nil
}

// compareFinal は最後の結果を比べる。
func compareFinal(ref, got Final) *Diff {
	if !bytes.Equal(ref.Result, got.Result) {
		return &Diff{Section: SectionFinal, Where: "result", T: FinalT, Index: -1, Ref: ref.Result, Got: got.Result, Reason: "round.result が違う"}
	}
	if d := compareLedgers(ref.Ledgers, got.Ledgers); d != nil {
		return d
	}
	if d := compareKeyed("request:", ref.Requests, got.Requests); d != nil {
		return d
	}
	return compareKeyed("island:", ref.Islands, got.Islands)
}

func compareLedgers(ref, got []LedgerFinal) *Diff {
	for k := range max(len(ref), len(got)) {
		switch {
		case k >= len(got):
			return &Diff{Section: SectionFinal, Where: fmt.Sprintf("ledger:%d", ref[k].IslandID), T: FinalT, Index: -1, Ref: mustMarshal(ref[k]), Reason: "got にこの島の台帳がない"}
		case k >= len(ref):
			return &Diff{Section: SectionFinal, Where: fmt.Sprintf("ledger:%d", got[k].IslandID), T: FinalT, Index: -1, Got: mustMarshal(got[k]), Reason: "ref にこの島の台帳がない"}
		case ref[k].IslandID != got[k].IslandID:
			return &Diff{Section: SectionFinal, Where: fmt.Sprintf("ledger:%d", min(ref[k].IslandID, got[k].IslandID)), T: FinalT, Index: -1,
				Ref: mustMarshal(ref[k]), Got: mustMarshal(got[k]), Reason: "台帳のある島が違う"}
		}
		r, g := ref[k].Applied, got[k].Applied
		where := fmt.Sprintf("ledger:%d", ref[k].IslandID)
		for i := range max(len(r), len(g)) {
			d := &Diff{Section: SectionFinal, Where: where, T: FinalT, Index: i}
			switch {
			case i >= len(g):
				d.Ref, d.Reason = r[i], "got の台帳が短い"
			case i >= len(r):
				d.Got, d.Reason = g[i], "got の台帳が長い"
			case !bytes.Equal(r[i], g[i]):
				d.Ref, d.Got, d.Reason = r[i], g[i], "台帳の内容が違う"
			default:
				continue
			}
			return d
		}
	}
	return nil
}

// compareKeyed は ID の順に並んだ最終の状態を比べる。
func compareKeyed(prefix string, ref, got []KeyedState) *Diff {
	for k := range max(len(ref), len(got)) {
		switch {
		case k >= len(got):
			return &Diff{Section: SectionFinal, Where: prefix + ref[k].ID, T: FinalT, Index: -1, Ref: ref[k].State, Reason: "got にこの最終の状態がない"}
		case k >= len(ref):
			return &Diff{Section: SectionFinal, Where: prefix + got[k].ID, T: FinalT, Index: -1, Got: got[k].State, Reason: "ref にこの最終の状態がない"}
		case ref[k].ID != got[k].ID:
			return &Diff{Section: SectionFinal, Where: prefix + ref[k].ID, T: FinalT, Index: -1, Ref: ref[k].State, Got: got[k].State,
				Reason: fmt.Sprintf("ID が違う（got は %s）", got[k].ID)}
		case !bytes.Equal(ref[k].State, got[k].State):
			return &Diff{Section: SectionFinal, Where: prefix + ref[k].ID, T: FinalT, Index: -1, Ref: ref[k].State, Got: got[k].State, Reason: "最終の状態が違う"}
		}
	}
	return nil
}
