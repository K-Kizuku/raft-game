package sim

import (
	"errors"
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// CommandType はプレイヤーのコマンドの種類（プロトコル §4 の type）。
type CommandType string

const (
	CmdGameStart   CommandType = "cmd.game.start"
	CmdFerryLoad   CommandType = "cmd.ferry.load"
	CmdFerryUnload CommandType = "cmd.ferry.unload"
	CmdFerryDepart CommandType = "cmd.ferry.depart"
)

// Command はプレイヤーのコマンド（プロトコル §4 の type と payload を平らにしたもの）。
// Type は上の定数・CmdFerryAutoload（autoload.go）・CmdDemoTrigger（demo.go）のどれか。
type Command struct {
	Type      CommandType
	FerryID   string   // load・unload・depart・autoload
	LetterIDs []uint64 // load・unload
	To        uint64   // depart・autoload
	Event     string   // demo.trigger
}

// Ack はコマンドの結果（プロトコル §5 の ack から cmdId を除いたもの）。
type Ack struct {
	OK      bool
	T       SimTime // 適用した時刻（= Now()）。失敗でも入れる
	Code    string  // OK でないとき。下の定数
	Message string  // OK でないとき。err.Error() など
}

// エラーコード（プロトコル §4.1）。
const (
	CodeFerryNotDocked   = "ferry_not_docked"
	CodeFerryNotReady    = "ferry_not_ready"
	CodeCapacityExceeded = "capacity_exceeded"
	CodeLetterNotHere    = "letter_not_here"
	CodeNoRoute          = "no_route"
	CodeRouteBlocked     = "route_blocked"
	CodePhaseInvalid     = "phase_invalid"
	CodeBadRequest       = "bad_request"
)

// InputEntry は入力ログの 1 件（DesignDoc §10.1 の {t, cmd}）。
type InputEntry struct {
	T   SimTime
	Cmd Command
}

// cloneCommand は LetterIDs も複製したコマンドを返す。
func cloneCommand(c Command) Command {
	c.LetterIDs = slices.Clone(c.LetterIDs)
	return c
}

// ferryErrCodes は ferry のエラーとエラーコードの対応（表の順に errors.Is で判定する）。
var ferryErrCodes = []struct {
	err  error
	code string
}{
	{ferry.ErrNotDocked, CodeFerryNotDocked},
	{ferry.ErrNotReady, CodeFerryNotReady},
	{ferry.ErrCapacityExceeded, CodeCapacityExceeded},
	{ferry.ErrLetterNotHere, CodeLetterNotHere},
	{ferry.ErrNoRoute, CodeNoRoute},
	{ferry.ErrRouteBlocked, CodeRouteBlocked},
	{ferry.ErrUnknownFerry, CodeBadRequest},
	{ferry.ErrInvalidArgument, CodeBadRequest},
	{ferry.ErrRelayNotSupported, CodeBadRequest},
}

// ferryErrCode は ferry のエラーをエラーコードに変える。表にないエラーは Engine の誤りなので panic する。
func ferryErrCode(err error) string {
	for _, c := range ferryErrCodes {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	panic(fmt.Sprintf("sim: unexpected ferry error: %v", err))
}

// toLetterIDs は []uint64 を []ferry.LetterID に変える。
func toLetterIDs(ids []uint64) []ferry.LetterID {
	out := make([]ferry.LetterID, len(ids))
	for i, id := range ids {
		out[i] = ferry.LetterID(id)
	}
	return out
}
