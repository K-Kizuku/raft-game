package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// output は islands.json（データ仕様 §3.1）。フィールドの順がキーの順になる。
// sources だけは map で、encoding/json がキーを昇順に並べる。
type output struct {
	Format      int               `json:"format"`
	GeneratedAt string            `json:"generatedAt"`
	Sources     map[string]source `json:"sources"`
	Islands     []islandOut       `json:"islands"`
	Excerpts    []excerptOut      `json:"excerpts"`
}

type islandOut struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Prefecture     string      `json:"prefecture"`
	DesignatedArea string      `json:"designatedArea"`
	Municipalities []string    `json:"municipalities"`
	Population     valueOut    `json:"population"`
	AreaKm2        valueOut    `json:"areaKm2"`
	PopulationSqrt json.Number `json:"populationSqrt"`
	Plan           *planOut    `json:"plan,omitempty"`
}

type valueOut struct {
	Value      json.Number `json:"value"`
	AsOf       string      `json:"asOf"`
	Provenance string      `json:"provenance"`
	Note       string      `json:"note,omitempty"`
	Source     string      `json:"source"`
}

type planOut struct {
	Region               string   `json:"region"`
	Geography            *textOut `json:"geography,omitempty"`
	Climate              *textOut `json:"climate,omitempty"`
	PopulationHouseholds *textOut `json:"populationHouseholds,omitempty"`
	Maritime             *textOut `json:"maritime,omitempty"`
	Ports                *textOut `json:"ports,omitempty"`
	Tourism              *textOut `json:"tourism,omitempty"`
	Disaster             *textOut `json:"disaster,omitempty"`
}

// slot は、planFields のキーに対応するフィールドを返す。
func (p *planOut) slot(key string) **textOut {
	switch key {
	case "geography":
		return &p.Geography
	case "climate":
		return &p.Climate
	case "populationHouseholds":
		return &p.PopulationHouseholds
	case "maritime":
		return &p.Maritime
	case "ports":
		return &p.Ports
	case "tourism":
		return &p.Tourism
	case "disaster":
		return &p.Disaster
	}
	panic("islandsdata: 未知の plan のキー " + key)
}

type textOut struct {
	Text       string `json:"text"`
	Provenance string `json:"provenance"`
	Source     string `json:"source"`
	Field      string `json:"field"`
}

// build は入力を読んで検証し、islands.json のバイト列を返す。ファイルを書かない。
func build(rawDir, curatedDir, selectPath string) ([]byte, error) {
	b, _, err := generate(rawDir, curatedDir, selectPath)
	return b, err
}

// generate は build の本体。出力の構造も返す（島と抜き出しの数を出すため）。
func generate(rawDir, curatedDir, selectPath string) ([]byte, *output, error) {
	sel, err := loadSelect(selectPath)
	if err != nil {
		return nil, nil, err
	}
	d1, err := readCSV(filepath.Join(rawDir, d1File), d1Columns)
	if err != nil {
		return nil, nil, err
	}
	d2t, err := readCSV(filepath.Join(rawDir, d2File), d2Columns())
	if err != nil {
		return nil, nil, err
	}
	d2 := newPlanIndex(d2t)
	list, err := newIslandList(d1)
	if err != nil {
		return nil, nil, err
	}

	out := &output{
		Format:      1,
		GeneratedAt: sel.GeneratedAt,
		Sources:     sel.Sources,
		Islands:     []islandOut{},
	}
	for _, is := range sel.Islands {
		o, err := list.island(is, sel.Dataset)
		if err != nil {
			return nil, nil, err
		}
		if is.Plan != nil {
			p, err := d2.plan(is, sel.Dataset)
			if err != nil {
				return nil, nil, fmt.Errorf("%s（島 %s）: %w", selectPath, is.ID, err)
			}
			o.Plan = p
		}
		out.Islands = append(out.Islands, o)
	}
	out.Excerpts, err = loadCurated(curatedDir, d2, sel.Dataset)
	if err != nil {
		return nil, nil, err
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, nil, fmt.Errorf("JSON を作れない: %w", err)
	}
	return buf.Bytes(), out, nil
}

