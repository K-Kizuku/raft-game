package bpv7

import (
	"bytes"
	"encoding/binary"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncodeFixtures(t *testing.T) {
	for name, b := range fixtureBundles(t) {
		t.Run(name, func(t *testing.T) {
			want := readHex(t, filepath.Join("testdata", name+".hex"))
			got, err := Encode(b)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("Encode:\n got %x\nwant %x", got, want)
			}
		})
	}
}

func TestDecodeFixtures(t *testing.T) {
	for name, want := range fixtureBundles(t) {
		t.Run(name, func(t *testing.T) {
			got, err := Decode(readHex(t, filepath.Join("testdata", name+".hex")))
			if err != nil {
				t.Fatal(err)
			}
			if !bundleEqual(got, want) {
				t.Fatalf("Decode:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

// TestDecodeErrors は testdata/errors/<code>.<説明>.hex がそれぞれ <code> で断られることを確かめる。
func TestDecodeErrors(t *testing.T) {
	files := globSorted(t, filepath.Join("testdata", "errors", "*.hex"))
	seen := map[Code]bool{}
	for _, f := range files {
		base := filepath.Base(f)
		want := Code(base[:strings.Index(base, ".")])
		seen[want] = true
		t.Run(strings.TrimSuffix(base, ".hex"), func(t *testing.T) {
			_, err := Decode(readHex(t, f))
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("Decode error = %v, want *Error %s", err, want)
			}
			if e.Code != want {
				t.Fatalf("Decode error = %v, want %s", err, want)
			}
			if !errors.Is(err, &Error{Code: want}) {
				t.Fatalf("errors.Is(%v, %s) = false", err, want)
			}
		})
	}
	for _, c := range []Code{CodeMalformed, CodeVersion, CodeUnsupportedFlags, CodeCRC, CodeEID,
		CodeTimestamp, CodeAge, CodeUnsupportedBlock, CodeStructure, CodeTooLarge} {
		if !seen[c] {
			t.Errorf("no fixture for %s", c)
		}
	}
}

func codeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func validBundle() Bundle {
	return Bundle{
		Dest: IslandRaftEID(2), Source: IslandRaftEID(1), ReportTo: DTNNone,
		Seq: 1, LifetimeMs: testLifetime, AgeMs: 0, Payload: []byte{1, 2, 3},
	}
}

func TestEncodeRejects(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*Bundle)
		want Code
	}{
		{"source ferry", func(b *Bundle) { b.Source = FerryEID(1); b.Dest = IPN(2, 0) }, CodeEID},
		{"dest none", func(b *Bundle) { b.Dest = DTNNone }, CodeEID},
		{"dest ferry", func(b *Bundle) { b.Dest = FerryEID(1) }, CodeEID},
		{"service 3", func(b *Bundle) { b.Source = IPN(1, 3); b.Dest = IPN(2, 3) }, CodeEID},
		{"service mismatch", func(b *Bundle) { b.Dest = IslandRequestEID(2) }, CodeEID},
		{"report-to ipn", func(b *Bundle) { b.ReportTo = IslandRaftEID(1) }, CodeEID},
		{"island 0", func(b *Bundle) { b.Source = IslandRaftEID(0) }, CodeEID},
		{"island 1000", func(b *Bundle) { b.Source = IslandRaftEID(1000) }, CodeEID},
		{"unknown scheme", func(b *Bundle) { b.Dest = EID{Scheme: 3, Node: 2, Service: 1} }, CodeEID},
		{"dtn time", func(b *Bundle) { b.CreatedDTN = 1 }, CodeTimestamp},
		{"seq 0", func(b *Bundle) { b.Seq = 0 }, CodeTimestamp},
		{"seq 1e9", func(b *Bundle) { b.Seq = 1_000_000_000 }, CodeTimestamp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := validBundle()
			tt.mod(&b)
			if _, err := Encode(b); codeOf(err) != tt.want {
				t.Fatalf("Encode error = %v, want %s", err, tt.want)
			}
		})
	}
}

func TestBoundaries(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*Bundle)
	}{
		{"seq 1", func(b *Bundle) { b.Seq = 1 }},
		{"seq 999999999", func(b *Bundle) { b.Seq = MaxSeq }},
		{"age 0", func(b *Bundle) { b.AgeMs = 0 }},
		{"age = lifetime", func(b *Bundle) { b.AgeMs = b.LifetimeMs }},
		{"age > lifetime (not expired on read)", func(b *Bundle) { b.AgeMs = b.LifetimeMs + 1 }},
		{"age > 2^32", func(b *Bundle) { b.AgeMs = 1<<32 + 5 }},
		{"age max", func(b *Bundle) { b.AgeMs = ^uint64(0) }},
		{"lifetime 0", func(b *Bundle) { b.LifetimeMs = 0 }},
		{"island 999", func(b *Bundle) { b.Source = IslandRaftEID(999); b.Dest = IslandRaftEID(999) }},
		{"request service", func(b *Bundle) { b.Source = IslandRequestEID(1); b.Dest = IslandRequestEID(2) }},
		{"empty payload", func(b *Bundle) { b.Payload = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := validBundle()
			tt.mod(&b)
			enc, err := Encode(b)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Decode(enc)
			if err != nil {
				t.Fatal(err)
			}
			if !bundleEqual(got, b) {
				t.Fatalf("round trip:\n got %+v\nwant %+v", got, b)
			}
		})
	}
}

