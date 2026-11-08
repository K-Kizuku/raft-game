package stage

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Islands は検証済みの islands.json。
type Islands struct {
	Sources  []string     // sources のキー（昇順）
	Islands  []IslandData // ファイルの順
	Excerpts []Excerpt    // ファイルの順

	sum      [sha256.Size]byte // ファイルのバイト列の SHA-256
	byID     map[string]int    // id → Islands の添字
	sources  map[string]bool
	regions  map[string]bool    // plan.region と excerpts[].region
	excerpts map[[3]string]bool // {region, field, quote}
}

// IslandData は islands.json の 1 つの島（stage が使う欄だけ）。
type IslandData struct {
	ID                  string
	Name                string
	Prefecture          string
	Population          int64
	PopulationSqrtCenti int64
	PlanRegion          string // plan がなければ ""
}

// Excerpt は islands.json の抜き出し（データ仕様 §3.4）。
type Excerpt struct {
	ID, Region, Field, Quote string
}

// Lookup は id の島を返す。
func (is *Islands) Lookup(id string) (IslandData, bool) {
	i, ok := is.byID[id]
	if !ok {
		return IslandData{}, false
	}
	return is.Islands[i], true
}

var (
	jsonUintRe = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	jsonSqrtRe = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,2})?$`)
)

// ParseIslands は islands.json（データ仕様 §3.1）を読んで検証する。stage が読まないキーは無視する。
// 数は浮動小数点を通さずに、JSON の文字列のまま読む。
func ParseIslands(name string, data []byte) (*Islands, error) {
	p := &issues{}
	top, ok := decodeJSONTop(p, data)
	if !ok {
		return nil, &Error{File: name, Issues: p.list}
	}
	is := &Islands{
		sum:      sha256.Sum256(data),
		byID:     map[string]int{},
		sources:  map[string]bool{},
		regions:  map[string]bool{},
		excerpts: map[[3]string]bool{},
	}

	// format
	if raw, ok := top["format"]; !ok {
		p.add("format", "必須の欄がない")
	} else if v, ok := jsonUint(raw); !ok || v != 1 {
		p.add("format", fmt.Sprintf("1（10 進の整数）にする（値: %s）", raw))
	}

	// sources
	var sources map[string]json.RawMessage
	if raw, ok := top["sources"]; !ok || !isJSONObject(raw) || json.Unmarshal(raw, &sources) != nil || len(sources) == 0 {
		p.add("sources", "1 つ以上のキーを持つオブジェクトにする")
	} else {
		for k := range sources {
			is.Sources = append(is.Sources, k)
			is.sources[k] = true
		}
		slices.Sort(is.Sources)
	}

	// islands
	var islands []json.RawMessage
	if raw, ok := top["islands"]; !ok || !isJSONArray(raw) || json.Unmarshal(raw, &islands) != nil || len(islands) == 0 {
		p.add("islands", "1 つ以上の島の配列にする")
	}
	for i, raw := range islands {
		path := fmt.Sprintf("islands[%d]", i)
		obj, ok := jsonObject(raw)
		if !ok {
			p.add(path, "オブジェクトにする")
			continue
		}
		var d IslandData
		d.ID, ok = jsonString(p, obj, path+".id", true)
		if ok {
			if _, dup := is.byID[d.ID]; dup {
				p.add(path+".id", fmt.Sprintf("id %q が重複している", d.ID))
			} else {
				is.byID[d.ID] = len(is.Islands)
			}
		}
		d.Name, _ = jsonString(p, obj, path+".name", true)
		d.Prefecture, _ = jsonString(p, obj, path+".prefecture", false)

		popOK := false
		popObj, ok := jsonObject(obj["population"])
		if raw, has := popObj["value"]; !ok || !has {
			p.add(path+".population.value", "必須の欄がない")
		} else if d.Population, popOK = jsonUint(raw); !popOK {
			p.add(path+".population.value", fmt.Sprintf("0 以上の 10 進の整数にする（値: %s）", raw))
		}
		if raw, has := obj["populationSqrt"]; !has {
			p.add(path+".populationSqrt", "必須の欄がない")
		} else if v, ok := jsonSqrtCenti(raw); !ok {
			p.add(path+".populationSqrt", fmt.Sprintf("小数第 2 位までの 0 以上の数にする（値: %s）", raw))
		} else {
			d.PopulationSqrtCenti = v
			if want := populationSqrtCenti(d.Population); popOK && v != want {
				p.add(path+".populationSqrt", fmt.Sprintf("人口 %d から計算した値 %s と合わない（値: %s）", d.Population, centiString(want), raw))
			}
		}
		if rawPlan, has := obj["plan"]; has {
			plan, ok := jsonObject(rawPlan)
			if !ok {
				p.add(path+".plan", "オブジェクトにする")
			} else if r, ok := jsonString(p, plan, path+".plan.region", false); ok {
				d.PlanRegion = r
				if r != "" {
					is.regions[r] = true
				}
			}
		}
		is.Islands = append(is.Islands, d)
	}

	// excerpts（なくてもよい）
	if raw, has := top["excerpts"]; has {
		var list []json.RawMessage
		if !isJSONArray(raw) || json.Unmarshal(raw, &list) != nil {
			p.add("excerpts", "配列にする")
		}
		for i, raw := range list {
			path := fmt.Sprintf("excerpts[%d]", i)
			obj, ok := jsonObject(raw)
			if !ok {
				p.add(path, "オブジェクトにする")
				continue
			}
			var e Excerpt
			e.ID, _ = jsonString(p, obj, path+".id", true)
			e.Region, _ = jsonString(p, obj, path+".region", true)
			e.Field, _ = jsonString(p, obj, path+".field", true)
			e.Quote, _ = jsonString(p, obj, path+".quote", true)
			is.Excerpts = append(is.Excerpts, e)
			if e.Region != "" {
				is.regions[e.Region] = true
			}
			is.excerpts[[3]string{e.Region, e.Field, e.Quote}] = true
		}
	}

	if len(p.list) > 0 {
		return nil, &Error{File: name, Issues: p.list}
	}
	return is, nil
}

// decodeJSONTop は data を 1 つの JSON のオブジェクトとして読む。
func decodeJSONTop(p *issues, data []byte) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var first json.RawMessage
	if err := dec.Decode(&first); err != nil {
		p.add("", "JSON として読めない: "+err.Error())
		return nil, false
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		p.add("", "1 つ目の値のあとに余計なものがある")
		return nil, false
	}
	top, ok := jsonObject(first)
	if !ok {
		p.add("", "オブジェクトにする")
		return nil, false
	}
	return top, true
}

func isJSONObject(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{'
}

func isJSONArray(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '['
}

// jsonObject は raw をオブジェクトとして読む（null・配列などは false）。
func jsonObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if !isJSONObject(raw) {
		return nil, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil, false
	}
	return m, true
}

// jsonString は obj の key（path の最後の要素）の文字列を読む。
// required なら空でないこと、そうでなければ、あれば文字列であることを確かめる。
func jsonString(p *issues, obj map[string]json.RawMessage, path string, required bool) (string, bool) {
	key := path[strings.LastIndexByte(path, '.')+1:]
	raw, has := obj[key]
	if !has {
		if required {
			p.add(path, "必須の欄がない")
			return "", false
		}
		return "", true
	}
	t := bytes.TrimSpace(raw)
	var s string
	if len(t) == 0 || t[0] != '"' || json.Unmarshal(t, &s) != nil {
		p.add(path, "文字列にする")
		return "", false
	}
	if required && s == "" {
		p.add(path, "空にしない")
		return "", false
	}
	return s, true
}

// jsonUint は raw が ^(0|[1-9][0-9]*)$ の JSON の数なら、その値を返す。
func jsonUint(raw json.RawMessage) (int64, bool) {
	t := bytes.TrimSpace(raw)
	if !jsonUintRe.Match(t) {
		return 0, false
	}
	v, err := strconv.ParseInt(string(t), 10, 64)
	return v, err == nil
}

// jsonSqrtCenti は populationSqrt を 0.01 単位の整数にする（160.88 → 16088、13 → 1300）。
func jsonSqrtCenti(raw json.RawMessage) (int64, bool) {
	t := bytes.TrimSpace(raw)
	if !jsonSqrtRe.Match(t) {
		return 0, false
	}
	intPart, frac, _ := bytes.Cut(t, []byte("."))
	ip, err := strconv.ParseInt(string(intPart), 10, 64)
	if err != nil || ip > (1<<62)/100 {
		return 0, false
	}
	var fp int64
	for k := range 2 {
		fp *= 10
		if k < len(frac) {
			fp += int64(frac[k] - '0')
		}
	}
	return ip*100 + fp, true
}

// populationSqrtCenti は人口の平方根を小数第 2 位に四捨五入し、0.01 単位の整数で返す
// （データ仕様 §3.1。ETL と同じ式）。x = 人口 × 10000、n = ⌊√x⌋、x > n² + n なら n + 1。
func populationSqrtCenti(pop int64) int64 {
	x := new(big.Int).Mul(big.NewInt(pop), big.NewInt(10000))
	n := new(big.Int).Sqrt(x)
	half := new(big.Int).Mul(n, n)
	half.Add(half, n)
	if x.Cmp(half) > 0 {
		n.Add(n, big.NewInt(1))
	}
	return n.Int64()
}

// centiString は 0.01 単位の整数を 10 進の文字列にする（16088 → "160.88"）。
func centiString(v int64) string {
	return fmt.Sprintf("%d.%02d", v/100, v%100)
}

// issues は Issue を検査の順に集める。
type issues struct {
	list []Issue
}

func (p *issues) add(path, msg string) {
	p.list = append(p.list, Issue{Path: path, Message: msg})
}
