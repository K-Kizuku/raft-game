package protocol

// Validate は要求の電文 raw を確かめて読む（actor-protocol.md §1・§2.1・§6）。
//
// 確かめる順:
//  1. 長さが lim.MaxMessageBytes を超える → too_large
//  2. JSON として読めない・重複したキー・小数や指数を含む数・範囲外の整数 → invalid
//  3. v が整数でない → invalid、1 でない → unsupported_version
//  4. エンベロープの形（未知のフィールド・足りないフィールド・型）・actor の形・未知の kind・
//     宛先の役割に合わない kind・journal に入れる種類で eventSeq が 0 → invalid
//  5. payload の形と中身 → invalid。手紙が lim.MaxLetters 通を超える・Bundle が lim.MaxBundleBytes を超える
//     → too_large、digest が Bundle の bytes の SHA-256 と違う → invalid
//
// 誤りのときも、読めたところまでの Request を返す（応答の actor・eventSeq に使う）。
func Validate(raw []byte, lim Limits) (Request, *Error) {
	var req Request
	if len(raw) > lim.MaxMessageBytes {
		return req, Errorf(CodeTooLarge, "message: %d bytes exceeds %d", len(raw), lim.MaxMessageBytes)
	}
	jv, err := parseJSON(raw)
	if err != nil {
		return req, err
	}
	if jv.kind != jObject {
		return req, Errorf(CodeInvalid, "message: must be an object")
	}
	v, ok := jv.member("v")
	if !ok || v.kind != jNumber {
		return req, Errorf(CodeInvalid, "v: must be an integer")
	}
	if v.num != Version {
		return req, Errorf(CodeUnsupportedVersion, "v: %d is not supported (want %d)", v.num, Version)
	}
	if err := decodeStrict(jv, raw, &req, "message"); err != nil {
		return req, err
	}
	if !validID(req.SessionID) {
		return req, Errorf(CodeInvalid, "sessionId: invalid")
	}
	role, aerr := req.Actor.parse()
	if aerr != nil {
		return req, aerr
	}
	ki, ok := LookupKind(req.Kind)
	if !ok {
		return req, Errorf(CodeInvalid, "kind: unknown %q", req.Kind)
	}
	if !ki.Accepts(role) {
		return req, Errorf(CodeInvalid, "kind: %q is not for %q", req.Kind, req.Actor)
	}
	if ki.Journaled && req.EventSeq == 0 {
		return req, Errorf(CodeInvalid, "eventSeq: must be >= 1")
	}
	pv, _ := jv.member("payload")
	if _, err := decodePayload(ki, *pv, req.Payload, req.Actor, lim); err != nil {
		return req, err
	}
	return req, nil
}
