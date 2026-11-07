package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// selectFile は data/islands-select.yaml（データ仕様 §2.6）。
type selectFile struct {
	Format      yamlInt           `yaml:"format"`
	GeneratedAt string            `yaml:"generatedAt"`
	Dataset     string            `yaml:"dataset"`
	Sources     map[string]source `yaml:"sources"`
	Islands     []selectIsland    `yaml:"islands"`
}

// source は出典の情報。islands.json の sources にもそのまま書く。
type source struct {
	Title       string `yaml:"title" json:"title"`
	Publisher   string `yaml:"publisher" json:"publisher"`
	License     string `yaml:"license" json:"license"`
	TermsURL    string `yaml:"termsUrl" json:"termsUrl"`
	PublishedAt string `yaml:"publishedAt" json:"publishedAt"`
}

type selectIsland struct {
	ID             string      `yaml:"id"`
	Prefecture     string      `yaml:"prefecture"`
	Name           string      `yaml:"name"`
	DesignatedArea string      `yaml:"designatedArea"`
	Municipalities []string    `yaml:"municipalities"`
	Plan           *selectPlan `yaml:"plan"`
}

type selectPlan struct {
	Region     string `yaml:"region"`
	IslandName string `yaml:"islandName"`
}

// planIslandName は、D2 の ISLAND_NAME で探す島の名前（省略時は島の name）。
func (p *selectPlan) planIslandName(is selectIsland) string {
	if p.IslandName != "" {
		return p.IslandName
	}
	return is.Name
}

var islandIDRe = regexp.MustCompile(`^[a-z]+(-[a-z0-9]+)+$`)

// loadSelect は islands-select.yaml を読んで検証する。
func loadSelect(path string) (*selectFile, error) {
	data, err := readInput(path)
	if err != nil {
		return nil, err
	}
	var s selectFile
	if id, err := decodeStrictYAML(data, &s, "islands"); err != nil {
		return nil, fmt.Errorf("%s%s: %w", path, withID("島", id), err)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%s%s", path, err)
	}
	return &s, nil
}

// validate は検証する。エラーのメッセージは「: …」か「（島 id）: …」で始まる（前にパスを付ける）。
func (s *selectFile) validate() error {
	if !s.Format.set || s.Format.value != 1 {
		return fmt.Errorf(": format が 1 ではない")
	}
	if !validISODate(s.GeneratedAt) {
		return fmt.Errorf(": generatedAt %q が実在する YYYY-MM-DD ではない", s.GeneratedAt)
	}
	if len(s.Sources) == 0 {
		return fmt.Errorf(": sources が空")
	}
	keys := make([]string, 0, len(s.Sources))
	for k := range s.Sources {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		src := s.Sources[k]
		for _, f := range []struct{ name, v string }{
			{"title", src.Title}, {"publisher", src.Publisher}, {"license", src.License},
			{"termsUrl", src.TermsURL}, {"publishedAt", src.PublishedAt},
		} {
			if strings.TrimSpace(f.v) == "" {
				return fmt.Errorf(": sources.%s.%s が空", k, f.name)
			}
		}
		if !strings.HasPrefix(src.TermsURL, "https://") {
			return fmt.Errorf(": sources.%s.termsUrl %q が https:// で始まらない", k, src.TermsURL)
		}
		if !validISODate(src.PublishedAt) {
			return fmt.Errorf(": sources.%s.publishedAt %q が実在する YYYY-MM-DD ではない", k, src.PublishedAt)
		}
	}
	if _, ok := s.Sources[s.Dataset]; !ok {
		return fmt.Errorf(": dataset %q が sources にない", s.Dataset)
	}
	if len(s.Islands) == 0 {
		return fmt.Errorf(": islands が空")
	}
	ids := map[string]bool{}
	keysSeen := map[[3]string]string{}
	for i, is := range s.Islands {
		ctx := withID("島", is.ID)
		if ctx == "" {
			ctx = fmt.Sprintf("（islands の %d 番目）", i+1)
		}
		if !islandIDRe.MatchString(is.ID) {
			return fmt.Errorf("%s: id %q が形式（^[a-z]+(-[a-z0-9]+)+$）に合わない", ctx, is.ID)
		}
		if ids[is.ID] {
			return fmt.Errorf("%s: id が重複している", ctx)
		}
		ids[is.ID] = true
		for _, f := range []struct{ name, v string }{
			{"prefecture", is.Prefecture}, {"name", is.Name}, {"designatedArea", is.DesignatedArea},
		} {
			if strings.TrimSpace(f.v) == "" {
				return fmt.Errorf("%s: %s が空", ctx, f.name)
			}
		}
		key := [3]string{is.Prefecture, is.DesignatedArea, is.Name}
		if other, dup := keysSeen[key]; dup {
			return fmt.Errorf("%s: (prefecture, designatedArea, name) が島 %s と同じ", ctx, other)
		}
		keysSeen[key] = is.ID
		if len(is.Municipalities) == 0 {
			return fmt.Errorf("%s: municipalities が空", ctx)
		}
		seen := map[string]bool{}
		for _, m := range is.Municipalities {
			if strings.TrimSpace(m) == "" {
				return fmt.Errorf("%s: municipalities に空の要素がある", ctx)
			}
			if seen[m] {
				return fmt.Errorf("%s: municipalities に %s が 2 つある", ctx, m)
			}
			seen[m] = true
		}
		if is.Plan != nil && strings.TrimSpace(is.Plan.Region) == "" {
			return fmt.Errorf("%s: plan.region が空", ctx)
		}
	}
	return nil
}
