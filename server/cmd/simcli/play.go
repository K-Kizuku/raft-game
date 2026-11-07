package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/K-Kizuku/raft-game/server/internal/bot"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// seedResult は 1 シードの結果（run・replay・record が出す 1 行）。キーはフィールドの順に出る。
// Engine を作れなかったときは Seed・OK・Error だけを出す（ほかは nil か空で省く）。
type seedResult struct {
	Seed      uint64       `json:"seed"`
	OK        bool         `json:"ok"`
	Phase     sim.Phase    `json:"phase,omitempty"`
	T         *sim.SimTime `json:"t,omitempty"`
	Events    *int         `json:"events,omitempty"`
	Commands  *int         `json:"commands,omitempty"`
	TraceHash string       `json:"traceHash,omitempty"`
	Error     string       `json:"error,omitempty"`

	stacks [][]byte // panic のスタックトレース（JSON に出さない）
}

// fail は res を失敗にする。エラーの文は 1 行にする。
func (res *seedResult) fail(err error) {
	res.OK = false
	res.Error = oneLine(err.Error())
}

// observe は Engine の今の状態（フェーズ・時刻・件数・ハッシュ）を res に入れる。
func (res *seedResult) observe(e *sim.Engine, events, commands int) {
	t := e.Now()
	res.Phase = e.Phase()
	res.T = &t
	res.Events = &events
	res.Commands = &commands
	res.TraceHash = formatHash(e.TraceHash())
}

// writeLine は res を JSON の 1 行にして w に書く。
func (res seedResult) writeLine(w io.Writer) error {
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// formatHash はハッシュを "sha256:" + 64 桁の 16 進の小文字にする。
func formatHash(h [32]byte) string { return "sha256:" + hex.EncodeToString(h[:]) }

// oneLine は改行を空白に置き換える。
func oneLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " ")
}

// playSeed は st・seed・ボットで 1 ラウンドを回す。Engine とボットの panic を回復してエラーにする。
// 失敗しても、それまでの入力ログ（inputs）を返す（--inputs-dir に書くため）。
func playSeed(st *stage.Stage, seed uint64, botName string, invariants bool) (res seedResult, events []sim.DomainEvent, inputs []sim.InputEntry, err error) {
	return playSeedWith(st, seed, botName, invariants, nil)
}

// playSeedWith は playSeed の本体。afterNew が nil でなければ、Engine を作った直後（ボットを動かす前）に呼ぶ
// （record が Snapshot を取るため）。afterNew は Engine の状態とハッシュを変えてはならない。
func playSeedWith(st *stage.Stage, seed uint64, botName string, invariants bool, afterNew func(*sim.Engine)) (res seedResult, events []sim.DomainEvent, inputs []sim.InputEntry, err error) {
	res = seedResult{Seed: seed}
	e, err := newEngine(st, seed, invariants)
	if err != nil {
		res.fail(err)
		return res, nil, nil, err
	}
	b, err := bot.New(botName, seed)
	if err != nil {
		res.fail(err)
		return res, nil, nil, err
	}
	if afterNew != nil {
		afterNew(e)
	}
	stack, err := safely(func() error { return bot.Play(e, b) })
	if err == nil {
		switch {
		case e.Phase() != sim.PhaseEnded:
			err = fmt.Errorf("ラウンドが終わらない: phase %s（want %s）", e.Phase(), sim.PhaseEnded)
		case e.Now() != sim.SimTime(st.DurationMs):
			err = fmt.Errorf("ラウンドが終わらない: t %d が durationMs %d と違う", e.Now(), st.DurationMs)
		}
	}
	events = e.DrainEvents()
	inputs = e.InputLog()
	res.OK = true
	res.observe(e, len(events), len(inputs))
	if err != nil {
		res.fail(err)
		if len(stack) > 0 {
			res.stacks = append(res.stacks, stack)
		}
	}
	return res, events, inputs, err
}

// safely は f を呼び、panic を "panic: <値>" のエラーにする。スタックトレースは stack に入れる。
// f が panic しなければ、f のエラーをそのまま返し、stack は nil。
func safely(f func() error) (stack []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			stack = debug.Stack()
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return nil, f()
}

// inputsDiffer は 2 つの入力ログの最初に違う位置を返す（T・Cmd の各フィールド・LetterIDs の中身を比べる）。
// 同じなら -1。長さだけが違うときは短いほうの長さ。
func inputsDiffer(a, b []sim.InputEntry) int {
	for i := range min(len(a), len(b)) {
		x, y := a[i], b[i]
		if x.T != y.T || x.Cmd.Type != y.Cmd.Type || x.Cmd.FerryID != y.Cmd.FerryID ||
			x.Cmd.To != y.Cmd.To || !slices.Equal(x.Cmd.LetterIDs, y.Cmd.LetterIDs) {
			return i
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b))
	}
	return -1
}
