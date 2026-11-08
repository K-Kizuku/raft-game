package bpv7

import (
	"encoding/binary"
	"hash/crc32"

	"github.com/fxamacker/cbor/v2"
)

// MaxBundleSize は Bundle の bytes の上限（16 KiB。ちょうどは許す）。
const MaxBundleSize = 16 * 1024

// プロファイルの固定値（bpv7-profile.md §5）。
const (
	version            uint64 = 7
	flagMustNotFrag    uint64 = 0x0004 // Bundle の処理の制御フラグ: 断片にしてはならない
	flagIsFragment     uint64 = 0x0001
	crcTypeCRC32C      uint64 = 2
	blockTypePayload   uint64 = 1
	blockTypeAge       uint64 = 7
	blockNumberPayload uint64 = 1
	blockNumberAge     uint64 = 2
	crcLen                    = 4
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Bundle はこのプロファイルの Bundle 1 つ。
type Bundle struct {
	Dest, Source, ReportTo EID
	CreatedDTN             uint64 // 作成時刻の DTN time。常に 0
	Seq                    uint64 // 作成時刻の連番 = letterId の島の中の通し番号 n（1..999,999,999）
	LifetimeMs             uint64
	AgeMs                  uint64 // Bundle Age ブロックの値（ms、仮想時間）
	Payload                []byte
}

// LetterID は、この Bundle の bundleId（送信元と連番）に対応する letterId。
func (b Bundle) LetterID() (uint64, error) { return LetterID(b.Source, b.Seq) }

// Encode は Bundle を bytes にする。Decode が断る値（EID・作成時刻）は同じエラーコードで断り、
// 結果が MaxBundleSize を超えたら bpv7_too_large を返す。Decode(Encode(b)) は b に戻る。
func Encode(b Bundle) ([]byte, error) {
	if err := validateEIDs(b.Dest, b.Source, b.ReportTo); err != nil {
		return nil, err
	}
	if err := validateTimestamp(b.CreatedDTN, b.Seq); err != nil {
		return nil, err
	}
	zero := make([]byte, crcLen)
	primary, err := marshal([]any{
		version, flagMustNotFrag, crcTypeCRC32C,
		eidValue(b.Dest), eidValue(b.Source), eidValue(b.ReportTo),
		[]uint64{b.CreatedDTN, b.Seq}, b.LifetimeMs, zero,
	})
	if err != nil {
		return nil, errorf(CodeMalformed, "primary: %v", err)
	}
	ageData, err := marshal(b.AgeMs)
	if err != nil {
		return nil, errorf(CodeMalformed, "age: %v", err)
	}
	age, err := marshal([]any{blockTypeAge, blockNumberAge, uint64(0), crcTypeCRC32C, ageData, zero})
	if err != nil {
		return nil, errorf(CodeMalformed, "age block: %v", err)
	}
	payload := b.Payload
	if payload == nil {
		payload = []byte{}
	}
	pl, err := marshal([]any{blockTypePayload, blockNumberPayload, uint64(0), crcTypeCRC32C, payload, zero})
	if err != nil {
		return nil, errorf(CodeMalformed, "payload block: %v", err)
	}
	size := 1 + len(primary) + len(age) + len(pl) + 1
	if size > MaxBundleSize {
		return nil, errorf(CodeTooLarge, "%d bytes > %d", size, MaxBundleSize)
	}
	out := make([]byte, 0, size)
	out = append(out, 0x9f)
	for _, blk := range [][]byte{primary, age, pl} {
		binary.BigEndian.PutUint32(blk[len(blk)-crcLen:], crcOf(blk))
		out = append(out, blk...)
	}
	return append(out, 0xff), nil
}

// SetAge は Bundle の Bundle Age を ageMs に変えた bytes を返す（Decode → AgeMs を変える →
// Encode。CRC も計算し直す）。data は変えない。
func SetAge(data []byte, ageMs uint64) ([]byte, error) {
	b, err := Decode(data)
	if err != nil {
		return nil, err
	}
	b.AgeMs = ageMs
	return Encode(b)
}

// eidValue は EID の CBOR の形（ipn は [2, [node, service]]、dtn:none は [1, 0]）。
func eidValue(e EID) any {
	if e.Scheme == SchemeIPN {
		return []any{SchemeIPN, []uint64{e.Node, e.Service}}
	}
	return []any{SchemeDTN, uint64(0)}
}

// crcOf は、末尾の 4 バイト（CRC の欄の中身）を 0 にしたブロックの CRC-32C。
func crcOf(blk []byte) uint32 {
	h := crc32.Update(0, castagnoli, blk[:len(blk)-crcLen])
	return crc32.Update(h, castagnoli, make([]byte, crcLen))
}

func validateTimestamp(dtnTime, seq uint64) *Error {
	if dtnTime != 0 {
		return errorf(CodeTimestamp, "creation DTN time %d is not 0", dtnTime)
	}
	if seq < MinSeq || seq > MaxSeq {
		return errorf(CodeTimestamp, "sequence %d out of range", seq)
	}
	return nil
}

// rawEID は読み込みの途中の EID（方式と SSP の raw）。
type rawEID struct {
	scheme uint64
	ssp    cbor.RawMessage
}

// rawBlock は読み込みの途中のブロック。Primary は typ・num・data を使わない。
type rawBlock struct {
	raw                      []byte
	typ, num, flags, crcType uint64
	data                     []byte
	crc                      []byte // CRC の欄がなければ nil
	version                  uint64 // Primary だけ
	dest, source, reportTo   rawEID // Primary だけ
	dtnTime, seq, lifetimeMs uint64 // Primary だけ
	age                      uint64 // Bundle Age ブロックだけ
}

// Decode は bytes を Bundle にする。bpv7-profile.md §6 の検査を次の順にすべて行い、
// 最初の失敗を *Error で返す: too_large → malformed → version → unsupported_flags →
// crc → eid → timestamp → age → unsupported_block → structure。
// Lifetime を過ぎた Bundle も断らない（§3）。
func Decode(data []byte) (Bundle, error) {
	if len(data) > MaxBundleSize {
		return Bundle{}, errorf(CodeTooLarge, "%d bytes > %d", len(data), MaxBundleSize)
	}
	// 1. 形（CBOR・外側の配列・各ブロックの形）。
	primary, blocks, err := parse(data)
	if err != nil {
		return Bundle{}, err
	}
	// 2. 版。
	if primary.version != version {
		return Bundle{}, errorf(CodeVersion, "version %d", primary.version)
	}
	// 3. フラグ。Bundle は 0x0004 だけ、Bundle Age・Payload のブロックは 0。
	// それ以外の種類のブロックはフラグに関係なく unsupported_block で断る（下の 7.）。
	if primary.flags != flagMustNotFrag {
		return Bundle{}, errorf(CodeUnsupportedFlags, "bundle flags %#x", primary.flags)
	}
	for _, blk := range blocks {
		if (blk.typ == blockTypeAge || blk.typ == blockTypePayload) && blk.flags != 0 {
			return Bundle{}, errorf(CodeUnsupportedFlags, "block %d flags %#x", blk.num, blk.flags)
		}
	}
	// 4. CRC（すべてのブロック）。
	if err := checkCRC(primary, "primary"); err != nil {
		return Bundle{}, err
	}
	for _, blk := range blocks {
		if err := checkCRC(blk, "block "+utoa(blk.num)); err != nil {
			return Bundle{}, err
		}
	}
	// 5. EID。
	dest, ok1 := toEID(primary.dest)
	src, ok2 := toEID(primary.source)
	reportTo, ok3 := toEID(primary.reportTo)
	if !ok1 || !ok2 || !ok3 {
		return Bundle{}, errorf(CodeEID, "endpoint ID is neither ipn:<node>.<service> nor dtn:none")
	}
	if err := validateEIDs(dest, src, reportTo); err != nil {
		return Bundle{}, err
	}
	// 6. 作成時刻。
	if err := validateTimestamp(primary.dtnTime, primary.seq); err != nil {
		return Bundle{}, err
	}
	// 7. Bundle Age はちょうど 1 つ。
	var age *rawBlock
	ages := 0
	for i := range blocks {
		if blocks[i].typ == blockTypeAge {
			ages++
			age = &blocks[i]
		}
	}
	if ages != 1 {
		return Bundle{}, errorf(CodeAge, "%d bundle age blocks", ages)
	}
	// 8. Bundle Age・Payload 以外のブロック。
	for _, blk := range blocks {
		if blk.typ != blockTypeAge && blk.typ != blockTypePayload {
			return Bundle{}, errorf(CodeUnsupportedBlock, "block type %d", blk.typ)
		}
	}
	// 9. 並びとブロック番号。ここまでで blocks は Bundle Age 1 つと Payload 0 個以上。
	if err := checkStructure(blocks); err != nil {
		return Bundle{}, err
	}
	payload := blocks[len(blocks)-1]
	return Bundle{
		Dest: dest, Source: src, ReportTo: reportTo,
		CreatedDTN: primary.dtnTime, Seq: primary.seq,
		LifetimeMs: primary.lifetimeMs,
		AgeMs:      age.age,
		Payload:    payload.data,
	}, nil
}

func checkStructure(blocks []rawBlock) *Error {
	payloads := 0
	seen := make(map[uint64]bool, len(blocks)) // 重複の判定だけに使う（走査しない）
	for _, blk := range blocks {
		if seen[blk.num] {
			return errorf(CodeStructure, "duplicate block number %d", blk.num)
		}
		seen[blk.num] = true
		if blk.typ == blockTypePayload {
			payloads++
		}
	}
	if payloads != 1 {
		return errorf(CodeStructure, "%d payload blocks", payloads)
	}
	last := blocks[len(blocks)-1]
	if last.typ != blockTypePayload {
		return errorf(CodeStructure, "payload block is not last")
	}
	for _, blk := range blocks {
		switch {
		case blk.typ == blockTypePayload && blk.num != blockNumberPayload:
			return errorf(CodeStructure, "payload block number %d", blk.num)
		case blk.typ == blockTypeAge && blk.num != blockNumberAge:
			return errorf(CodeStructure, "bundle age block number %d", blk.num)
		}
	}
	return nil
}

func checkCRC(blk rawBlock, what string) *Error {
	if blk.crcType != crcTypeCRC32C {
		return errorf(CodeCRC, "%s: CRC type %d", what, blk.crcType)
	}
	if len(blk.crc) != crcLen {
		return errorf(CodeCRC, "%s: CRC length %d", what, len(blk.crc))
	}
	// 長さ確定・最短の符号化なので、CRC の欄（最後の要素の 4 バイトの bstr）の中身は raw の末尾の 4 バイト。
	if got, want := binary.BigEndian.Uint32(blk.crc), crcOf(blk.raw); got != want {
		return errorf(CodeCRC, "%s: CRC %08x, want %08x", what, got, want)
	}
	return nil
}

func toEID(r rawEID) (EID, bool) {
	switch r.scheme {
	case SchemeDTN:
		v, ok := decodeUint(r.ssp)
		return DTNNone, ok && v == 0
	case SchemeIPN:
		arr, ok := decodeArray(r.ssp)
		if !ok || len(arr) != 2 {
			return EID{}, false
		}
		node, ok1 := decodeUint(arr[0])
		service, ok2 := decodeUint(arr[1])
		return IPN(node, service), ok1 && ok2
	default:
		return EID{}, false
	}
}

func malformed(format string, args ...any) *Error { return errorf(CodeMalformed, format, args...) }

// parse は外側の長さ不定の配列を自前で読み、各ブロックを形だけ確かめて分ける（bpv7_malformed の検査）。
func parse(data []byte) (rawBlock, []rawBlock, *Error) {
	if len(data) == 0 || data[0] != 0x9f {
		return rawBlock{}, nil, malformed("not an indefinite-length array")
	}
	var items []cbor.RawMessage
	rest := data[1:]
	for {
		if len(rest) == 0 {
			return rawBlock{}, nil, malformed("missing break")
		}
		if rest[0] == 0xff {
			rest = rest[1:]
			break
		}
		var item cbor.RawMessage
		r, err := decMode.UnmarshalFirst(rest, &item)
		if err != nil {
			return rawBlock{}, nil, malformed("block %d: %v", len(items), err)
		}
		if !canonical(item) {
			return rawBlock{}, nil, malformed("block %d: not deterministically encoded", len(items))
		}
		items = append(items, item)
		rest = r
	}
	if len(rest) != 0 {
		return rawBlock{}, nil, malformed("%d bytes after break", len(rest))
	}
	if len(items) == 0 {
		return rawBlock{}, nil, malformed("no primary block")
	}
	primary, err := parsePrimary(items[0])
	if err != nil {
		return rawBlock{}, nil, err
	}
	blocks := make([]rawBlock, 0, len(items)-1)
	for i, item := range items[1:] {
		blk, err := parseCanonical(item, i+1)
		if err != nil {
			return rawBlock{}, nil, err
		}
		blocks = append(blocks, blk)
	}
	return primary, blocks, nil
}

func parsePrimary(raw cbor.RawMessage) (rawBlock, *Error) {
	arr, ok := decodeArray(raw)
	if !ok || len(arr) < 8 || len(arr) > 11 {
		return rawBlock{}, malformed("primary: not an array of 8..11 items")
	}
	p := rawBlock{raw: raw}
	var ok0, ok1, ok2, ok7 bool
	p.version, ok0 = decodeUint(arr[0])
	p.flags, ok1 = decodeUint(arr[1])
	p.crcType, ok2 = decodeUint(arr[2])
	p.lifetimeMs, ok7 = decodeUint(arr[7])
	if !ok0 || !ok1 || !ok2 || !ok7 {
		return rawBlock{}, malformed("primary: version, flags, CRC type and lifetime must be unsigned integers")
	}
	for i, dst := range []*rawEID{&p.dest, &p.source, &p.reportTo} {
		e, ok := decodeArray(arr[3+i])
		if !ok || len(e) != 2 {
			return rawBlock{}, malformed("primary: endpoint ID %d is not an array of 2 items", i)
		}
		if dst.scheme, ok = decodeUint(e[0]); !ok {
			return rawBlock{}, malformed("primary: endpoint ID %d scheme is not an unsigned integer", i)
		}
		dst.ssp = e[1]
	}
	ts, ok := decodeArray(arr[6])
	if !ok || len(ts) != 2 {
		return rawBlock{}, malformed("primary: creation timestamp is not an array of 2 items")
	}
	var okT, okS bool
	p.dtnTime, okT = decodeUint(ts[0])
	p.seq, okS = decodeUint(ts[1])
	if !okT || !okS {
		return rawBlock{}, malformed("primary: creation timestamp items must be unsigned integers")
	}
	// 要素の数は CRC の有無と断片かどうかで決まる（RFC 9171 §4.3.1）。
	want := 8
	if p.crcType != 0 {
		want++
	}
	fragment := p.flags&flagIsFragment != 0
	if fragment {
		want += 2
	}
	if len(arr) != want {
		return rawBlock{}, malformed("primary: %d items, want %d", len(arr), want)
	}
	if fragment {
		_, okO := decodeUint(arr[8])
		_, okL := decodeUint(arr[9])
		if !okO || !okL {
			return rawBlock{}, malformed("primary: fragment offset and total length must be unsigned integers")
		}
	}
	if p.crcType != 0 {
		if p.crc, ok = decodeBytes(arr[len(arr)-1]); !ok {
			return rawBlock{}, malformed("primary: CRC is not a byte string")
		}
	}
	return p, nil
}

func parseCanonical(raw cbor.RawMessage, index int) (rawBlock, *Error) {
	arr, ok := decodeArray(raw)
	if !ok || len(arr) < 5 || len(arr) > 6 {
		return rawBlock{}, malformed("block %d: not an array of 5..6 items", index)
	}
	b := rawBlock{raw: raw}
	var ok0, ok1, ok2, ok3, ok4 bool
	b.typ, ok0 = decodeUint(arr[0])
	b.num, ok1 = decodeUint(arr[1])
	b.flags, ok2 = decodeUint(arr[2])
	b.crcType, ok3 = decodeUint(arr[3])
	b.data, ok4 = decodeBytes(arr[4])
	if !ok0 || !ok1 || !ok2 || !ok3 || !ok4 {
		return rawBlock{}, malformed("block %d: type, number, flags and CRC type must be unsigned integers and data a byte string", index)
	}
	want := 5
	if b.crcType != 0 {
		want++
	}
	if len(arr) != want {
		return rawBlock{}, malformed("block %d: %d items, want %d", index, len(arr), want)
	}
	if b.crcType != 0 {
		if b.crc, ok = decodeBytes(arr[5]); !ok {
			return rawBlock{}, malformed("block %d: CRC is not a byte string", index)
		}
	}
	if b.typ == blockTypeAge {
		// Bundle Age の data は、最短で符号化した CBOR の uint 1 つ。
		if b.age, ok = decodeUint(b.data); !ok || !canonical(b.data) {
			return rawBlock{}, malformed("block %d: bundle age is not a deterministically encoded unsigned integer", index)
		}
	}
	return b, nil
}
