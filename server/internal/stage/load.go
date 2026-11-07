package stage

import (
	"errors"
	"io/fs"
	"path"
	"strings"
)

// LoadDir は fsys の islandsPath の islands.json と、stagesDir の直下の *.yaml を読んで検証する。
// 呼び出し側が os.DirFS か embed.FS を渡す。ステージはファイルの名前の順に返す。
// 誤りがあれば、ファイルごとの *Error を errors.Join でまとめて返す（ファイルの名前の順）。
func LoadDir(fsys fs.FS, islandsPath, stagesDir string) ([]*Stage, error) {
	islandsData, err := fs.ReadFile(fsys, islandsPath)
	if err != nil {
		return nil, fileError(islandsPath, "", "読めない: "+err.Error())
	}
	is, err := ParseIslands(islandsPath, islandsData)
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(fsys, stagesDir)
	if err != nil {
		return nil, fileError(stagesDir, "", "ディレクトリを読めない: "+err.Error())
	}

	type file struct {
		name  string // ファイル名（stagesDir を含まない）
		path  string
		stage *Stage
		err   *Error
	}
	var files []*file
	for _, e := range entries { // fs.ReadDir は名前の昇順
		if !e.Type().IsRegular() {
			continue
		}
		name := e.Name()
		f := &file{name: name, path: path.Join(stagesDir, name)}
		switch {
		case strings.HasSuffix(name, ".yml"):
			f.err = fileError(f.path, "", "拡張子は .yaml にする")
		case strings.HasSuffix(name, ".yaml"):
			data, rerr := fs.ReadFile(fsys, f.path)
			if rerr != nil {
				f.err = fileError(f.path, "", "読めない: "+rerr.Error())
				break
			}
			st, perr := Parse(f.path, data, is)
			if perr != nil {
				var se *Error
				if !errors.As(perr, &se) {
					se = fileError(f.path, "", perr.Error())
				}
				f.err = se
				break
			}
			f.stage = st
		default:
			continue // .gitkeep など
		}
		files = append(files, f)
	}

	// ファイルをまたぐ検査。id がファイル名と一致するステージだけを unlock の参照先にする。
	byID := map[string]*Stage{}
	for _, f := range files {
		if f.stage == nil {
			continue
		}
		if want := strings.TrimSuffix(f.name, ".yaml"); f.stage.ID != want {
			f.err = &Error{File: f.path, Issues: []Issue{{Path: "id", Message: "ファイル名（" + want + "）と一致しない（値: " + f.stage.ID + "）"}}}
			continue
		}
		byID[f.stage.ID] = f.stage
	}
	for _, f := range files {
		if f.stage == nil || f.stage.Unlock == "" {
			continue
		}
		var msg string
		if _, ok := byID[f.stage.Unlock]; !ok {
			msg = "読めたステージの id にない（値: " + f.stage.Unlock + "）"
		} else if inUnlockCycle(f.stage, byID, len(files)) {
			msg = "unlock をたどると循環する"
		} else {
			continue
		}
		if f.err == nil {
			f.err = &Error{File: f.path}
		}
		f.err.Issues = append(f.err.Issues, Issue{Path: "unlock", Message: msg})
	}

	var errs []error
	stages := []*Stage{}
	for _, f := range files {
		if f.err != nil {
			errs = append(errs, f.err)
			continue
		}
		stages = append(stages, f.stage)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return stages, nil
}

// inUnlockCycle は、st から unlock をたどって st に戻るかどうかを返す（たどるのは最大 limit 回）。
func inUnlockCycle(st *Stage, byID map[string]*Stage, limit int) bool {
	cur := st
	for range limit {
		next, ok := byID[cur.Unlock]
		if !ok {
			return false
		}
		if next == st {
			return true
		}
		cur = next
	}
	return false
}
