package stage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// 表示用の値（T-201b）。シミュレーションに使う型（Stage・Island・IslandData・Islands）に混ぜず、
// islands.json のバイト列から別に読む。表示用の値はシミュレーションの判定に使わない。

// Measure は人口・面積の 1 つの値と出典（データ仕様 §3.1 の population・areaKm2）。
type Measure struct {
	Value      int64      // 人口は人。面積は 0.0001 km² 単位の整数（153.27 → 1532700）
	Text       string     // islands.json に書かれた数のまま（"25881"・"153.27"・"2"）。表示に使う
	AsOf       string     // 基準日（"令和8年4月1日"）
	Provenance Provenance // Fact か Derived
	Note       string     // "" = note がない
	Source     string     // sources のキー
}

// PlanText は計画書（D2）の 1 つの列の文章（データ仕様 §3.1 の plan.*）。
type PlanText struct {
	Key        string     // "geography"・"climate"・"populationHouseholds"・"maritime"・"ports"・"tourism"・"disaster"
	Field      string     // D2 の列名（Key に対応する列）
	Text       string     // そのまま（空白も除かない）
	Provenance Provenance // Fact か Derived
	Source     string     // sources のキー
}

// Plan は島の計画書の地域と文章。
type Plan struct {
	Region string     // plan.region（"" もありうる）
	Texts  []PlanText // planKeys の順。書いてあるものだけ。なければ nil
}

// IslandDisplay は 1 つの島の表示用の値。
type IslandDisplay struct {
	Ref            string // islands.json の id
	Name           string
	Prefecture     string // "" = 書いていない
	DesignatedArea string
	Municipalities []string // 1 つ以上。ファイルの順
	Population     Measure
	Area           Measure
	Plan           *Plan // nil = plan がない
}

// SourceInfo は出典（islands.json の sources の 1 項目）。
type SourceInfo struct {
	Key, Title, Publisher, License, TermsURL, PublishedAt string
}

// ExcerptDisplay は抜き出し（データ仕様 §3.4）の表示用の値。
type ExcerptDisplay struct {
	ID, Region, Field, Quote string
	Provenance               Provenance // Fact か Derived
	Value                    *int64     // nil = value がない
	Unit                     string     // value があるときだけ空でない
	Note                     string     // "" = note がない
	Source                   string     // sources のキー
}

// Display は islands.json の表示用の値（検証済み）。
type Display struct {
	Sources  []SourceInfo     // Key の昇順
	Islands  []IslandDisplay  // ファイルの順
	Excerpts []ExcerptDisplay // ファイルの順

	byID      map[string]int    // id → Islands の添字（検索だけに使う）
	byExcerpt map[[3]string]int // {region, field, quote} → Excerpts の添字（同上）
}

// planKeys は plan のキーと D2 の列（データ仕様 §3.1）。この順に Plan.Texts に入れる。
var planKeys = [...]struct{ key, field string }{
	{"geography", "GEO_LOCATION_AREA"},
	{"climate", "NATURAL_CLIMATE_FEATURES"},
	{"populationHouseholds", "POPULATION_HOUSEHOLDS"},
	{"maritime", "MARITIME_TRANSPORT"},
	{"ports", "PORTS"},
	{"tourism", "LOCAL_INDUSTRY_TOURISM"},
	{"disaster", "DISASTER_LAND_CONSERVATION"},
}

