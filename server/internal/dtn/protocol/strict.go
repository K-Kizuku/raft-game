package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
)

// 厳密な読み込み。encoding/json の DisallowUnknownFields はキーを大文字と小文字を区別せずに照合し、
// 足りないフィールドも誤りにしないので、先に厳密に読んだ値（jvalue）と Go の型を突き合わせる:
//   - 型にないキー（大文字と小文字だけ違うものも）は invalid
//   - omitempty の付かないフィールドは必須（無い・null は invalid）
//   - JSON の種類（文字列・数・真偽・配列・オブジェクト）が型と合わなければ invalid、整数は型の範囲に収まること
//
// json.Unmarshaler を実装する型（Mark など）と json.RawMessage は、中身を見ない（それぞれが確かめる）。

var (
	unmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	rawMessageType  = reflect.TypeFor[json.RawMessage]()
)

// decodeStrict は raw（厳密に読んだ値が jv）を dst（構造体へのポインタ）に読む。
func decodeStrict(jv jvalue, raw []byte, dst any, path string) *Error {
	if err := checkShape(jv, reflect.TypeOf(dst).Elem(), path); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if e, ok := errors.AsType[*Error](err); ok {
			return e
		}
		return Errorf(CodeInvalid, "%s: %v", path, err)
	}
	return nil
}

type fieldInfo struct {
	name     string
	typ      reflect.Type
	optional bool
}

// jsonFields は構造体 t の JSON のフィールド（埋め込みの構造体は平らにする）。
func jsonFields(t reflect.Type) []fieldInfo {
	var out []fieldInfo
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			out = append(out, jsonFields(f.Type)...)
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, fieldInfo{name: name, typ: f.Type, optional: strings.Contains(","+opts+",", ",omitempty,")})
	}
	return out
}

func checkShape(v jvalue, t reflect.Type, path string) *Error {
	if t == rawMessageType || reflect.PointerTo(t).Implements(unmarshalerType) || t.Implements(unmarshalerType) {
		return nil
	}
	bad := func(want string) *Error { return Errorf(CodeInvalid, "%s: must be %s", path, want) }
	switch t.Kind() {
	case reflect.Pointer:
		if v.kind == jNull {
			return nil
		}
		return checkShape(v, t.Elem(), path)
	case reflect.Struct:
		if v.kind != jObject {
			return bad("an object")
		}
		fields := jsonFields(t)
		for _, m := range v.obj {
			var fi *fieldInfo
			for i := range fields {
				if fields[i].name == m.key {
					fi = &fields[i]
					break
				}
			}
			if fi == nil {
				return Errorf(CodeInvalid, "%s: unknown field %q", path, m.key)
			}
			if m.val.kind == jNull {
				if fi.optional {
					continue
				}
				return Errorf(CodeInvalid, "%s.%s: must not be null", path, m.key)
			}
			if err := checkShape(m.val, fi.typ, path+"."+m.key); err != nil {
				return err
			}
		}
		for _, fi := range fields {
			if _, ok := v.member(fi.name); !ok && !fi.optional {
				return Errorf(CodeInvalid, "%s: missing field %q", path, fi.name)
			}
		}
		return nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			if v.kind != jString {
				return bad("a base64 string")
			}
			// RFC 4648 §4 のパディングありの形だけを受ける（改行などを含む別の書き方を断り、TS と同じ bytes にする）
			b, err := base64.StdEncoding.Strict().DecodeString(v.str)
			if err != nil || base64.StdEncoding.EncodeToString(b) != v.str {
				return bad("a padded standard base64 string")
			}
			return nil
		}
		if v.kind != jArray {
			return bad("an array")
		}
		for i, e := range v.arr {
			if err := checkShape(e, t.Elem(), path+"["+strconv.Itoa(i)+"]"); err != nil {
				return err
			}
		}
		return nil
	case reflect.String:
		if v.kind != jString {
			return bad("a string")
		}
		return nil
	case reflect.Bool:
		if v.kind != jBool {
			return bad("a boolean")
		}
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.kind != jNumber {
			return bad("an integer")
		}
		if reflect.Zero(t).OverflowInt(v.num) {
			return bad("an integer in range")
		}
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v.kind != jNumber {
			return bad("an integer")
		}
		if v.num < 0 || reflect.Zero(t).OverflowUint(uint64(v.num)) {
			return bad("a non-negative integer in range")
		}
		return nil
	}
	return Errorf(CodeInternal, "%s: unsupported type %s", path, t)
}
