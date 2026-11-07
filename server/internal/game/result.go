package game

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// StoryKind は「今日の出来事」の型（台本）の種類（ゲームデザイン §7.5）。
type StoryKind string

const (
	StoryStorm           StoryKind = "storm"            // 嵐で航路が止まった
	StorySteppedDown     StoryKind = "stepped_down"     // 代表が退いた
	StoryLeaderElected   StoryKind = "leader_elected"   // 新しい代表が選ばれた（開会式の後）
	StoryFirstCommit     StoryKind = "first_commit"     // 最初に確定した依頼
	StoryRejected        StoryKind = "rejected"         // 最初の予約失敗
	StoryDuplicate       StoryKind = "duplicate"        // 最初の重複した記録（再送）
	StoryStaleIgnored    StoryKind = "stale_ignored"    // 最初に届いた古い任期の手紙
	StoryPreVoteRejected StoryKind = "prevote_rejected" // 最初に断られた立候補の相談
	StorySilence         StoryKind = "silence"          // 最長の音信不通
)

// stepped_down の理由（sim の node.stepped_down の reason）。
const (
	stepDownCheckQuorum = "check_quorum"
	stepDownHigherTerm  = "higher_term"
)

// 「今日の出来事」の種類ごとの上限（ゲームデザイン §7.5）。リザルト画面を読める長さにするための表示の決まりで、
// ゲームの調整値ではない（SatisfactionMax と同じく、ステージ定義に置かない）。
const (
	storyLimitRepeat = 2 // storm・stepped_down・leader_elected
	storyLimitOnce   = 1 // ほか
)

// storyLimit は種類 k の上限を返す。
func storyLimit(k StoryKind) int {
	switch k {
	case StoryStorm, StorySteppedDown, StoryLeaderElected:
		return storyLimitRepeat
	}
	return storyLimitOnce
}

// StoryFact は出来事 1 つの材料（sim がイベントを観察して作る）。使う欄は Kind ごとに決まる（ゲームデザイン §7.5）。
type StoryFact struct {
	T          int64 // 出来事の時刻（ゲーム内時間のミリ秒）
	Kind       StoryKind
	Island     uint64      // 主語の島
	Term       uint64      // leader_elected・stepped_down・stale_ignored
	Reason     string      // stepped_down: "check_quorum" | "higher_term"、rejected: 台帳の理由
	Resource   string      // first_commit・rejected・duplicate: 資源の ID
	Routes     [][2]uint64 // storm: 新しく止まった航路（各組は小さい島 ID が先。航路の定義の順）
	Isolated   []uint64    // storm: すべての航路が止まった島（ID の昇順）。無ければ空
	DurationMs int64       // silence: 音信不通の長さ
}

// StoryNames は文章に使う名前。
type StoryNames struct {
	Islands   []string          // 添字 = 島の ID − 1
	Resources map[string]string // 資源の ID → 表示名（ステージ定義の resources[].label）
}

// StoryLine は文章 1 項目（プロトコル §5.5 の story[] の 1 要素）。
type StoryLine struct {
	T        int64
	Text     string
	Concepts []string // nil にしない
}

// WriteStory は facts から文章を組み立てる。facts を T の昇順に安定に並べ替え（同じ T は渡した順）、
// 種類ごとの上限（storyLimit）を超えた分と、文にできない材料を捨てる。戻り値は nil にしない。facts を変えない。
func WriteStory(facts []StoryFact, names StoryNames) []StoryLine {
	sorted := slices.Clone(facts)
	slices.SortStableFunc(sorted, func(a, b StoryFact) int { return cmp.Compare(a.T, b.T) })
	out := []StoryLine{}
	var counts []storyCount // 種類ごとの数（出た順。map を走査しないためスライスで持つ）
	for _, f := range sorted {
		k := slices.IndexFunc(counts, func(c storyCount) bool { return c.kind == f.Kind })
		if k >= 0 && counts[k].n >= storyLimit(f.Kind) {
			continue
		}
		text, concepts, ok := storyText(f, names)
		if !ok {
			continue
		}
		if k < 0 {
			counts = append(counts, storyCount{kind: f.Kind})
			k = len(counts) - 1
		}
		counts[k].n++
		out = append(out, StoryLine{T: f.T, Text: text, Concepts: concepts})
	}
	return out
}

// storyCount は WriteStory が数える、種類ごとの文の数。
type storyCount struct {
	kind StoryKind
	n    int
}

