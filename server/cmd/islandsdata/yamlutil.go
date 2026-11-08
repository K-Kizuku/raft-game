package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// yamlInt は YAML の整数（タグが !!int のスカラー）だけを受け付ける。
// yaml/v3 は小数を整数のフィールドに切り捨てて入れるので、それを防ぐ。
type yamlInt struct {
	value int64
	set   bool
	line  int
}

func (i *yamlInt) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!int" {
		return fmt.Errorf("line %d: 整数ではない: %q", n.Line, n.Value)
	}
	var v int64
	if err := n.Decode(&v); err != nil {
		return err
	}
	*i = yamlInt{value: v, set: true, line: n.Line}
	return nil
}

// decodeStrictYAML は data を v に読む。未知のキー・型の違い・重複したキーはエラーにする。
// 文書は 1 つだけ受け付ける。エラーのとき、その行が listKey の何番目の要素の中にあるかを調べ、
// 要素の id を返す（分からなければ空）。
func decodeStrictYAML(data []byte, v any, listKey string) (string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return "", errors.New("空のファイル")
		}
		return idAtErrorLine(data, listKey, err), err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("line %d: YAML の文書が 2 つ以上ある", extra.Line)
	}
	return "", nil
}

var errLineRe = regexp.MustCompile(`line ([0-9]+):`)

// idAtErrorLine は、エラーの最初の行番号を含む listKey の要素の id を返す。
func idAtErrorLine(data []byte, listKey string, err error) string {
	m := errLineRe.FindStringSubmatch(err.Error())
	if m == nil {
		return ""
	}
	line, _ := strconv.Atoi(m[1])
	var root yaml.Node
	if yaml.Unmarshal(data, &root) != nil || root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return ""
	}
	top := root.Content[0]
	if top.Kind != yaml.MappingNode {
		return ""
	}
	var list *yaml.Node
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == listKey {
			list = top.Content[i+1]
			// 次のキーより後の行は、この一覧の外。
			if i+2 < len(top.Content) && top.Content[i+2].Line <= line {
				return ""
			}
			break
		}
	}
	if list == nil || list.Kind != yaml.SequenceNode {
		return ""
	}
	id := ""
	for _, item := range list.Content {
		if item.Line > line {
			break
		}
		id = ""
		if item.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(item.Content); i += 2 {
			if item.Content[i].Value == "id" {
				id = item.Content[i+1].Value
				break
			}
		}
	}
	return id
}

// withID は、id が分かるときにメッセージへ付ける「（id: …）」を返す。
func withID(kind, id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf("（%s %s）", kind, id)
}
