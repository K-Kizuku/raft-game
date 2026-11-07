package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// errNoData は、--data（DATA_DIR）がなく、ステージ定義も埋め込んでいないこと。
var errNoData = errors.New("ステージ定義がない: --data（DATA_DIR）を指定するか、make sync-data の後に -tags embeddata でビルドする")

// OpenData は、dataDir が空でなければ os.DirFS(dataDir) を、空なら stage.Embedded() を返す。
// desc はログ用の説明（"dir:<絶対パス>" か "embedded"）。
// dataDir がない・ディレクトリでない、または空で埋め込みもないときはエラー。
func OpenData(dataDir string) (fsys fs.FS, desc string, err error) {
	if dataDir == "" {
		sub, ok := stage.Embedded()
		if !ok {
			return nil, "", errNoData
		}
		return sub, "embedded", nil
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, "", fmt.Errorf("--data（DATA_DIR）%q: %w", dataDir, err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, "", fmt.Errorf("--data（DATA_DIR）%q: %w", dataDir, err)
	}
	if !fi.IsDir() {
		return nil, "", fmt.Errorf("--data（DATA_DIR）%q: ディレクトリでない", dataDir)
	}
	return os.DirFS(abs), "dir:" + abs, nil
}

// Catalog は、読み込みと検査を済ませたステージ定義の一覧。作った後は変わらない（並行に読んでよい）。
type Catalog struct {
	stages   []*stage.Stage
	byID     map[string]*stage.Stage
	warnings []string
}

// LoadCatalog は stage.LoadDir(fsys, "islands.json", "stages") で読み、すべてのステージで
// stage.SimConfig が誤りにならないことを確かめる。誤りがあれば起動を止めるためのエラーを返す。
// ステージが 0 個でもエラーにしない。
func LoadCatalog(fsys fs.FS) (*Catalog, error) {
	stages, err := stage.LoadDir(fsys, "islands.json", "stages")
	if err != nil {
		return nil, fmt.Errorf("ステージ定義を読めない:\n%w", err)
	}
	c := &Catalog{byID: make(map[string]*stage.Stage, len(stages))}
	for _, st := range stages {
		if _, err := stage.SimConfig(st); err != nil {
			return nil, fmt.Errorf("stage %s: %w", st.ID, err)
		}
		for _, w := range st.Warnings {
			if w.Path == "" {
				c.warnings = append(c.warnings, st.ID+": "+w.Message)
			} else {
				c.warnings = append(c.warnings, st.ID+": "+w.Path+": "+w.Message)
			}
		}
		c.stages = append(c.stages, st)
		c.byID[st.ID] = st
	}
	return c, nil
}

// Stages は、LoadDir の順（ファイル名の順）のステージを新しいスライスで返す。
func (c *Catalog) Stages() []*stage.Stage {
	out := make([]*stage.Stage, len(c.stages))
	copy(out, c.stages)
	return out
}

// Lookup は id のステージを返す。
func (c *Catalog) Lookup(id string) (*stage.Stage, bool) {
	st, ok := c.byID[id]
	return st, ok
}

// Warnings は、ステージの検査の警告を "<stageId>: <Path>: <Message>" の形で返す（Path が空なら省く）。
func (c *Catalog) Warnings() []string {
	out := make([]string, len(c.warnings))
	copy(out, c.warnings)
	return out
}
