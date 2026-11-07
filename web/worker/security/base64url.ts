// base64url（RFC 4648 §5。`=` の埋め草を付けない）の符号化と、厳密な復号（T-310）。
//
// 復号は「復号して符号化し直すと元の文字列に戻る」ものだけを受け付ける。`+`・`/`・`=`・空白、
// 4 で割って 1 余る長さ、末尾の余りのビットが 0 でないものは null。

const ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
const BASE64URL_PATTERN = /^[A-Za-z0-9_-]*$/;

const DECODE_TABLE: ReadonlyMap<string, number> = new Map(
  [...ALPHABET].map((char, index) => [char, index]),
);

function char(index: number): string {
  return ALPHABET.charAt(index & 0x3f);
}

export function encodeBase64Url(bytes: Uint8Array): string {
  let out = "";
  for (let i = 0; i < bytes.length; i += 3) {
    const b0 = bytes[i] ?? 0;
    const b1 = bytes[i + 1] ?? 0;
    const b2 = bytes[i + 2] ?? 0;
    const remaining = bytes.length - i;
    out += char(b0 >> 2);
    out += char(((b0 & 0x03) << 4) | (b1 >> 4));
    if (remaining > 1) out += char(((b1 & 0x0f) << 2) | (b2 >> 6));
    if (remaining > 2) out += char(b2);
  }
  return out;
}

export function decodeBase64Url(text: string): Uint8Array<ArrayBuffer> | null {
  if (!BASE64URL_PATTERN.test(text) || text.length % 4 === 1) return null;
  const out = new Uint8Array(Math.floor((text.length * 3) / 4));
  let buffer = 0;
  let bits = 0;
  let offset = 0;
  for (const c of text) {
    const value = DECODE_TABLE.get(c);
    if (value === undefined) return null;
    buffer = ((buffer << 6) | value) & 0xffffff;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out[offset] = (buffer >> bits) & 0xff;
      offset += 1;
    }
  }
  // 末尾の余りのビットは 0 でなければならない（同じバイト列の別の表記を受け付けない）。
  if ((buffer & ((1 << bits) - 1)) !== 0) return null;
  return out;
}