// storyText は材料 f の文と concepts（新しいスライス）を返す。文にできなければ ok == false。
func storyText(f StoryFact, names StoryNames) (text string, concepts []string, ok bool) {
	island := names.island(f.Island)
	switch f.Kind {
	case StoryStorm:
		if len(f.Isolated) > 0 {
			ns := make([]string, len(f.Isolated))
			for i, id := range f.Isolated {
				ns[i] = names.island(id)
			}
			return "嵐で" + strings.Join(ns, "・") + "が孤立しました。", []string{"Network Partition"}, true
		}
		if len(f.Routes) > 0 {
			rs := make([]string, len(f.Routes))
			for i, r := range f.Routes {
				rs[i] = names.island(r[0]) + "と" + names.island(r[1]) + "の間"
			}
			return "嵐で" + strings.Join(rs, "、") + "の航路が止まりました。", []string{"Network Partition"}, true
		}
	case StorySteppedDown:
		switch f.Reason {
		case stepDownCheckQuorum:
			return island + "は過半数の島と連絡が取れなくなったので、自分から代表を降りました。", []string{"CheckQuorum"}, true
		case stepDownHigherTerm:
			return island + "は、もっと新しい任期（Term）の手紙を受け取ったので、代表を降りました。", []string{"Term"}, true
		}
	case StoryLeaderElected:
		return island + "が Term " + strconv.FormatUint(f.Term, 10) + " の新しい代表に選ばれました。", []string{"Leader Election"}, true
	case StoryFirstCommit:
		return island + "の" + names.resource(f.Resource) + "の依頼が過半数の島の台帳に記録され、共和国で確定しました。",
			[]string{"Log Replication", "Commit", "Quorum"}, true
	case StoryRejected:
		const same = "どの島の台帳も同じ順に記録するので、結果はどの島でも同じです。"
		switch f.Reason {
		case ledger.ReasonAlreadyReserved:
			return island + "の" + names.resource(f.Resource) + "の予約は、先に別の予約が確定していたので取れませんでした。" + same,
				[]string{"Replicated State Machine"}, true
		case ledger.ReasonFull:
			return island + "の" + names.resource(f.Resource) + "の予約は、先に確定した予約で枠が埋まっていたので取れませんでした。" + same,
				[]string{"Replicated State Machine"}, true
		}
	case StoryDuplicate:
		return island + "の役場が" + names.resource(f.Resource) + "の依頼を送り直したので、同じ依頼が台帳に 2 回記録されました。2 回目は処理済みとして扱われ、二重に予約されませんでした。",
			[]string{"Idempotency"}, true
	case StoryStaleIgnored:
		return island + "が出した古い任期（Term " + strconv.FormatUint(f.Term, 10) + "）の手紙が届きましたが、受け取った島は無視しました。", []string{"Term"}, true
	case StoryPreVoteRejected:
		return island + "は代表と連絡が取れず、立候補してよいかほかの島に相談しましたが、断られました。", []string{"PreVote"}, true
	case StorySilence:
		return island + "には " + strconv.FormatInt(f.DurationMs/1000, 10) + " 秒のあいだ、手紙が 1 通も届きませんでした。",
			[]string{"Heartbeat", "Election Timeout"}, true
	}
	return "", nil, false
}

// island は島 id の名前。0・範囲外・空の名前は「島 {ID}」。
func (n StoryNames) island(id uint64) string {
	if id >= 1 && id <= uint64(len(n.Islands)) {
		if s := n.Islands[id-1]; s != "" {
			return s
		}
	}
	return "島 " + strconv.FormatUint(id, 10)
}

// resource は資源 id の表示名。無い・空なら id のまま。
func (n StoryNames) resource(id string) string {
	if s := n.Resources[id]; s != "" {
		return s
	}
	return id
}

// TermRanges は、index 1 から順に並んだエントリの Term の列を、[term, 最初の index, 最後の index] の帯にする
// （プロトコル §5.5 の finalLogs[].terms）。terms[i] は index i+1 のエントリの Term。空なら空のスライス（nil にしない）。
func TermRanges(terms []uint64) [][3]uint64 {
	out := [][3]uint64{}
	for i, t := range terms {
		idx := uint64(i + 1)
		if n := len(out); n > 0 && out[n-1][0] == t {
			out[n-1][2] = idx
			continue
		}
		out = append(out, [3]uint64{t, idx, idx})
	}
	return out
}
