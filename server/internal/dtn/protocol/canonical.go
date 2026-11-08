package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// 正準の JSON（RFC 8785、JSON Canonicalization Scheme）。actor-protocol.md §1・§5。
//
// この電文の数は整数だけなので、数の規則は整数の部分だけを実装する:
//   - 小数点・指数を含む数（1.0・1e3 も）は invalid
//   - 絶対値が 2^53 − 1（Number.MAX_SAFE_INTEGER）を超える整数は invalid（TS の number で同じ値を表せないため）
//   - -0 は 0 にする（RFC 8785 の数の規則と同じ）
//
// ほかに、重複したキー・不正な UTF-8・対にならないサロゲートの \u エスケープ・JSON の前後の余分な文字は invalid。
// 正準の形は、キーを UTF-16 の符号単位の列の辞書式の順に並べ、空白を入れず、文字列は ", \, 制御文字だけを
// エスケープする（\b \t \n \f \r と、ほかの制御文字は \u00xx の小文字の 16 進）。

// MaxSafeInteger は JSON の数として受ける整数の絶対値の上限（2^53 − 1）。
const MaxSafeInteger = 1<<53 - 1

// maxDepth は JSON の入れ子の深さの上限。この電文は深くても 8 程度。
const maxDepth = 32

type jkind uint8

const (
	jNull jkind = iota
	jBool
	jNumber
	jString
	jArray
	jObject
)

// jvalue は厳密に読んだ JSON の値。
type jvalue struct {
	kind jkind
	b    bool
	num  int64
	str  string
	arr  []jvalue
	obj  []jmember // 書かれた順（重複はない）
}

type jmember struct {
	key string
	val jvalue
}

// member は obj のキー k の値を返す。
func (v *jvalue) member(k string) (*jvalue, bool) {
	for i := range v.obj {
		if v.obj[i].key == k {
			return &v.obj[i].val, true
		}
	}
	return nil, false
}

// Canonical は v の正準の JSON を返す。v が json.RawMessage ならその JSON を、それ以外は encoding/json で
// 符号化したものを正準にする。小数・指数・範囲外の整数などは *Error（invalid）。
func Canonical(v any) ([]byte, error) {
	var raw []byte
	switch x := v.(type) {
	case json.RawMessage:
		raw = x
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, Errorf(CodeInvalid, "canonical: %v", err)
		}
		raw = b
	}
	return CanonicalJSON(raw)
}

// CanonicalJSON は JSON の文書 raw の正準の JSON を返す。
func CanonicalJSON(raw []byte) ([]byte, error) {
	jv, err := parseJSON(raw)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writeCanonical(&buf, jv)
	return buf.Bytes(), nil
}

