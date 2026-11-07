package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// 入力のファイル名と列名（データ仕様 §2.3・§2.4）。ここ 1 か所にまとめる。
const (
	d1File = "ritou-list/01_ritoushinkoutaisakujisshichiikiichiran.csv"
	d2File = "shinkou-keikaku/01_zentaijouhou.csv"

	colReferenceDate  = "REFERENCE_DATE"
	colPrefecture     = "PREFECTURE_NAME_CODE"
	colDesignatedArea = "DESIGNATED_AREA_NAME_CODE"
	colIsland         = "ISLAND_NAME_CODE"
	colMunicipality   = "MUNICIPALITY_NAME_CODE"
	colPopulation     = "POPULATION"
	colArea           = "AREA"

	colRegion     = "DESIGNATED_REGION_NAME"
	colIslandName = "ISLAND_NAME"
	colID         = "_id"
)

// planField は、計画書の文章の列と、出力のキーの対応。
type planField struct {
	key    string
	column string
}

// planFields は出力の plan のキーの順でもある。
var planFields = []planField{
	{"geography", "GEO_LOCATION_AREA"},
	{"climate", "NATURAL_CLIMATE_FEATURES"},
	{"populationHouseholds", "POPULATION_HOUSEHOLDS"},
	{"maritime", "MARITIME_TRANSPORT"},
	{"ports", "PORTS"},
	{"tourism", "LOCAL_INDUSTRY_TOURISM"},
	{"disaster", "DISASTER_LAND_CONSERVATION"},
}

var d1Columns = []string{colReferenceDate, colPrefecture, colDesignatedArea, colIsland, colMunicipality, colPopulation, colArea}

func d2Columns() []string {
	cols := []string{colRegion, colIslandName}
	for _, f := range planFields {
		cols = append(cols, f.column)
	}
	return cols
}

// table は見出しで列を引ける CSV。
type table struct {
	path   string
	header []string
	index  map[string]int
	rows   []record
}

// record は CSV の 1 行。line はファイルの行番号（1 始まり）。
type record struct {
	line   int
	fields []string
}

func (t *table) get(r record, col string) string {
	return r.fields[t.index[col]]
}

func (t *table) has(col string) bool {
	_, ok := t.index[col]
	return ok
}

// readCSV は CSV を読む。先頭の BOM は外す。1 行目を見出しとし、required の列がなければエラーにする。
func readCSV(path string, required []string) (*table, error) {
	data, err := readInput(path)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%s:%d: UTF-8 ではない", path, invalidUTF8Line(data))
	}
	r := csv.NewReader(bytes.NewReader(data))
	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: 空のファイル（見出しの行がない）", path)
	}
	if err != nil {
		return nil, csvError(path, err)
	}
	t := &table{path: path, header: header, index: map[string]int{}}
	for i, name := range header {
		if _, dup := t.index[name]; dup {
			return nil, fmt.Errorf("%s:1: 見出しの列 %s が 2 つある", path, name)
		}
		t.index[name] = i
	}
	var missing []string
	for _, c := range required {
		if !t.has(c) {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s:1: 使う列がない: %s", path, strings.Join(missing, ", "))
	}
	for {
		fields, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, csvError(path, err)
		}
		line, _ := r.FieldPos(0)
		t.rows = append(t.rows, record{line: line, fields: fields})
	}
	return t, nil
}

// readInput は入力のファイルを読む。エラーのメッセージは「<パス>: 読めない: <理由>」。
func readInput(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return nil, fmt.Errorf("%s: 読めない: %w", path, err)
	}
	return data, nil
}

func csvError(path string, err error) error {
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s:%d: CSV を読めない: %v", path, pe.Line, pe.Err)
	}
	return fmt.Errorf("%s: CSV を読めない: %w", path, err)
}

// invalidUTF8Line は、UTF-8 として正しくない最初のバイトがある行の番号（1 始まり）を返す。
func invalidUTF8Line(data []byte) int {
	line := 1
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size <= 1 {
			return line
		}
		if r == '\n' {
			line++
		}
		data = data[size:]
	}
	return line
}
