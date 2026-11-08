package main

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// 数・日付の読み書き。浮動小数点を使わず、すべて整数で扱う（データ仕様 §2.5）。

const (
	areaScale = 4 // 面積は 0.0001 km² 単位の整数
	sqrtScale = 2 // populationSqrt は 0.01 単位の整数
)

// toHalfwidthDigits は全角の数字（０〜９）を半角にする。
func toHalfwidthDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '０' && r <= '９' {
			return '0' + (r - '０')
		}
		return r
	}, s)
}

// date はグレゴリオ暦の日付。比較のため、年・月・日の順にフィールドを並べる。
type date struct {
	year, month, day int
}

func (d date) less(o date) bool {
	if d.year != o.year {
		return d.year < o.year
	}
	if d.month != o.month {
		return d.month < o.month
	}
	return d.day < o.day
}

func isLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

func daysIn(y, m int) int {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeap(y) {
			return 29
		}
		return 28
	}
	return 0
}

func validDate(y, m, d int) bool {
	return y >= 1 && m >= 1 && m <= 12 && d >= 1 && d <= daysIn(y, m)
}

var warekiRe = regexp.MustCompile(`^(平成|令和)(元|[0-9]{1,3})年([0-9]{1,2})月([0-9]{1,2})日$`)

// eraBase は元号の 1 年の前の年（グレゴリオ暦）。
var eraBase = map[string]int{"平成": 1988, "令和": 2018}

// parseWareki は「令和8年4月1日」の形の和暦を読む。平成と令和だけを受け付ける。
func parseWareki(s string) (date, error) {
	m := warekiRe.FindStringSubmatch(toHalfwidthDigits(s))
	if m == nil {
		return date{}, fmt.Errorf("基準日 %q を読めない（「令和8年4月1日」の形。元号は平成・令和だけ）", s)
	}
	n := 1
	if m[2] != "元" {
		n, _ = strconv.Atoi(m[2])
		if n < 1 {
			return date{}, fmt.Errorf("基準日 %q の年が 1 より小さい", s)
		}
	}
	mo, _ := strconv.Atoi(m[3])
	d, _ := strconv.Atoi(m[4])
	y := eraBase[m[1]] + n
	if !validDate(y, mo, d) {
		return date{}, fmt.Errorf("基準日 %q は存在しない日付", s)
	}
	return date{y, mo, d}, nil
}

var isoDateRe = regexp.MustCompile(`^([0-9]{4})-([0-9]{2})-([0-9]{2})$`)

// validISODate は s が実在する YYYY-MM-DD かを返す。
func validISODate(s string) bool {
	m := isoDateRe.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	return validDate(y, mo, d)
}

var digitsRe = regexp.MustCompile(`^[0-9]+$`)

// parsePopulation は人口を読む。全角の数字と 3 桁ごとの「,」を受け付ける。
func parsePopulation(s string) (int64, error) {
	t := strings.ReplaceAll(toHalfwidthDigits(s), ",", "")
	if !digitsRe.MatchString(t) {
		return 0, fmt.Errorf("人口 %q を読めない（0 以上の整数）", s)
	}
	v, err := strconv.ParseInt(t, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("人口 %q を読めない（大きすぎる）", s)
	}
	return v, nil
}

var areaRe = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]{1,4}))?$`)

// parseArea は面積（km²）を読み、0.0001 km² 単位の整数を返す。
func parseArea(s string) (int64, error) {
	m := areaRe.FindStringSubmatch(toHalfwidthDigits(s))
	if m == nil {
		return 0, fmt.Errorf("面積 %q を読めない（0 以上で、小数は第 4 位まで）", s)
	}
	ip, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || ip > (1<<62)/pow10(areaScale) {
		return 0, fmt.Errorf("面積 %q を読めない（大きすぎる）", s)
	}
	frac := m[2] + strings.Repeat("0", areaScale-len(m[2]))
	fp, _ := strconv.ParseInt(frac, 10, 64)
	return ip*pow10(areaScale) + fp, nil
}

func pow10(n int) int64 {
	v := int64(1)
	for range n {
		v *= 10
	}
	return v
}

// formatFixed は、scale 桁の小数を表す整数 v（0 以上）を 10 進の文字列にする。
// 小数部の末尾の 0 と、残った末尾の「.」を消す（14.5000 → 14.5、12.0000 → 12）。
func formatFixed(v int64, scale int) string {
	p := pow10(scale)
	s := strconv.FormatInt(v/p, 10)
	if scale == 0 {
		return s
	}
	f := strconv.FormatInt(v%p, 10)
	f = strings.Repeat("0", scale-len(f)) + f
	f = strings.TrimRight(f, "0")
	if f == "" {
		return s
	}
	return s + "." + f
}

// formatThousands は 0 以上の整数に 3 桁ごとの「,」を入れる。
func formatThousands(v int64) string {
	s := strconv.FormatInt(v, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// populationSqrt は人口の平方根を小数第 2 位に四捨五入し、0.01 単位の整数で返す。
// x = 人口 × 10000、n = ⌊√x⌋ とし、x > n² + n なら n + 1 にする（(n + 0.5)² = n² + n + 0.25）。
func populationSqrt(pop int64) int64 {
	x := new(big.Int).Mul(big.NewInt(pop), big.NewInt(pow10(2*sqrtScale)))
	n := new(big.Int).Sqrt(x)
	half := new(big.Int).Mul(n, n)
	half.Add(half, n)
	if x.Cmp(half) > 0 {
		n.Add(n, big.NewInt(1))
	}
	return n.Int64()
}
