package stage

import (
	"strings"
	"testing"
)

// Y-1 整数。
func TestYAMLInt(t *testing.T) {
	t.Parallel()
	for src, want := range map[string]int64{"0": 0, "1": 1, "-1": -1, "1000000000": 1_000_000_000} {
		var v struct {
			V yInt `yaml:"v"`
		}
		if err := decodeStrictYAML([]byte("v: "+src+"\n"), &v); err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if !v.V.set || v.V.value != want {
			t.Errorf("%s: %+v", src, v.V)
		}
	}
	for _, src := range []string{"1.5", `"1"`, "0x10", "1_000", "010", "+1", "1e3", "true", "[1]", "99999999999999999999"} {
		var v struct {
			V yInt `yaml:"v"`
		}
		err := decodeStrictYAML([]byte("v: "+src+"\n"), &v)
		if err == nil || !strings.Contains(err.Error(), "line") {
			t.Errorf("%s: エラーにならないか line がない: %v", src, err)
		}
	}
}

// Y-2 小数。
func TestYAMLDecimal(t *testing.T) {
	t.Parallel()
	for src, want := range map[string]int64{"0": 0, "1": 1000, "1.0": 1000, "1.2": 1200, "0.5": 500, "0.001": 1, "2.25": 2250, "4": 4000, "999999.999": 999_999_999} {
		var v struct {
			V yDec `yaml:"v"`
		}
		if err := decodeStrictYAML([]byte("v: "+src+"\n"), &v); err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if !v.V.set || v.V.permille != want {
			t.Errorf("%s: %+v, want %d", src, v.V, want)
		}
	}
	for _, src := range []string{"1.2345", "-1", ".5", "1.", "1e3", `"1.2"`, ".inf", "0x1", "1234567", "01.5", "true"} {
		var v struct {
			V yDec `yaml:"v"`
		}
		err := decodeStrictYAML([]byte("v: "+src+"\n"), &v)
		if err == nil || !strings.Contains(err.Error(), "line") {
			t.Errorf("%s: エラーにならないか line がない: %v", src, err)
		}
	}
}

// Y-3 真偽値。
func TestYAMLBool(t *testing.T) {
	t.Parallel()
	for src, want := range map[string]bool{"true": true, "false": false} {
		var v struct {
			V yBool `yaml:"v"`
		}
		if err := decodeStrictYAML([]byte("v: "+src+"\n"), &v); err != nil || !v.V.set || v.V.value != want {
			t.Errorf("%s: %+v %v", src, v.V, err)
		}
	}
	for _, src := range []string{"yes", "on", "True", `"true"`, "1", "no"} {
		var v struct {
			V yBool `yaml:"v"`
		}
		err := decodeStrictYAML([]byte("v: "+src+"\n"), &v)
		if err == nil || !strings.Contains(err.Error(), "line") {
			t.Errorf("%s: エラーにならないか line がない: %v", src, err)
		}
	}
}

// Y-4 文書の誤り（Parse を通す）。
func TestParseYAMLDocumentErrors(t *testing.T) {
	t.Parallel()
	is := fixtureIslands(t)
	alpha := readTestdata(t, "stages/alpha.yaml")
	cases := []struct {
		name     string
		data     func(t *testing.T) string
		wantLine bool
	}{
		{"未知のキー", func(t *testing.T) string { return applyEdits(t, alpha, edit{"travelMs: 4000", "travelms: 4000"}) }, true},
		{"重複したキー", func(t *testing.T) string {
			return applyEdits(t, alpha, edit{"title: テストの海\n", "title: テストの海\ntitle: 別の海\n"})
		}, true},
		{"2つの文書", func(*testing.T) string { return alpha + "---\nformat: 1\n" }, true},
		{"空のファイル", func(*testing.T) string { return "" }, false},
		{"整数の欄に小数", func(t *testing.T) string { return applyEdits(t, alpha, edit{"tickMs: 500", "tickMs: 1.5"}) }, true},
		{"整数の欄に16進", func(t *testing.T) string { return applyEdits(t, alpha, edit{"tickMs: 500", "tickMs: 0x10"}) }, true},
		{"整数の欄に文字列", func(t *testing.T) string { return applyEdits(t, alpha, edit{"tickMs: 500", `tickMs: "1"`}) }, true},
		{"小数の欄に4桁", func(t *testing.T) string { return applyEdits(t, alpha, edit{"speed: 1.0", "speed: 1.2345"}) }, true},
		{"小数の欄に文字列", func(t *testing.T) string { return applyEdits(t, alpha, edit{"speed: 1.0", `speed: "1.2"`}) }, true},
		{"真偽値の欄にyes", func(t *testing.T) string { return applyEdits(t, alpha, edit{"checkQuorum: true", "checkQuorum: yes"}) }, true},
		{"真偽値の欄にon", func(t *testing.T) string { return applyEdits(t, alpha, edit{"preVote: true", "preVote: on"}) }, true},
		{"マッピングでない", func(*testing.T) string { return "[1, 2]\n" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st, err := Parse("s/alpha.yaml", []byte(tc.data(t)), is)
			if st != nil {
				t.Error("エラーなのに *Stage を返した")
			}
			se := asError(t, err)
			if len(se.Issues) != 1 || se.Issues[0].Path != "" {
				t.Fatalf("Issues = %+v（Path が空の 1 件のはず）", se.Issues)
			}
			if tc.wantLine && !strings.Contains(se.Issues[0].Message, "line") {
				t.Errorf("Message に line がない: %q", se.Issues[0].Message)
			}
			if strings.Contains(se.Error(), "\n") {
				t.Errorf("Error() が 1 行でない: %q", se.Error())
			}
			requireIssue(t, err, "s/alpha.yaml", "")
		})
	}
}
