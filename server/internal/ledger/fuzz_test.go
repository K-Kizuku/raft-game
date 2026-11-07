package ledger

import (
	"bytes"
	"testing"
)

// F-1: 任意のバイト列の列を index 1, 2, … で適用しても panic せず、不変条件を保つ。
// 入力は 0x00 で区切った複数のエントリとして扱う（JSON の中に 0x00 は現れないため）。
func FuzzApply(f *testing.F) {
	f.Add([]byte(`{"v":1,"reqId":"s2-0007","origin":3,"kind":"firework.reserve","resource":"firework:grand","amount":0}`))
	for _, in := range decodeInvalidInputs {
		f.Add([]byte(in.data))
	}
	f.Add([]byte(`{"v":1,"reqId":"a","origin":1,"kind":"firework.reserve","resource":"firework:a","amount":0}` + "\x00" +
		`{"v":1,"reqId":"b","origin":2,"kind":"firework.reserve","resource":"firework:a","amount":0}` + "\x00" +
		`{"v":1,"reqId":"a","origin":1,"kind":"stall.reserve","resource":"stall:main","amount":0}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		l, err := New(testDefs())
		if err != nil {
			t.Fatal(err)
		}
		for i, data := range bytes.Split(in, []byte{0}) {
			l.Apply(uint64(i+1), data)
			if err := l.CheckInvariants(); err != nil {
				t.Fatalf("entry %d: %v", i+1, err)
			}
		}
	})
}
