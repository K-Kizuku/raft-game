package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/K-Kizuku/raft-game/server/internal/replaykit"
)

// 入力ログ（DesignDoc §10.1）の読み書きは replaykit にある。ここはファイルへの書き込みだけを持つ。

// inputLogPath は --inputs-dir に書く入力ログのパス（DIR/<stageId>-<bot>-<seed>.json）。
func inputLogPath(dir, stageID, botName string, seed uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%s-%s-%d.json", stageID, botName, seed))
}

// writeInputLog は入力ログを dir に書く。dir がなければ作る。部分的なファイルを残さない。
func writeInputLog(dir, botName string, l replaykit.InputLog) error {
	data, err := replaykit.EncodeInputLog(l)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%s: ディレクトリを作れない: %w", dir, err)
	}
	return writeAtomic(inputLogPath(dir, l.StageID, botName, l.Seed), data)
}

// writeAtomic は、path と同じディレクトリの一時ファイルに書いてから置き換える（cmd/islandsdata と同じ方法）。
// 失敗したときは path を変えず、一時ファイルを消す。
func writeAtomic(path string, data []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("%s: 一時ファイルを作れない: %w", path, err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return fmt.Errorf("%s: 書けない: %w", tmp, err)
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return fmt.Errorf("%s: 権限を変えられない: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("%s: 置き換えられない: %w", path, err)
	}
	return nil
}