var jsonAreaRe = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,4})?$`)

// ParseDisplay は islands.json を読み、表示用の値を検証して返す。
// 先に ParseIslands(name, data) を呼び、誤りがあればそのエラーをそのまま返す（検査を重ねて書かない）。
// そのあと表示用の欄を検査し、誤りがあればすべての Issue を検査の順に並べた *Error（File は name）を返す。
// data は変えない。stage が読まないキーは無視する。
func ParseDisplay(name string, data []byte) (*Display, error) {
	is, err := ParseIslands(name, data)
	if err != nil {
		return nil, err
	}
	p := &issues{}
	top, ok := decodeJSONTop(p, data)
	if !ok { // ParseIslands が通ったので起こらない
		return nil, &Error{File: name, Issues: p.list}
	}
	d := &Display{byID: map[string]int{}, byExcerpt: map[[3]string]int{}}
	sources := map[string]bool{}
	for _, k := range is.Sources {
		sources[k] = true
	}

	// sources（is.Sources はキーの昇順）
	var rawSources map[string]json.RawMessage
	_ = json.Unmarshal(top["sources"], &rawSources) // ParseIslands が検査済み
	keys := slices.Clone(is.Sources)
	slices.Sort(keys)
	for _, k := range keys {
		path := "sources." + k
		obj, ok := jsonObject(rawSources[k])
		if !ok {
			p.add(path, "オブジェクトにする")
			continue
		}
		s := SourceInfo{Key: k}
		s.Title, _ = jsonString(p, obj, path+".title", true)
		s.Publisher, _ = jsonString(p, obj, path+".publisher", true)
		s.License, _ = jsonString(p, obj, path+".license", true)
		s.TermsURL, _ = jsonString(p, obj, path+".termsUrl", true)
		s.PublishedAt, _ = jsonString(p, obj, path+".publishedAt", true)
		d.Sources = append(d.Sources, s)
	}

	// islands（ParseIslands が通ったので、どれもオブジェクトで、is.Islands と添字が揃う）
	var rawIslands []json.RawMessage
	_ = json.Unmarshal(top["islands"], &rawIslands)
	for i, raw := range rawIslands {
		path := fmt.Sprintf("islands[%d]", i)
		obj, _ := jsonObject(raw)
		base := is.Islands[i]
		isl := IslandDisplay{Ref: base.ID, Name: base.Name, Prefecture: base.Prefecture}
		isl.DesignatedArea, _ = jsonString(p, obj, path+".designatedArea", true)
		isl.Municipalities = parseMunicipalities(p, obj, path+".municipalities")

		popObj, _ := jsonObject(obj["population"])
		isl.Population = parseMeasureMeta(p, popObj, path+".population", sources)
		isl.Population.Value = base.Population
		isl.Population.Text = strings.TrimSpace(string(popObj["value"]))

		areaPath := path + ".areaKm2"
		if rawArea, has := obj["areaKm2"]; !has {
			p.add(areaPath, "必須の欄がない")
		} else if areaObj, ok := jsonObject(rawArea); !ok {
			p.add(areaPath, "オブジェクトにする")
		} else {
			var v int64
			var text string
			if rawV, has := areaObj["value"]; !has {
				p.add(areaPath+".value", "必須の欄がない")
			} else if v, ok = jsonAreaTenThousandths(rawV); !ok {
				p.add(areaPath+".value", fmt.Sprintf("小数第 4 位までの 0 以上の数にする（値: %s）", rawV))
			} else {
				text = strings.TrimSpace(string(rawV))
			}
			isl.Area = parseMeasureMeta(p, areaObj, areaPath, sources)
			isl.Area.Value = v
			isl.Area.Text = text
		}

		if rawPlan, has := obj["plan"]; has {
			planObj, _ := jsonObject(rawPlan) // ParseIslands が検査済み
			isl.Plan = &Plan{Region: base.PlanRegion}
			for _, pk := range planKeys {
				rawText, has := planObj[pk.key]
				if !has {
					continue
				}
				tpath := path + ".plan." + pk.key
				tobj, ok := jsonObject(rawText)
				if !ok {
					p.add(tpath, "オブジェクトにする")
					continue
				}
				t := PlanText{Key: pk.key, Field: pk.field}
				t.Text, _ = jsonString(p, tobj, tpath+".text", true)
				t.Provenance = parseProvenance(p, tobj, tpath+".provenance")
				t.Source = parseSourceKey(p, tobj, tpath+".source", sources)
				if f, ok := jsonString(p, tobj, tpath+".field", true); ok && f != pk.field {
					p.add(tpath+".field", fmt.Sprintf("field は %s にする（値: %q）", pk.field, f))
				}
				isl.Plan.Texts = append(isl.Plan.Texts, t)
			}
		}

		d.byID[isl.Ref] = len(d.Islands)
		d.Islands = append(d.Islands, isl)
	}

	// excerpts（なくてもよい。ParseIslands が通ったので、どれもオブジェクトで、is.Excerpts と添字が揃う）
	if rawList, has := top["excerpts"]; has {
		var list []json.RawMessage
		_ = json.Unmarshal(rawList, &list)
		for i, raw := range list {
			path := fmt.Sprintf("excerpts[%d]", i)
			obj, _ := jsonObject(raw)
			base := is.Excerpts[i]
			e := ExcerptDisplay{ID: base.ID, Region: base.Region, Field: base.Field, Quote: base.Quote}
			e.Provenance = parseProvenance(p, obj, path+".provenance")
			rawV, hasValue := obj["value"]
			if hasValue {
				if v, ok := jsonUint(rawV); ok {
					e.Value = &v
				} else {
					p.add(path+".value", fmt.Sprintf("0 以上の 10 進の整数にする（値: %s）", rawV))
				}
			}
			_, hasUnit := obj["unit"]
			switch {
			case hasValue && !hasUnit:
				p.add(path+".unit", "value があるときは必須")
			case hasValue:
				e.Unit, _ = jsonString(p, obj, path+".unit", true)
			case hasUnit:
				p.add(path+".unit", "value がないときは書かない")
			}
			e.Note, _ = jsonString(p, obj, path+".note", false)
			e.Source = parseSourceKey(p, obj, path+".source", sources)

			k := [3]string{e.Region, e.Field, e.Quote}
			if _, dup := d.byExcerpt[k]; !dup {
				d.byExcerpt[k] = len(d.Excerpts)
			}
			d.Excerpts = append(d.Excerpts, e)
		}
	}

	if len(p.list) > 0 {
		return nil, &Error{File: name, Issues: p.list}
	}
	return d, nil
}

// LoadDisplay は fsys の islandsPath を読んで ParseDisplay に渡す。
func LoadDisplay(fsys fs.FS, islandsPath string) (*Display, error) {
	data, err := fs.ReadFile(fsys, islandsPath)
	if err != nil {
		return nil, fileError(islandsPath, "", "読めない: "+err.Error())
	}
	return ParseDisplay(islandsPath, data)
}

// Lookup は id の島を返す。返す値の Municipalities・Plan は複製（呼び出し側が変えてもよい）。
func (d *Display) Lookup(ref string) (IslandDisplay, bool) {
	i, ok := d.byID[ref]
	if !ok {
		return IslandDisplay{}, false
	}
	return d.Islands[i].clone(), true
}

// ForStage は st の島（ノード ID の順 = st.Islands の順）の表示用の値を返す。新しいスライスで、
// 要素は Lookup と同じく複製。st の島の ref が d にない、または d の Name・Population.Value が
// st の島の Name・Population と違う（別の islands.json を読んだ）ときはエラー。
func (d *Display) ForStage(st *Stage) ([]IslandDisplay, error) {
	if st == nil {
		return nil, fmt.Errorf("stage: ForStage: nil stage")
	}
	out := make([]IslandDisplay, len(st.Islands))
	for i, isl := range st.Islands {
		v, ok := d.Lookup(isl.Ref)
		if !ok {
			return nil, fmt.Errorf("stage %s: islands[%d]: %s が islands.json の表示用の値にない", st.ID, i, isl.Ref)
		}
		if v.Name != isl.Name || v.Population.Value != isl.Population {
			return nil, fmt.Errorf("stage %s: islands[%d]: %s の name・population.value が、ステージを読んだ islands.json と違う", st.ID, i, isl.Ref)
		}
		out[i] = v
	}
	return out, nil
}

// Excerpt は、ステージ定義の source（Quote が空でないもの）が指す抜き出しを返す。
// src が nil・Quote が空・見つからないときは false。
func (d *Display) Excerpt(src *Source) (ExcerptDisplay, bool) {
	if src == nil || src.Quote == "" {
		return ExcerptDisplay{}, false
	}
	i, ok := d.byExcerpt[[3]string{src.Region, src.Field, src.Quote}]
	if !ok {
		return ExcerptDisplay{}, false
	}
	e := d.Excerpts[i]
	if e.Value != nil {
		v := *e.Value
		e.Value = &v
	}
	return e, true
}

// MunicipalityLabel は市町村を「・」でつないだ 1 つの文字列（"小豆島町・土庄町"）。
// スナップショットの municipality（プロトコル §5.1）に使う。
func (i IslandDisplay) MunicipalityLabel() string {
	return strings.Join(i.Municipalities, "・")
}

// clone は Municipalities・Plan を複製した値を返す。
func (i IslandDisplay) clone() IslandDisplay {
	i.Municipalities = slices.Clone(i.Municipalities)
	if i.Plan != nil {
		pl := *i.Plan
		pl.Texts = slices.Clone(pl.Texts)
		i.Plan = &pl
	}
	return i
}

// parseMunicipalities は municipalities（1 つ以上の空でない文字列。重複なし）を読む。
func parseMunicipalities(p *issues, obj map[string]json.RawMessage, path string) []string {
	raw, has := obj["municipalities"]
	var list []json.RawMessage
	if !has || !isJSONArray(raw) || json.Unmarshal(raw, &list) != nil || len(list) == 0 {
		p.add(path, "1 つ以上の市町村の配列にする")
		return nil
	}
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	for j, r := range list {
		epath := fmt.Sprintf("%s[%d]", path, j)
		t := bytes.TrimSpace(r)
		var s string
		if len(t) == 0 || t[0] != '"' || json.Unmarshal(t, &s) != nil {
			p.add(epath, "文字列にする")
			continue
		}
		if s == "" {
			p.add(epath, "空にしない")
			continue
		}
		if seen[s] {
			p.add(epath, fmt.Sprintf("%q が重複している", s))
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// parseMeasureMeta は population・areaKm2 の asOf・provenance・note・source を読む（value は呼び出し側）。
func parseMeasureMeta(p *issues, obj map[string]json.RawMessage, path string, sources map[string]bool) Measure {
	var m Measure
	m.AsOf, _ = jsonString(p, obj, path+".asOf", true)
	m.Provenance = parseProvenance(p, obj, path+".provenance")
	m.Note, _ = jsonString(p, obj, path+".note", false)
	m.Source = parseSourceKey(p, obj, path+".source", sources)
	return m
}

// parseProvenance は fact か derived の provenance を読む。
func parseProvenance(p *issues, obj map[string]json.RawMessage, path string) Provenance {
	s, ok := jsonString(p, obj, path, true)
	if !ok {
		return ""
	}
	switch v := Provenance(s); v {
	case Fact, Derived:
		return v
	}
	p.add(path, fmt.Sprintf("fact か derived にする（値: %q）", s))
	return ""
}

// parseSourceKey は sources のキーを指す source を読む。
func parseSourceKey(p *issues, obj map[string]json.RawMessage, path string, sources map[string]bool) string {
	s, ok := jsonString(p, obj, path, true)
	if !ok {
		return ""
	}
	if !sources[s] {
		p.add(path, fmt.Sprintf("sources にない（値: %q）", s))
		return ""
	}
	return s
}

// jsonAreaTenThousandths は面積を 0.0001 km² 単位の整数にする（153.27 → 1532700、2 → 20000）。
func jsonAreaTenThousandths(raw json.RawMessage) (int64, bool) {
	t := bytes.TrimSpace(raw)
	if !jsonAreaRe.Match(t) {
		return 0, false
	}
	intPart, frac, _ := bytes.Cut(t, []byte("."))
	ip, err := strconv.ParseInt(string(intPart), 10, 64)
	if err != nil || ip > maxInt {
		return 0, false
	}
	var fp int64
	for k := range 4 {
		fp *= 10
		if k < len(frac) {
			fp += int64(frac[k] - '0')
		}
	}
	return ip*10000 + fp, true
}
