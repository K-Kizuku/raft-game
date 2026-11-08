// Package bpv7 は、島と船の間を動く手紙を RFC 9171 の単一宛先・非フラグメントの
// プロファイルの Bundle として符号化・読み込みする（DTN-06 #163）。
//
// 契約は docs/dtn/bpv7-profile.md と ADR-D003。RFC 9171 の一部だけを実装するので、
// 「BPv7 に準拠」とは書かない。外部の DTN の実装とつながることも保証しない。
//
//   - Bundle は長さ不定の CBOR の配列（0x9f … 0xff）。並びは Primary → Bundle Age
//     （type 7・ブロック番号 2）→ Payload（type 1・ブロック番号 1、最後）
//   - CRC はすべてのブロックで CRC-32C。CBOR は fxamacker/cbor/v2 の
//     Core Deterministic Encoding。外側の配列だけを自前で書く
//   - 読み込みは bpv7-profile.md §6 の順に検査し、最初の失敗を *Error で返す。
//     黙って読み飛ばすブロックはない
//
// 実時間・乱数・浮動小数点を使わない（決定的。forbidigo の対象）。
package bpv7
