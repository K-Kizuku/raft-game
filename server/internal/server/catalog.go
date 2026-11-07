package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

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
	displays map[string][]stage.IslandDisplay // stage の id → 島の表示用の値（ノード ID の順）
	warnings []string
}

// LoadCatalog は stage.LoadDir(fsys, "islands.json", "stages") で読み、すべてのステージで
// stage.SimConfig が誤りにならないことを確かめる。続けて stage.LoadDisplay(fsys, "islands.json") で
// 島の表示用の値を読み、各ステージで ForStage を呼んで結果を持つ（T-201b）。
// 誤りがあれば起動を止めるためのエラーを返す。ステージが 0 個でもエラーにしない（LoadDisplay は呼ぶ）。
func LoadCatalog(fsys fs.FS) (*Catalog, error) {
	stages, err := stage.LoadDir(fsys, "islands.json", "stages")
	if err != nil {
		return nil, fmt.Errorf("ステージ定義を読めない:\n%w", err)
	}
	display, err := stage.LoadDisplay(fsys, "islands.json")
	if err != nil {
		return nil, fmt.Errorf("ステージ定義を読めない:\n%w", err)
	}
	c := &Catalog{
		byID:     make(map[string]*stage.Stage, len(stages)),
		displays: make(map[string][]stage.IslandDisplay, len(stages)),
	}
	for _, st := range stages {
		if _, err := stage.SimConfig(st); err != nil {
			return nil, fmt.Errorf("stage %s: %w", st.ID, err)
		}
		islands, err := display.ForStage(st)
		if err != nil {
			return nil, err
		}
		c.displays[st.ID] = islands
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

// islands は id のステージの島の表示用の値（ノード ID の順）を返す。返すたびに複製する
// （stage.Display.ForStage と同じく、Municipalities・Plan も新しい値。呼び出し側が変えてもよい）。
func (c *Catalog) islands(id string) ([]stage.IslandDisplay, bool) {
	src, ok := c.displays[id]
	if !ok {
		return nil, false
	}
	out := make([]stage.IslandDisplay, len(src))
	for i, isl := range src {
		isl.Municipalities = slices.Clone(isl.Municipalities)
		if isl.Plan != nil {
			pl := *isl.Plan
			pl.Texts = slices.Clone(pl.Texts)
			isl.Plan = &pl
		}
		out[i] = isl
	}
	return out, true
}

// Warnings は、ステージの検査の警告を "<stageId>: <Path>: <Message>" の形で返す（Path が空なら省く）。
func (c *Catalog) Warnings() []string {
	out := make([]string, len(c.warnings))
	copy(out, c.warnings)
	return out
}