// Digest は b の SHA-256 を "sha256:" ＋ 16 進の小文字で返す。
func Digest(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

// CanonicalDigest は v の正準の JSON の Digest を返す。
func CanonicalDigest(v any) (string, error) {
	c, err := Canonical(v)
	if err != nil {
		return "", err
	}
	return Digest(c), nil
}

// RequestDigest は要求の digest（actor-protocol.md §5）。{v, sessionId, actor, eventSeq, kind, payload} の
// 正準の JSON の SHA-256。epoch・coordEpoch・replay は含めない（世代が変わった後の送り直しを conflict にしないため）。
func RequestDigest(req Request) (string, error) {
	payload := req.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	pv, err := parseJSON(payload)
	if err != nil {
		return "", err
	}
	if req.V > MaxSafeInteger || req.EventSeq > MaxSafeInteger {
		return "", Errorf(CodeInvalid, "number out of range")
	}
	obj := jvalue{kind: jObject, obj: []jmember{
		{"v", jvalue{kind: jNumber, num: int64(req.V)}},
		{"sessionId", jvalue{kind: jString, str: req.SessionID}},
		{"actor", jvalue{kind: jString, str: string(req.Actor)}},
		{"eventSeq", jvalue{kind: jNumber, num: int64(req.EventSeq)}},
		{"kind", jvalue{kind: jString, str: string(req.Kind)}},
		{"payload", pv},
	}}
	for _, s := range []string{req.SessionID, string(req.Actor), string(req.Kind)} {
		if !utf8.ValidString(s) {
			return "", Errorf(CodeInvalid, "invalid UTF-8")
		}
	}
	var buf bytes.Buffer
	writeCanonical(&buf, obj)
	return Digest(buf.Bytes()), nil
}

// ResponseDigest は応答の digest。{v, actor, eventSeq, status, outputs, cursor, error} の正準の JSON の SHA-256。
// epoch（世代が変わった後の再生でも同じになるように）と outputs[].bundle（Host が保存する応答には bytes を
// 入れないので。bundle の digest は data に残る）を含めない。persistence-and-recovery.md §4.1 の照合に使う。
func ResponseDigest(resp Response) (string, error) {
	outs := make([]Output, len(resp.Outputs))
	for i, o := range resp.Outputs {
		o.Bundle = nil
		outs[i] = o
	}
	type view struct {
		V        int      `json:"v"`
		Actor    ActorRef `json:"actor"`
		EventSeq uint64   `json:"eventSeq"`
		Status   Status   `json:"status"`
		Outputs  []Output `json:"outputs"`
		Cursor   *Cursor  `json:"cursor"`
		Error    *Error   `json:"error"`
	}
	return CanonicalDigest(view{resp.V, resp.Actor, resp.EventSeq, resp.Status, outs, resp.Cursor, resp.Error})
}

// --- 読み込み ---

type jparser struct {
	s   []byte
	pos int
}

func parseJSON(raw []byte) (jvalue, *Error) {
	p := &jparser{s: raw}
	p.ws()
	v, err := p.value(0)
	if err != nil {
		return jvalue{}, err
	}
	p.ws()
	if p.pos != len(p.s) {
		return jvalue{}, p.errf("trailing data")
	}
	return v, nil
}

func (p *jparser) errf(format string, args ...any) *Error {
	e := Errorf(CodeInvalid, format, args...)
	e.Message = "json: " + e.Message + " at offset " + strconv.Itoa(p.pos)
	return e
}

func (p *jparser) ws() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jparser) value(depth int) (jvalue, *Error) {
	if depth > maxDepth {
		return jvalue{}, p.errf("too deep")
	}
	if p.pos >= len(p.s) {
		return jvalue{}, p.errf("unexpected end")
	}
	switch c := p.s[p.pos]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		s, err := p.str()
		return jvalue{kind: jString, str: s}, err
	case c == '-' || (c >= '0' && c <= '9'):
		n, err := p.number()
		return jvalue{kind: jNumber, num: n}, err
	case p.lit("true"):
		return jvalue{kind: jBool, b: true}, nil
	case p.lit("false"):
		return jvalue{kind: jBool}, nil
	case p.lit("null"):
		return jvalue{kind: jNull}, nil
	default:
		return jvalue{}, p.errf("unexpected character %q", c)
	}
}

func (p *jparser) lit(w string) bool {
	if bytes.HasPrefix(p.s[p.pos:], []byte(w)) {
		p.pos += len(w)
		return true
	}
	return false
}

func (p *jparser) object(depth int) (jvalue, *Error) {
	p.pos++ // {
	v := jvalue{kind: jObject, obj: []jmember{}}
	p.ws()
	if p.pos < len(p.s) && p.s[p.pos] == '}' {
		p.pos++
		return v, nil
	}
	for {
		p.ws()
		if p.pos >= len(p.s) || p.s[p.pos] != '"' {
			return jvalue{}, p.errf("expected object key")
		}
		k, err := p.str()
		if err != nil {
			return jvalue{}, err
		}
		if _, dup := v.member(k); dup {
			return jvalue{}, p.errf("duplicate key %q", k)
		}
		p.ws()
		if p.pos >= len(p.s) || p.s[p.pos] != ':' {
			return jvalue{}, p.errf("expected ':'")
		}
		p.pos++
		p.ws()
		m, err := p.value(depth + 1)
		if err != nil {
			return jvalue{}, err
		}
		v.obj = append(v.obj, jmember{key: k, val: m})
		p.ws()
		if p.pos >= len(p.s) {
			return jvalue{}, p.errf("unexpected end")
		}
		switch p.s[p.pos] {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return v, nil
		default:
			return jvalue{}, p.errf("expected ',' or '}'")
		}
	}
}

func (p *jparser) array(depth int) (jvalue, *Error) {
	p.pos++ // [
	v := jvalue{kind: jArray, arr: []jvalue{}}
	p.ws()
	if p.pos < len(p.s) && p.s[p.pos] == ']' {
		p.pos++
		return v, nil
	}
	for {
		p.ws()
		e, err := p.value(depth + 1)
		if err != nil {
			return jvalue{}, err
		}
		v.arr = append(v.arr, e)
		p.ws()
		if p.pos >= len(p.s) {
			return jvalue{}, p.errf("unexpected end")
		}
		switch p.s[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return v, nil
		default:
			return jvalue{}, p.errf("expected ',' or ']'")
		}
	}
}