// d1Row は D1 の 1 行のうち、使う値。
type d1Row struct {
	line           int
	date           date
	referenceDate  string
	prefecture     string
	designatedArea string
	island         string
	municipality   string
	population     string
	area           string
}

// islandList は D1 の全行と、最新の基準日。
type islandList struct {
	path   string
	rows   []d1Row
	latest date
}

func newIslandList(t *table) (*islandList, error) {
	l := &islandList{path: t.path}
	if len(t.rows) == 0 {
		return nil, fmt.Errorf("%s: データの行がない", t.path)
	}
	for i, r := range t.rows {
		ref := t.get(r, colReferenceDate)
		d, err := parseWareki(ref)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %s: %w", t.path, r.line, colReferenceDate, err)
		}
		if i == 0 || l.latest.less(d) {
			l.latest = d
		}
		l.rows = append(l.rows, d1Row{
			line:           r.line,
			date:           d,
			referenceDate:  ref,
			prefecture:     strings.TrimSpace(t.get(r, colPrefecture)),
			designatedArea: strings.TrimSpace(t.get(r, colDesignatedArea)),
			island:         strings.TrimSpace(t.get(r, colIsland)),
			municipality:   strings.TrimSpace(t.get(r, colMunicipality)),
			population:     t.get(r, colPopulation),
			area:           t.get(r, colArea),
		})
	}
	return l, nil
}

// island は、選んだ島の最新の基準日の行を集め、人口・面積を作る。
func (l *islandList) island(is selectIsland, dataset string) (islandOut, error) {
	ctx := fmt.Sprintf("%s（島 %s・%s %s 指定地域 %s）", l.path, is.ID, is.Prefecture, is.Name, is.DesignatedArea)
	var rows []d1Row
	older := false
	for _, r := range l.rows {
		if r.prefecture != is.Prefecture || r.designatedArea != is.DesignatedArea || r.island != is.Name {
			continue
		}
		if r.date != l.latest {
			older = true
			continue
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		if older {
			return islandOut{}, fmt.Errorf("%s: 最新の基準日（%04d-%02d-%02d）の行がない（古い基準日の行だけがある）",
				ctx, l.latest.year, l.latest.month, l.latest.day)
		}
		var cands []string
		for _, r := range l.rows {
			if r.prefecture == is.Prefecture && r.island == is.Name && r.designatedArea != is.DesignatedArea &&
				!slices.Contains(cands, r.designatedArea) {
				cands = append(cands, r.designatedArea)
			}
		}
		if len(cands) > 0 {
			return islandOut{}, fmt.Errorf("%s: 行がない。指定地域の候補: %s", ctx, strings.Join(cands, "、"))
		}
		return islandOut{}, fmt.Errorf("%s: 行がない", ctx)
	}

	byMuni := map[string]d1Row{}
	for _, r := range rows {
		if prev, dup := byMuni[r.municipality]; dup {
			return islandOut{}, fmt.Errorf("%s: 自治体 %s の行が 2 つある（%d 行目と %d 行目）", ctx, r.municipality, prev.line, r.line)
		}
		byMuni[r.municipality] = r
	}
	var missing, extra []string
	for _, m := range is.Municipalities {
		if _, ok := byMuni[m]; !ok {
			missing = append(missing, m)
		}
	}
	for _, r := range rows {
		if !slices.Contains(is.Municipalities, r.municipality) {
			extra = append(extra, fmt.Sprintf("%s（%d 行目）", r.municipality, r.line))
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		return islandOut{}, fmt.Errorf("%s: 自治体が municipalities と違う。足りない: [%s]、余分: [%s]",
			ctx, strings.Join(missing, "、"), strings.Join(extra, "、"))
	}

	var popSum, areaSum int64
	var popParts, areaParts []string
	for _, m := range is.Municipalities {
		r := byMuni[m]
		pop, err := parsePopulation(r.population)
		if err != nil {
			return islandOut{}, fmt.Errorf("%s:%d: %s（島 %s）: %w", l.path, r.line, colPopulation, is.ID, err)
		}
		area, err := parseArea(r.area)
		if err != nil {
			return islandOut{}, fmt.Errorf("%s:%d: %s（島 %s）: %w", l.path, r.line, colArea, is.ID, err)
		}
		popSum += pop
		areaSum += area
		popParts = append(popParts, fmt.Sprintf("%s %s 人", m, formatThousands(pop)))
		areaParts = append(areaParts, fmt.Sprintf("%s %s km²", m, formatFixed(area, areaScale)))
	}

	asOf := rows[0].referenceDate
	o := islandOut{
		ID:             is.ID,
		Name:           is.Name,
		Prefecture:     is.Prefecture,
		DesignatedArea: is.DesignatedArea,
		Municipalities: append([]string{}, is.Municipalities...),
		Population: valueOut{
			Value: json.Number(strconv.FormatInt(popSum, 10)), AsOf: asOf, Provenance: "fact", Source: dataset,
		},
		AreaKm2: valueOut{
			Value: json.Number(formatFixed(areaSum, areaScale)), AsOf: asOf, Provenance: "fact", Source: dataset,
		},
		PopulationSqrt: json.Number(formatFixed(populationSqrt(popSum), sqrtScale)),
	}
	if len(is.Municipalities) > 1 {
		o.Population.Provenance = "derived"
		o.Population.Note = joinJa(popParts) + "の合計"
		o.AreaKm2.Provenance = "derived"
		o.AreaKm2.Note = joinJa(areaParts) + "の合計"
	}
	return o, nil
}

// joinJa は「A、BとC」の形につなぐ（最後の 1 つの前が「と」、ほかは「、」）。
func joinJa(parts []string) string {
	if len(parts) <= 1 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], "、") + "と" + parts[len(parts)-1]
}

