package ledger

import (
	"errors"
	"testing"
)

// C-1
func TestEncodeFixedBytes(t *testing.T) {
	t.Parallel()
	got, err := Encode(Command{V: 1, ReqID: "s2-0007", Origin: 3, Kind: "firework.reserve", Resource: "firework:grand"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	const want = `{"v":1,"reqId":"s2-0007","origin":3,"kind":"firework.reserve","resource":"firework:grand","amount":0}`
	if string(got) != want {
		t.Fatalf("Encode =\n%s\nwant\n%s", got, want)
	}
}

// C-2
func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []Command{
		{V: 1, ReqID: "s2-0007", Origin: 3, Kind: KindFireworkReserve, Resource: "firework:grand"},
		{V: 1, ReqID: "r", Origin: 1, Kind: KindStallReserve, Resource: "stall:main", Amount: 0},
		{V: 1, ReqID: "neg", Origin: 18446744073709551615, Kind: "budget.spend", Amount: -42},
		{V: 1, ReqID: "big", Origin: 7, Kind: "budget.spend", Amount: 9223372036854775807},
		{V: 1, ReqID: "min", Origin: 7, Kind: "budget.spend", Amount: -9223372036854775808},
		{V: 1, ReqID: "日本語\"\\<>&", Origin: 2, Kind: "x", Resource: ""},
	}
	for _, c := range cases {
		b, err := Encode(c)
		if err != nil {
			t.Fatalf("Encode(%+v): %v", c, err)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatalf("Decode(%s): %v", b, err)
		}
		if got != c {
			t.Fatalf("round trip = %+v, want %+v", got, c)
		}
	}
}

// C-3
func TestEncodeInvalid(t *testing.T) {
	t.Parallel()
	valid := Command{V: 1, ReqID: "r1", Origin: 1, Kind: KindFireworkReserve, Resource: "firework:a"}
	tests := []struct {
		name string
		mod  func(*Command)
	}{
		{"v0", func(c *Command) { c.V = 0 }},
		{"v2", func(c *Command) { c.V = 2 }},
		{"empty reqId", func(c *Command) { c.ReqID = "" }},
		{"origin0", func(c *Command) { c.Origin = 0 }},
		{"empty kind", func(c *Command) { c.Kind = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := valid
			tt.mod(&c)
			b, err := Encode(c)
			if !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("Encode err = %v, want ErrInvalidCommand", err)
			}
			if b != nil {
				t.Fatalf("Encode bytes = %q, want nil", b)
			}
		})
	}
}

const validJSON = `{"v":1,"reqId":"r1","origin":3,"kind":"firework.reserve","resource":"firework:a","amount":0}`

// decodeInvalidInputs は C-4 の不正な入力。F-1 のシードにも使う。
var decodeInvalidInputs = []struct {
	name string
	data string
}{
	{"empty", ""},
	{"spaces", "  \n"},
	{"null", "null"},
	{"array", "[]"},
	{"string", `"x"`},
	{"number", "1"},
	{"broken", `{"v":1,`},
	{"broken2", `{"v":}`},
	{"unknown key", `{"v":1,"reqId":"r1","origin":3,"kind":"firework.reserve","resource":"firework:a","amount":0,"extra":1}`},
	{"origin string", `{"v":1,"reqId":"r1","origin":"3","kind":"firework.reserve","resource":"firework:a","amount":0}`},
	{"origin negative", `{"v":1,"reqId":"r1","origin":-1,"kind":"firework.reserve","resource":"firework:a","amount":0}`},
	{"amount float", `{"v":1,"reqId":"r1","origin":3,"kind":"firework.reserve","resource":"firework:a","amount":1.5}`},
	{"two values", validJSON + validJSON},
	{"trailing garbage", validJSON + " x"},
	{"trailing null", validJSON + " null"},
}

// C-4
func TestDecodeInvalid(t *testing.T) {
	t.Parallel()
	for _, tt := range decodeInvalidInputs {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Decode([]byte(tt.data)); !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("Decode(%q) err = %v, want ErrInvalidCommand", tt.data, err)
			}
		})
	}
}

// C-4（末尾の空白・改行だけなら成功する）
func TestDecodeTrailingWhitespace(t *testing.T) {
	t.Parallel()
	want := Command{V: 1, ReqID: "r1", Origin: 3, Kind: KindFireworkReserve, Resource: "firework:a"}
	for _, data := range []string{validJSON, validJSON + "\n", validJSON + " \t\r\n ", "\n " + validJSON + "\n"} {
		got, err := Decode([]byte(data))
		if err != nil {
			t.Fatalf("Decode(%q): %v", data, err)
		}
		if got != want {
			t.Fatalf("Decode(%q) = %+v, want %+v", data, got, want)
		}
	}
}
