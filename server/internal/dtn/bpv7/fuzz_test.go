package bpv7

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"google.golang.org/protobuf/proto"
)

// FuzzDecode は、どんな入力でも Decode が panic せず、*Error か成功を返すことを確かめる。
// 読み込めたら、Encode(Decode(x)) は x とバイト単位で同じ（このプロファイルの符号化は一意）。
// 種は testdata の fixture すべて（go test のふつうの実行で回る）。
func FuzzDecode(f *testing.F) {
	for _, pat := range []string{"*.hex", filepath.Join("errors", "*.hex")} {
		for _, file := range globSorted(f, filepath.Join("testdata", pat)) {
			f.Add(readHex(f, file))
		}
	}
	c := LetterCodec{LifetimeMs: testLifetime}
	f.Fuzz(func(t *testing.T, data []byte) {
		b, err := Decode(data)
		if err != nil {
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("Decode returned a non-*Error: %v", err)
			}
			return
		}
		enc, err := Encode(b)
		if err != nil {
			t.Fatalf("Encode(Decode(x)) failed: %v", err)
		}
		if !bytes.Equal(enc, data) {
			t.Fatalf("Encode(Decode(x)) != x:\n x %x\n e %x", data, enc)
		}
		b2, err := Decode(enc)
		if err != nil || !bundleEqual(b, b2) {
			t.Fatalf("Decode(Encode(Decode(x))) = %+v, %v; want %+v", b2, err, b)
		}
		// Codec も panic しない。読めたら符号化し直して読み、同じ手紙に戻る。依頼の手紙は
		// bytes も同じ（依頼のペイロードは最短の符号化だけを受ける）。Raft のペイロードは
		// proto.Unmarshal が最短でない protobuf も受けるので、bytes までは比べない。
		l, cmd, age, err := c.Decode(data)
		if err != nil {
			return
		}
		c2 := LetterCodec{LifetimeMs: b.LifetimeMs}
		again, err := c2.Encode(l, cmd, age)
		if err != nil {
			t.Fatalf("LetterCodec.Encode(Decode(x)) failed: %v", err)
		}
		l2, cmd2, age2, err := c2.Decode(again)
		if err != nil {
			t.Fatalf("LetterCodec.Decode(Encode(Decode(x))) failed: %v", err)
		}
		if l2.ID != l.ID || l2.Kind != l.Kind || l2.From != l.From || l2.To != l.To || age2 != age ||
			!bytes.Equal(cmd2, cmd) || !proto.Equal(l2.Raft, l.Raft) ||
			(l.Req != nil) != (l2.Req != nil) || (l.Req != nil && *l.Req != *l2.Req) {
			t.Fatalf("LetterCodec round trip differs: %+v vs %+v", l, l2)
		}
		if l.Kind == ferry.LetterRequest && !bytes.Equal(again, data) {
			t.Fatalf("request letter round trip differs:\n x %x\n e %x", data, again)
		}
	})
}