// sizedBundle はちょうど total バイトに符号化される Bundle を返す（total は 256 バイトより十分大きいこと）。
func sizedBundle(t *testing.T, total int) Bundle {
	t.Helper()
	b := validBundle()
	b.Payload = nil
	base, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	// 空のペイロードの bstr の頭は 1 バイト（0x40）、256〜65535 バイトなら 3 バイト（0x59 xx xx）。
	b.Payload = bytes.Repeat([]byte{0xaa}, total-len(base)-2)
	return b
}

func TestSizeLimit(t *testing.T) {
	exact := sizedBundle(t, MaxBundleSize)
	enc, err := Encode(exact)
	if err != nil {
		t.Fatalf("16 KiB exactly: %v", err)
	}
	if len(enc) != MaxBundleSize {
		t.Fatalf("len = %d", len(enc))
	}
	if _, err := Decode(enc); err != nil {
		t.Fatalf("Decode 16 KiB exactly: %v", err)
	}
	over := exact
	over.Payload = append(bytes.Clone(exact.Payload), 0xaa)
	if _, err := Encode(over); codeOf(err) != CodeTooLarge {
		t.Fatalf("Encode 16 KiB + 1: %v", err)
	}
	// 読み込みでは、形を見る前に大きさで断る。
	if _, err := Decode(append(enc, 0)); codeOf(err) != CodeTooLarge {
		t.Fatalf("Decode 16 KiB + 1: %v", err)
	}
}

func TestSetAge(t *testing.T) {
	for name, b := range fixtureBundles(t) {
		t.Run(name, func(t *testing.T) {
			orig := readHex(t, filepath.Join("testdata", name+".hex"))
			keep := bytes.Clone(orig)
			for _, age := range []uint64{0, 1, b.AgeMs + 12_345, 1 << 40} {
				out, err := SetAge(orig, age)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(orig, keep) {
					t.Fatal("SetAge modified its input")
				}
				got, err := Decode(out) // CRC が合うこと
				if err != nil {
					t.Fatalf("Decode(SetAge): %v", err)
				}
				want := b
				want.AgeMs = age
				if !bundleEqual(got, want) {
					t.Fatalf("SetAge(%d):\n got %+v\nwant %+v", age, got, want)
				}
				if !bytes.Equal(got.Payload, b.Payload) {
					t.Fatal("payload bytes changed")
				}
				// Age が同じなら bytes も同じ。
				if age == b.AgeMs && !bytes.Equal(out, orig) {
					t.Fatal("SetAge with the same age changed bytes")
				}
			}
		})
	}
	if _, err := SetAge([]byte{0x9f, 0xff}, 1); codeOf(err) != CodeMalformed {
		t.Fatalf("SetAge(malformed) = %v", err)
	}
}

// TestCRCLayout は、CRC が「CRC の欄を 0 にしたブロック」に対する CRC-32C であることを、
// 既知の値（testdata/README.md の検算）で確かめる。
func TestCRCLayout(t *testing.T) {
	hb := readHex(t, filepath.Join("testdata", "heartbeat.hex"))
	// Primary は 1 バイト目から 30 バイト、Age は 14 バイト、Payload は 21 バイト。
	blocks := [][]byte{hb[1:31], hb[31:45], hb[45:66]}
	wants := []uint32{0x34c853c5, 0x2f8e92bf, 0x7b90425c}
	for i, blk := range blocks {
		if got := binary.BigEndian.Uint32(blk[len(blk)-4:]); got != wants[i] || crcOf(blk) != wants[i] {
			t.Errorf("block %d: stored %08x, computed %08x, want %08x", i, got, crcOf(blk), wants[i])
		}
	}
	if hb[66] != 0xff || len(hb) != 67 {
		t.Fatalf("unexpected layout: len %d", len(hb))
	}
}
