package sim

import (
	"errors"
	"fmt"
)

// ReplayFormat は、Replay が受け付ける入力ログの形式の版（DesignDoc §10.1 の format）。
const ReplayFormat = 1

// ReplayLog は、復元とリプレイに使う入力ログ（DesignDoc §10.1 のうち Engine が使う分）。
// JSON の読み書きと、プロトコルの型からの変換は、呼び出し側（T-301・T-302）が行う。
type ReplayLog struct {
	Format    int          // 入力ログの format。ReplayFormat だけを受け付ける
	StageHash string       // 記録したときのステージのハッシュ（stageHash。"sha256:…"）
	Seed      uint64       // ラウンドのシード
	Entries   []InputEntry // Engine のコマンドだけ。T の昇順（同じ T は記録の順）
	Until     SimTime      // 最後にこの時刻まで進める。0 なら最後のエントリの T（エントリがなければ 0）
}

var (
	ErrReplayFormat   = errors.New("sim: replay: unsupported input log format")
	ErrReplayStage    = errors.New("sim: replay: stage hash mismatch")
	ErrReplayOrder    = errors.New("sim: replay: input log is out of order")
	ErrReplayDiverged = errors.New("sim: replay: input log does not reproduce")
)

// Replay は、cfg と入力ログ in から Engine を作り直す（DesignDoc §10.1 のリプレイ、ADR-0017 の復元）。
// stageHash は、cfg を作ったステージのハッシュ（呼び出し側が計算した stage.Stage.Hash）。
// in.Seed で New し、各エントリについて RunUntil(T) → Apply(Cmd) を行い、最後に RunUntil(until) を行う。
// 失敗したら (nil, err)。err は ErrReplay… または New のエラーを包む（errors.Is で判定できる）。
// 返した Engine の DrainEvents には、New からの全イベントが残っている（取り出すかどうかは呼び出し側が決める）。
//
// 検査の順序は、形式 → ステージのハッシュ（空どうしでも一致とみなさない）→ 時刻の順 → New → 各エントリ。
// in.Entries には Engine のコマンドだけを入れる。セッションのコマンド（cmd.game.pause・cmd.game.speed・
// cmd.game.skip など）は呼び出し側が除く。知らない種類のコマンドは読み飛ばさず、ErrReplayDiverged にする。
//
// 復元の後、セッションは DrainEvents の結果を捨ててから Snapshot を送る（Snapshot().LastSeq 以下のイベントは
// スナップショットに含まれている）。TraceHash は DrainEvents に関係なく New からの全イベントが対象。
//
// Replay は panic を回復しない（Engine の誤りは panic になる）。回復は呼び出し側が行う。
// in.Entries は書き換えない。返した Engine は in と何も共有しない（Apply が入力ログに複製を入れる）。
func Replay(cfg Config, stageHash string, in ReplayLog, opt Options) (*Engine, error) {
	if in.Format != ReplayFormat {
		return nil, fmt.Errorf("%w: %d", ErrReplayFormat, in.Format)
	}
	if stageHash == "" || in.StageHash != stageHash {
		return nil, fmt.Errorf("%w: log %q, stage %q", ErrReplayStage, in.StageHash, stageHash)
	}
	for i, en := range in.Entries {
		if en.T < 0 {
			return nil, fmt.Errorf("%w: entry %d: t=%d < 0", ErrReplayOrder, i, en.T)
		}
		if i > 0 && en.T < in.Entries[i-1].T {
			return nil, fmt.Errorf("%w: entry %d: t=%d < t=%d of entry %d", ErrReplayOrder, i, en.T, in.Entries[i-1].T, i-1)
		}
	}
	var last SimTime
	if n := len(in.Entries); n > 0 {
		last = in.Entries[n-1].T
	}
	if in.Until < 0 {
		return nil, fmt.Errorf("%w: until=%d < 0", ErrReplayOrder, in.Until)
	}
	if in.Until != 0 && in.Until < last {
		return nil, fmt.Errorf("%w: until=%d < t=%d of entry %d", ErrReplayOrder, in.Until, last, len(in.Entries)-1)
	}

	e, err := New(cfg, in.Seed, opt)
	if err != nil {
		return nil, fmt.Errorf("sim: replay: %w", err)
	}
	for i, en := range in.Entries {
		e.RunUntil(en.T)
		ack := e.Apply(en.Cmd)
		if !ack.OK {
			return nil, fmt.Errorf("%w: entry %d (t=%d, %s): %s: %s", ErrReplayDiverged, i, en.T, en.Cmd.Type, ack.Code, ack.Message)
		}
		if ack.T != en.T {
			return nil, fmt.Errorf("%w: entry %d (%s): applied at t=%d, recorded t=%d", ErrReplayDiverged, i, en.Cmd.Type, ack.T, en.T)
		}
	}
	target := in.Until
	if target == 0 {
		target = last
	}
	e.RunUntil(target)
	return e, nil
}
