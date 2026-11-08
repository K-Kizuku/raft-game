package sim

import (
	"fmt"
	"math/rand/v2"
	"slices"

	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// DriftDef は台本の漂流瓶 1 つ（events[] の kind: drift、FR-EVT-05）。値はすべてステージ定義から渡す。
type DriftDef struct {
	WindowStart int64 // at と書いたときは WindowEnd と同じ
	WindowEnd   int64
	Letters     int   // 複製する手紙の数の上限（1 以上）
	MinAgeMs    int64 // この長さ以上前に配達した手紙だけを複製する（0 以上）
}

// 漂流瓶のスケジューラのイベントの種類。
// engine.go の iota（1〜）・score.go（300〜）・storm.go（400〜）・breakdown.go（500〜）・rush.go（600〜）と
// 重ならないよう 700 から振る。重なれば handleEvent の switch がコンパイルエラーになる。
const (
	kindDrift EventKind = 700 + iota // ClassWorldEvent。Data は帳簿の添字 int
)

// driftBook は漂流瓶の帳簿。定義が無ければゼロ値のまま（乱数列を作らず、配達も記録しない）。
type driftBook struct {
	rng  *rand.Rand    // Sub(seed, StreamDrift)。定義があるときだけ作る
	list []driftEntry  // 定義の順
	log  []driftRecord // 配達した Raft の手紙（配達の順）
}

// driftEntry は漂流瓶 1 つの帳簿の行。
type driftEntry struct {
	def  DriftDef
	at   SimTime
	done bool
}

// driftRecord は配達した Raft の手紙 1 通の記録。msg は ferry が持っていたメッセージで、読み取り専用（書き換えない）。
type driftRecord struct {
	deliveredAt SimTime
	from        uint64
	msg         *pb.Message
}

// initDrifts は台本の漂流瓶の時刻を決めて帳簿に入れる（New の中）。
// 定義がなければ乱数列を作らない。定義があれば Sub(seed, StreamDrift) を 1 本作り、定義の順に
// 時刻（WindowEnd > WindowStart のときだけ Int64N(幅 + 1)）を引く。残りはラウンドの途中の複製の選択に使う。
func (e *Engine) initDrifts(seed uint64) error {
	e.cfg.Drifts = slices.Clone(e.cfg.Drifts)
	if len(e.cfg.Drifts) == 0 {
		return nil
	}
	for i, d := range e.cfg.Drifts {
		p := fmt.Sprintf("sim: drifts[%d]", i)
		switch {
		case d.WindowStart < 0:
			return fmt.Errorf("%s.windowStart: %d < 0", p, d.WindowStart)
		case d.WindowEnd < d.WindowStart:
			return fmt.Errorf("%s.windowEnd: %d < windowStart %d", p, d.WindowEnd, d.WindowStart)
		case SimTime(d.WindowEnd) > e.cfg.DurationMs:
			return fmt.Errorf("%s.windowEnd: %d > durationMs %d", p, d.WindowEnd, e.cfg.DurationMs)
		case d.Letters < 1:
			return fmt.Errorf("%s.letters: %d < 1", p, d.Letters)
		case d.MinAgeMs < 0:
			return fmt.Errorf("%s.minAgeMs: %d < 0", p, d.MinAgeMs)
		}
	}
	e.drifts.rng = Sub(seed, StreamDrift)
	e.drifts.list = make([]driftEntry, len(e.cfg.Drifts))
	for i, d := range e.cfg.Drifts {
		at := d.WindowStart
		if d.WindowEnd > d.WindowStart {
			at += e.drifts.rng.Int64N(d.WindowEnd - d.WindowStart + 1)
		}
		e.drifts.list[i] = driftEntry{def: d, at: SimTime(at)}
	}
	return nil
}

// startDrifts は帳簿の漂流瓶を定義の順にスケジューラに積む（cmd.game.start）。
func (e *Engine) startDrifts() {
	for i, en := range e.drifts.list {
		e.push(en.at, ClassWorldEvent, kindDrift, i)
	}
}

// noteDriftCandidate は、配達した手紙 l が Raft の手紙なら、漂流瓶の候補として記録する（arrive の中）。
// 漂流瓶の定義が無ければ何もしない。依頼書は記録しない。Raft が受け付けたかどうかは問わない。
func (e *Engine) noteDriftCandidate(l ferry.Letter) {
	if e.drifts.rng == nil || l.Kind != ferry.LetterRaft {
		return
	}
	e.drifts.log = append(e.drifts.log, driftRecord{deliveredAt: e.Now(), from: l.From, msg: l.Raft})
}

// drift は漂流瓶 i。今から MinAgeMs 以上前に配達した Raft の手紙から、最大 Letters 通を重複なしで選び、
// 選んだ順に、その複製を元の送り主の港に元の宛先のまま置く（ネットワークでの重複・遅延と同じ扱い）。
// メッセージは proto.Clone した値をそのまま置き、書き換えない。候補が無ければ何もしない（乱数も引かない）。
func (e *Engine) drift(i int) {
	en := &e.drifts.list[i]
	en.done = true
	cutoff := e.Now() - SimTime(en.def.MinAgeMs)
	// 記録は配達の順（時刻の昇順）なので、候補は先頭からの連続した範囲。
	n := 0
	for n < len(e.drifts.log) && e.drifts.log[n].deliveredAt <= cutoff {
		n++
	}
	if n == 0 {
		return
	}
	idx := make([]int, n)
	for k := range idx {
		idx[k] = k
	}
	for range min(en.def.Letters, n) {
		p := e.drifts.rng.IntN(len(idx))
		rec := e.drifts.log[idx[p]]
		idx = slices.Delete(idx, p, p+1)
		isl := e.islands[rec.from-1]
		isl.Repost(int64(e.Now()), proto.Clone(rec.msg).(*pb.Message))
		e.drain(isl)
	}
}

// nextDriftAt は、まだ処理していない漂流瓶のうち、DurationMs 以下でいちばん早い時刻を返す（NextNotableAt が使う）。
func (e *Engine) nextDriftAt() (SimTime, bool) {
	var best SimTime
	found := false
	for _, en := range e.drifts.list {
		if en.done || en.at > e.cfg.DurationMs {
			continue
		}
		if !found || en.at < best {
			best, found = en.at, true
		}
	}
	return best, found
}
