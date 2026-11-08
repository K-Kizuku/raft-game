package bpv7

import (
	"bytes"

	"github.com/fxamacker/cbor/v2"
)

// encMode は Core Deterministic Encoding（最短の整数・長さ確定・map のキーの並び）。
// nil の []byte・slice は null にせず空にする（ペイロードが空でも bstr にするため）。
var encMode = func() cbor.EncMode {
	opts := cbor.CoreDetEncOptions()
	opts.NilContainers = cbor.NilContainerAsEmpty
	opts.IndefLength = cbor.IndefLengthForbidden
	opts.TagsMd = cbor.TagsForbidden
	em, err := opts.EncMode()
	if err != nil {
		panic(err)
	}
	return em
}()

// decMode は読み込み用。長さ不定・タグ・map のキーの重複・不正な UTF-8・NaN・無限大を断る。
// 最短の符号化かどうかは、encMode で符号化し直して元の bytes と比べて確かめる（canonical）。
var decMode = func() cbor.DecMode {
	dm, err := cbor.DecOptions{
		DupMapKey:        cbor.DupMapKeyEnforcedAPF,
		IndefLength:      cbor.IndefLengthForbidden,
		TagsMd:           cbor.TagsForbidden,
		MaxNestedLevels:  16,
		MaxArrayElements: 1024,
		MaxMapPairs:      1024,
		UTF8:             cbor.UTF8RejectInvalid,
		NaN:              cbor.NaNDecodeForbidden,
		Inf:              cbor.InfDecodeForbidden,
		IntDec:           cbor.IntDecConvertNone,
		BignumTag:        cbor.BignumTagForbidden,
	}.DecMode()
	if err != nil {
		panic(err)
	}
	return dm
}()

func marshal(v any) ([]byte, error) { return encMode.Marshal(v) }

// canonical は、raw が 1 つの CBOR のデータ項目で、Core Deterministic Encoding の
// とおりに符号化されているかを確かめる。
func canonical(raw []byte) bool {
	var v any
	if err := decMode.Unmarshal(raw, &v); err != nil {
		return false
	}
	again, err := encMode.Marshal(v)
	return err == nil && bytes.Equal(again, raw)
}

// cborHead は CBOR のデータ項目の先頭のバイトの主の型。
func majorType(raw []byte) byte {
	if len(raw) == 0 {
		return 0xff
	}
	return raw[0] >> 5
}

const (
	majorUint  = 0
	majorBytes = 2
	majorArray = 4
)

// decodeUint は raw（1 つのデータ項目）を符号なし整数として読む。
func decodeUint(raw cbor.RawMessage) (uint64, bool) {
	if majorType(raw) != majorUint {
		return 0, false
	}
	var v uint64
	if err := decMode.Unmarshal(raw, &v); err != nil {
		return 0, false
	}
	return v, true
}

// decodeBytes は raw をバイト列（bstr）として読む。
func decodeBytes(raw cbor.RawMessage) ([]byte, bool) {
	if majorType(raw) != majorBytes {
		return nil, false
	}
	var v []byte
	if err := decMode.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	if v == nil {
		v = []byte{}
	}
	return v, true
}

// decodeArray は raw を長さ確定の配列として読み、要素の raw を返す。
func decodeArray(raw cbor.RawMessage) ([]cbor.RawMessage, bool) {
	if majorType(raw) != majorArray {
		return nil, false
	}
	var v []cbor.RawMessage
	if err := decMode.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	return v, true
}
