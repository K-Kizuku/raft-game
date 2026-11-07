package main

import "testing"

// E-12: 和暦。
func TestParseWareki(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want date
	}{
		{"令和元年5月1日", date{2019, 5, 1}},
		{"平成31年4月30日", date{2019, 4, 30}},
		{"平成元年1月8日", date{1989, 1, 8}},
		{"令和8年4月1日", date{2026, 4, 1}},
		{"令和８年４月１日", date{2026, 4, 1}},
		{"令和2年2月29日", date{2020, 2, 29}},
	}
	for _, c := range cases {
		got, err := parseWareki(c.in)
		if err != nil || got != c.want {
			t.Errorf("parseWareki(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	h, _ := parseWareki("平成31年4月30日")
	r, _ := parseWareki("令和元年5月1日")
	if !h.less(r) || r.less(h) {
		t.Error("平成31年4月30日 が 令和元年5月1日 より前になっていない")
	}
	for _, bad := range []string{"", "令和年4月1日", "2026-04-01", "昭和60年1月1日", "令和8年2月30日", "令和7年2月29日", "令和8年13月1日", "令和8年0月1日", "令和0年1月1日", " 令和8年4月1日"} {
		if _, err := parseWareki(bad); err == nil {
			t.Errorf("parseWareki(%q) がエラーにならない", bad)
		}
	}
}

func TestValidISODate(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]bool{
		"2026-11-07": true, "2024-02-29": true, "2026-02-29": false, "2026-13-01": false,
		"2026-1-07": false, "": false, "2026/11/07": false,
	} {
		if got := validISODate(s); got != want {
			t.Errorf("validISODate(%q) = %v", s, got)
		}
	}
}

// E-12: 人口。
func TestParsePopulation(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int64{"12,011": 12011, "１２０１１": 12011, "１２,０１１": 12011, "0": 0, "768": 768} {
		if got, err := parsePopulation(in); err != nil || got != want {
			t.Errorf("parsePopulation(%q) = %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "-1", "1.5", "abc", " 1", ","} {
		if _, err := parsePopulation(bad); err == nil {
			t.Errorf("parsePopulation(%q) がエラーにならない", bad)
		}
	}
}

// E-12: 面積の読み書き。
func TestArea(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		v    int64
		text string
	}{
		{"14.5000", 145000, "14.5"},
		{"14.50", 145000, "14.5"},
		{"12", 120000, "12"},
		{"12.0000", 120000, "12"},
		{"0.0001", 1, "0.0001"},
		{"0", 0, "0"},
		{"326.36", 3263600, "326.36"},
		{"１５３．２７", 0, ""}, // 全角の「．」は受け付けない
	}
	for _, c := range cases {
		v, err := parseArea(c.in)
		if c.text == "" {
			if err == nil {
				t.Errorf("parseArea(%q) がエラーにならない", c.in)
			}
			continue
		}
		if err != nil || v != c.v {
			t.Errorf("parseArea(%q) = %d, %v; want %d", c.in, v, err, c.v)
			continue
		}
		if got := formatFixed(v, areaScale); got != c.text {
			t.Errorf("formatFixed(%d) = %q, want %q", v, got, c.text)
		}
	}
	if v, err := parseArea("１５３.２７"); err != nil || v != 1532700 {
		t.Errorf("全角の数字: %d, %v", v, err)
	}
	for _, bad := range []string{"", "1.23456", "x", "-1", "1.", ".5", "1e3"} {
		if _, err := parseArea(bad); err == nil {
			t.Errorf("parseArea(%q) がエラーにならない", bad)
		}
	}
	a, _ := parseArea("0.1")
	b, _ := parseArea("0.2")
	if got := formatFixed(a+b, areaScale); got != "0.3" {
		t.Errorf("0.1 + 0.2 = %s", got)
	}
}

func TestFormatThousands(t *testing.T) {
	t.Parallel()
	for v, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 12011: "12,011", 1234567: "1,234,567"} {
		if got := formatThousands(v); got != want {
			t.Errorf("formatThousands(%d) = %s, want %s", v, got, want)
		}
	}
}

// E-12: populationSqrt。
func TestPopulationSqrt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pop  int64
		want string
	}{
		{0, "0"}, {1, "1"}, {2, "1.41"}, {169, "13"}, {768, "27.71"}, {25881, "160.88"},
		{4462, "66.8"}, {51492, "226.92"},
		// (n + 0.5)² の前後。x = 人口 × 10000 が n² + n 以下なら n、より大きければ n + 1。
		{6, "2.45"},     // x = 60000、n = 244、n² + n = 59780 < x → 245（√6 = 2.4494…）
		{3, "1.73"},     // x = 30000、n = 173、n² + n = 30102 > x → 173（√3 = 1.7320…）
		{2025, "45"},    // ちょうど 45²
		{2026, "45.01"}, // x = 20260000、n = 4501、n² + n = 20263502 > x → 4501
	}
	for _, c := range cases {
		if got := formatFixed(populationSqrt(c.pop), sqrtScale); got != c.want {
			t.Errorf("populationSqrt(%d) = %s, want %s", c.pop, got, c.want)
		}
	}
	// 性質: 結果 r（0.01 単位）は (r - 0.5)² < x < (r + 0.5)²、つまり (2r - 1)² < 4x < (2r + 1)²。
	for pop := int64(0); pop <= 30000; pop++ {
		r := populationSqrt(pop)
		x4 := 4 * pop * 10000
		lo, hi := (2*r-1)*(2*r-1), (2*r+1)*(2*r+1)
		if (r > 0 && lo >= x4) || x4 >= hi {
			t.Fatalf("populationSqrt(%d) = %d は四捨五入になっていない", pop, r)
		}
	}
}
