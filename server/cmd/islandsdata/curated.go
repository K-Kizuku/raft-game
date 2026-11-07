package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// curatedFile は data/curated/*.yaml（データ仕様 §3.4）。
type curatedFile struct {
	Format   yamlInt          `yaml:"format"`
	Excerpts []curatedExcerpt `yaml:"excerpts"`
}

type curatedExcerpt struct {
	ID         string  `yaml:"id"`
	Region     string  `yaml:"region"`
	Field      string  `yaml:"field"`
	Quote      string  `yaml:"quote"`
	Provenance string  `yaml:"provenance"`
	Value      yamlInt `yaml:"value"`
	Unit       string  `yaml:"unit"`
	Note       string  `yaml:"note"`
}

// excerptOut は islands.json の excerpts の要素。フィールドの順がキーの順になる。
type excerptOut struct {
	ID         string      `json:"id"`
	Region     string      `json:"region"`
	Field      string      `json:"field"`
	Quote      string      `json:"quote"`
	Provenance string      `json:"provenance"`
	Value      json.Number `json:"value,omitempty"`
	Unit       string      `json:"unit,omitempty"`
	Note       string      `json:"note,omitempty"`
	Source     string      `json:"source"`
}

var excerptIDRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// loadCurated は dir の直下の *.yaml をファイル名の昇順で読み、D2 と照合して検証する。
// 結果は id の昇順。
func loadCurated(dir string, d2 *planIndex, dataset string) ([]excerptOut, error) {
	entries, err := os.ReadDir(dir) // ファイル名の昇順
	if err != nil {
		return nil, fmt.Errorf("%s: 抜き出しのディレクトリを読めない: %w", dir, err)
	}
	out := []excerptOut{}
	seen := map[string]string{} // id → ファイルのパス
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if strings.HasSuffix(e.Name(), ".yml") {
			return nil, fmt.Errorf("%s: 拡張子が .yml（.yaml にする）", path)
		}
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := readInput(path)
		if err != nil {
			return nil, err
		}
		var f curatedFile
		if id, err := decodeStrictYAML(data, &f, "excerpts"); err != nil {
			return nil, fmt.Errorf("%s%s: %w", path, withID("抜き出し", id), err)
		}
		if !f.Format.set || f.Format.value != 1 {
			return nil, fmt.Errorf("%s: format が 1 ではない", path)
		}
		for i, x := range f.Excerpts {
			ctx := withID("抜き出し", x.ID)
			if ctx == "" {
				ctx = fmt.Sprintf("（excerpts の %d 番目）", i+1)
			}
			if err := validateExcerpt(x, d2, seen); err != nil {
				return nil, fmt.Errorf("%s%s: %w", path, ctx, err)
			}
			seen[x.ID] = path
			o := excerptOut{
				ID: x.ID, Region: x.Region, Field: x.Field, Quote: x.Quote,
				Provenance: x.Provenance, Unit: x.Unit, Note: x.Note, Source: dataset,
			}
			if x.Value.set {
				o.Value = json.Number(strconv.FormatInt(x.Value.value, 10))
			}
			out = append(out, o)
		}
	}
	slices.SortFunc(out, func(a, b excerptOut) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func validateExcerpt(x curatedExcerpt, d2 *planIndex, seen map[string]string) error {
	if !excerptIDRe.MatchString(x.ID) {
		return fmt.Errorf("id %q が形式（^[a-z0-9]+(-[a-z0-9]+)*$）に合わない", x.ID)
	}
	if other, dup := seen[x.ID]; dup {
		return fmt.Errorf("id が重複している（%s にもある）", other)
	}
	row, err := d2.region(x.Region)
	if err != nil {
		return err
	}
	if x.Field == colID || !d2.table.has(x.Field) {
		return fmt.Errorf("field %q が %s の列にない（%s は使えない）", x.Field, d2.table.path, colID)
	}
	if x.Quote == "" {
		return fmt.Errorf("quote が空")
	}
	if text := d2.table.get(row, x.Field); !strings.Contains(text, x.Quote) {
		return fmt.Errorf("%s:%d: %s の文章に quote %q が含まれない（正規化しないで比べる）", d2.table.path, row.line, x.Field, x.Quote)
	}
	if x.Provenance != "fact" && x.Provenance != "derived" {
		return fmt.Errorf("provenance %q が fact でも derived でもない", x.Provenance)
	}
	if x.Value.set && x.Unit == "" {
		return fmt.Errorf("value があるのに unit がない")
	}
	if !x.Value.set && x.Unit != "" {
		return fmt.Errorf("value がないのに unit がある")
	}
	return nil
}