// planIndex は D2 を計画の地域名で引く。
type planIndex struct {
	table *table
}

func newPlanIndex(t *table) *planIndex {
	return &planIndex{table: t}
}

// region は、DESIGNATED_REGION_NAME が name の行を返す。ちょうど 1 行でなければエラー。
func (p *planIndex) region(name string) (record, error) {
	var found []record
	for _, r := range p.table.rows {
		if strings.TrimSpace(p.table.get(r, colRegion)) == name {
			found = append(found, r)
		}
	}
	switch len(found) {
	case 0:
		return record{}, fmt.Errorf("%s: %s が %q の行がない", p.table.path, colRegion, name)
	case 1:
		return found[0], nil
	}
	lines := make([]string, len(found))
	for i, r := range found {
		lines[i] = strconv.Itoa(r.line)
	}
	return record{}, fmt.Errorf("%s:%s: %s が %q の行が %d 行ある（ちょうど 1 行にする）",
		p.table.path, strings.Join(lines, ","), colRegion, name, len(found))
}

// plan は島の計画書の文章を取り込む。
func (p *planIndex) plan(is selectIsland, dataset string) (*planOut, error) {
	r, err := p.region(is.Plan.Region)
	if err != nil {
		return nil, err
	}
	want := is.Plan.planIslandName(is)
	var names []string
	for n := range strings.SplitSeq(p.table.get(r, colIslandName), "、") {
		names = append(names, strings.TrimSpace(n))
	}
	if !slices.Contains(names, want) {
		return nil, fmt.Errorf("%s:%d: %s に %q がない（plan.islandName で書き方を合わせられる）",
			p.table.path, r.line, colIslandName, want)
	}
	out := &planOut{Region: is.Plan.Region}
	for _, f := range planFields {
		text := p.table.get(r, f.column)
		if strings.TrimSpace(text) == "" {
			continue
		}
		*out.slot(f.key) = &textOut{Text: text, Provenance: "fact", Source: dataset, Field: f.column}
	}
	return out, nil
}
