package issuebody

import (
	"errors"
	"path"
	"strings"
)

// NormalizePath は、先頭の `./` と `/` を取り除く。
func NormalizePath(p string) string {
	for {
		switch {
		case strings.HasPrefix(p, "./"):
			p = p[2:]
		case strings.HasPrefix(p, "/"):
			p = p[1:]
		default:
			return p
		}
	}
}

// ValidatePattern は glob の書式を確かめる。
func ValidatePattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return errors.New("空の glob")
	}
	for _, seg := range strings.Split(NormalizePath(pattern), "/") {
		if seg == "" {
			return errors.New("空の要素（`//` や末尾の `/`）がある")
		}
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, ""); err != nil {
			return err
		}
	}
	return nil
}

// Match は、`/` 区切りのパス name が glob pattern に一致するかを返す。
//
// 要素ごとに path.Match で比べる。要素がちょうど `**` のときは、0 個以上の要素に一致する
// （`a/**` は `a` 自身と `a` の下のすべてに一致する）。
func Match(pattern, name string) (bool, error) {
	if err := ValidatePattern(pattern); err != nil {
		return false, err
	}
	name = NormalizePath(name)
	if name == "" {
		return false, nil
	}
	return matchSegs(strings.Split(NormalizePath(pattern), "/"), strings.Split(name, "/")), nil
}

// MatchAny は、name がいずれかの pattern に一致するかを返す。
func MatchAny(patterns []string, name string) (bool, error) {
	for _, p := range patterns {
		ok, err := Match(p, name)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func matchSegs(p, n []string) bool {
	for len(p) > 0 {
		if p[0] == "**" {
			for len(p) > 0 && p[0] == "**" {
				p = p[1:]
			}
			if len(p) == 0 {
				return true
			}
			for i := 0; i <= len(n); i++ {
				if matchSegs(p, n[i:]) {
					return true
				}
			}
			return false
		}
		if len(n) == 0 {
			return false
		}
		if ok, _ := path.Match(p[0], n[0]); !ok {
			return false
		}
		p, n = p[1:], n[1:]
	}
	return len(n) == 0
}