// number は整数だけを受ける。
func (p *jparser) number() (int64, *Error) {
	start := p.pos
	if p.s[p.pos] == '-' {
		p.pos++
	}
	digits := p.pos
	for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
		p.pos++
	}
	nd := p.pos - digits
	if nd == 0 {
		return 0, p.errf("invalid number")
	}
	if nd > 1 && p.s[digits] == '0' {
		return 0, p.errf("leading zero in number")
	}
	if p.pos < len(p.s) {
		switch p.s[p.pos] {
		case '.', 'e', 'E':
			return 0, p.errf("non-integer number (fraction or exponent)")
		}
	}
	if nd > 16 {
		return 0, p.errf("integer out of range")
	}
	n, err := strconv.ParseInt(string(p.s[start:p.pos]), 10, 64)
	if err != nil || n > MaxSafeInteger || n < -MaxSafeInteger {
		return 0, p.errf("integer out of range")
	}
	return n, nil // "-0" は 0
}

func (p *jparser) str() (string, *Error) {
	p.pos++ // "
	var b []byte
	for {
		if p.pos >= len(p.s) {
			return "", p.errf("unterminated string")
		}
		c := p.s[p.pos]
		switch {
		case c == '"':
			p.pos++
			return string(b), nil
		case c == '\\':
			p.pos++
			if p.pos >= len(p.s) {
				return "", p.errf("unterminated string")
			}
			e := p.s[p.pos]
			p.pos++
			switch e {
			case '"', '\\', '/':
				b = append(b, e)
			case 'b':
				b = append(b, '\b')
			case 'f':
				b = append(b, '\f')
			case 'n':
				b = append(b, '\n')
			case 'r':
				b = append(b, '\r')
			case 't':
				b = append(b, '\t')
			case 'u':
				r, err := p.hex4()
				if err != nil {
					return "", err
				}
				if utf16.IsSurrogate(r) {
					if r >= 0xDC00 || !p.lit(`\u`) {
						return "", p.errf("lone surrogate")
					}
					r2, err := p.hex4()
					if err != nil {
						return "", err
					}
					r = utf16.DecodeRune(r, r2)
					if r == utf8.RuneError {
						return "", p.errf("lone surrogate")
					}
				}
				b = utf8.AppendRune(b, r)
			default:
				return "", p.errf("invalid escape")
			}
		case c < 0x20:
			return "", p.errf("control character in string")
		case c < utf8.RuneSelf:
			b = append(b, c)
			p.pos++
		default:
			r, size := utf8.DecodeRune(p.s[p.pos:])
			if r == utf8.RuneError && size <= 1 {
				return "", p.errf("invalid UTF-8")
			}
			b = append(b, p.s[p.pos:p.pos+size]...)
			p.pos += size
		}
	}
}

func (p *jparser) hex4() (rune, *Error) {
	if p.pos+4 > len(p.s) {
		return 0, p.errf("invalid \\u escape")
	}
	n, err := strconv.ParseUint(string(p.s[p.pos:p.pos+4]), 16, 32)
	if err != nil {
		return 0, p.errf("invalid \\u escape")
	}
	p.pos += 4
	return rune(n), nil
}

// --- 書き出し ---

func writeCanonical(buf *bytes.Buffer, v jvalue) {
	switch v.kind {
	case jNull:
		buf.WriteString("null")
	case jBool:
		if v.b {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case jNumber:
		buf.WriteString(strconv.FormatInt(v.num, 10))
	case jString:
		writeString(buf, v.str)
	case jArray:
		buf.WriteByte('[')
		for i, e := range v.arr {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonical(buf, e)
		}
		buf.WriteByte(']')
	case jObject:
		ms := slices.Clone(v.obj)
		slices.SortFunc(ms, func(a, b jmember) int { return compareUTF16(a.key, b.key) })
		buf.WriteByte('{')
		for i, m := range ms {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, m.key)
			buf.WriteByte(':')
			writeCanonical(buf, m.val)
		}
		buf.WriteByte('}')
	}
}

func writeString(buf *bytes.Buffer, s string) {
	const hexdigits = "0123456789abcdef"
	buf.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\t':
			buf.WriteString(`\t`)
		case '\n':
			buf.WriteString(`\n`)
		case '\f':
			buf.WriteString(`\f`)
		case '\r':
			buf.WriteString(`\r`)
		default:
			if c < 0x20 {
				buf.WriteString(`\u00`)
				buf.WriteByte(hexdigits[c>>4])
				buf.WriteByte(hexdigits[c&0xF])
			} else {
				buf.WriteByte(c)
			}
		}
	}
	buf.WriteByte('"')
}

// compareUTF16 は a と b を UTF-16 の符号単位の列として比べる（RFC 8785 §3.2.3）。
func compareUTF16(a, b string) int {
	return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
}
