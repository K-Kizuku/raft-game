package main

import (
	"slices"
	"testing"
)

// SE-1: 正しい形。
func TestParseSeeds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []uint64
	}{
		{"7", []uint64{7}},
		{"1..3", []uint64{1, 2, 3}},
		{"1..3,10,20..21", []uint64{1, 2, 3, 10, 20, 21}},
		{"5..5", []uint64{5}},
		{"0", []uint64{0}},
		{"18446744073709551615", []uint64{18446744073709551615}},
		{"18446744073709551614..18446744073709551615", []uint64{18446744073709551614, 18446744073709551615}},
		{"3,1", []uint64{3, 1}},
	}
	for _, c := range cases {
		got, err := parseSeeds(c.in)
		if err != nil {
			t.Errorf("parseSeeds(%q): %v", c.in, err)
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("parseSeeds(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	got, err := parseSeeds("1..100000")
	if err != nil || len(got) != maxSeeds {
		t.Errorf("parseSeeds(1..100000) = %d seeds, %v; want %d", len(got), err, maxSeeds)
	}
}

// SE-2: 誤り。
func TestParseSeedsErrors(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"", ",", "1,,2", "1,", "a", "-1", " 1", "1 ", "+1", "0x10", "1.5", "3..1", "1..", "..2", "..", "1..2..3",
		"18446744073709551616", "1,1", "1..3,2", "1..100001", "0..18446744073709551615", "1..100000,100001",
	} {
		if got, err := parseSeeds(in); err == nil {
			t.Errorf("parseSeeds(%q) = %v, want error", in, got)
		}
	}
}
