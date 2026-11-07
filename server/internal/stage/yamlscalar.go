package stage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// YAML の整数・小数・真偽値を受ける小さな型（ADR-0022）。どれも「書いたかどうか」を持つ。
// 考え方は islandsdata の yamlInt と同じ（import はしない）。

var (
	yamlIntRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
	yamlDecRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})(\.[0-9]{1,3})?$`)
)

// yInt は 10 進の整数（タグが !!int で、値が ^-?(0|[1-9][0-9]*)$）だけを受け付ける。
// yaml/v3 は 0x10・1_000・010・+1 も !!int にするので、値の形も確かめる。
type yInt struct {
	value int64
	set   bool
}

func (i *yInt) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!int" || !yamlIntRe.MatchString(n.Value) {
		return fmt.Errorf("line %d: 10 進の整数ではない: %q", n.Line, n.Value)
	}
	v, err := strconv.ParseInt(n.Value, 10, 64)
	if err != nil {
		return fmt.Errorf("line %d: 整数が大きすぎる: %q", n.Line, n.Value)
	}
	*i = yInt{value: v, set: true}
	return nil
}

// yDec は小数第 3 位までの 0 以上の小数を受け付け、千分率の整数にする（1.2 → 1200）。
// 浮動小数点を通さずに、文字列から読む。
type yDec struct {
	permille int64
	set      bool
}

func (d *yDec) UnmarshalYAML(n *yaml.Node) error {
	tag := n.ShortTag()
	if n.Kind != yaml.ScalarNode || (tag != "!!int" && tag != "!!float") || !yamlDecRe.MatchString(n.Value) {
		return fmt.Errorf("line %d: 小数第 3 位までの 0 以上の小数ではない: %q", n.Line, n.Value)
	}
	v, ok := parsePermille(n.Value)
	if !ok {
		return fmt.Errorf("line %d: 小数を読めない: %q", n.Line, n.Value)
	}
	*d = yDec{permille: v, set: true}
	return nil
}

// parsePermille は yamlDecRe に合う文字列を千分率の整数にする。
func parsePermille(s string) (int64, bool) {
	intPart, frac, _ := bytes.Cut([]byte(s), []byte("."))
	ip, err := strconv.ParseInt(string(intPart), 10, 64)
	if err != nil {
		return 0, false
	}
	var fp int64
	for k := range 3 {
		fp *= 10
		if k < len(frac) {
			fp += int64(frac[k] - '0')
		}
	}
	return ip*1000 + fp, true
}

// yBool は true か false だけを受け付ける（yaml/v3 は yes・on・True も bool の欄に入れてしまう）。
type yBool struct {
	value bool
	set   bool
}

func (b *yBool) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!bool" || (n.Value != "true" && n.Value != "false") {
		return fmt.Errorf("line %d: true か false ではない: %q", n.Line, n.Value)
	}
	*b = yBool{value: n.Value == "true", set: true}
	return nil
}

// decodeStrictYAML は data を v に読む。未知のキー・重複したキー・型の違いはエラーにする。
// 文書は 1 つだけ受け付け、空のファイルはエラーにする。
func decodeStrictYAML(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("空のファイル")
		}
		return err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return fmt.Errorf("line %d: YAML の文書が 2 つ以上ある", extra.Line)
	}
	return nil
}
