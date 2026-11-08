#!/usr/bin/env python3
"""DTN-06（#163）の fixture を作る。Go の実装と fxamacker/cbor を使わずに、CBOR の
バイトを手で組み立て、CRC-32C をビットごとの計算で求める（独立した検算）。

使い方: python3 -I mkfixtures.py <出力ディレクトリ>   （ふつうは testdata/ 自身）
手順の説明は README.md。
"""
import os
import sys


# --- CRC-32C（Castagnoli、反転した多項式 0x82F63B78）をビットごとに計算する ---
def crc32c(data: bytes) -> int:
    crc = 0xFFFFFFFF
    for byte in data:
        crc ^= byte
        for _ in range(8):
            crc = (crc >> 1) ^ 0x82F63B78 if crc & 1 else crc >> 1
    return crc ^ 0xFFFFFFFF


assert crc32c(b"123456789") == 0xE3069283  # CRC-32C の検査値（RFC 3720 B.4）


# --- CBOR の頭（RFC 8949 §3）。最短の引数で書く ---
def head(major: int, n: int) -> bytes:
    if n < 24:
        return bytes([major << 5 | n])
    for ai, size in ((24, 1), (25, 2), (26, 4), (27, 8)):
        if n < 1 << (8 * size):
            return bytes([major << 5 | ai]) + n.to_bytes(size, "big")
    raise ValueError(n)


def u(n: int) -> bytes:
    return head(0, n)


def bstr(b: bytes) -> bytes:
    return head(2, len(b)) + b


def tstr(s: str) -> bytes:
    b = s.encode()
    return head(3, len(b)) + b


def arr(*items: bytes) -> bytes:
    return head(4, len(items)) + b"".join(items)


def ipn(node: int, service: int) -> bytes:
    return arr(u(2), arr(u(node), u(service)))


DTN_NONE = arr(u(1), u(0))
ZERO_CRC = bstr(b"\x00\x00\x00\x00")


def stamp(block: bytes) -> bytes:
    """CRC の欄（末尾の 44 00 00 00 00）を 0 にしたまま CRC-32C を計算し、末尾に入れる。"""
    assert block.endswith(ZERO_CRC), block.hex()
    return block[:-4] + crc32c(block).to_bytes(4, "big")


def primary(dest, src, report=DTN_NONE, seq=1, lifetime=660000, version=7, flags=4, dtn_time=0,
            extra=b"", ts=None, lifetime_raw=None):
    ts = ts if ts is not None else arr(u(dtn_time), u(seq))
    lt = lifetime_raw if lifetime_raw is not None else u(lifetime)
    n = 9 + (2 if extra else 0)
    return stamp(head(4, n) + u(version) + u(flags) + u(2) + dest + src + report + ts + lt + extra + ZERO_CRC)


def block(typ, num, data, flags=0):
    return stamp(arr(u(typ), u(num), u(flags), u(2), bstr(data), ZERO_CRC))


def age_block(age_ms, num=2, flags=0):
    return block(7, num, u(age_ms), flags)


def payload_block(payload, num=1, flags=0):
    return block(1, num, payload, flags)


def bundle(*blocks: bytes) -> bytes:
    return b"\x9f" + b"".join(blocks) + b"\xff"


# --- ペイロード ---
def pb_varint(n: int) -> bytes:
    out = b""
    while True:
        b = n & 0x7F
        n >>= 7
        if n:
            out += bytes([b | 0x80])
        else:
            return out + bytes([b])


def pb_u(field: int, n: int) -> bytes:  # wire type 0
    return pb_varint(field << 3) + pb_varint(n)


def pb_bytes(field: int, b: bytes) -> bytes:  # wire type 2
    return pb_varint(field << 3 | 2) + pb_varint(len(b)) + b


# raftpb.Message: type=1 to=2 from=3 term=4 logTerm=5 index=6 entries=7 commit=8
# raftpb.Entry:   Type=1 Term=2 Index=3 Data=4（proto2・明示的な存在。値を入れた欄だけを出す）
HEARTBEAT_PB = pb_u(1, 8) + pb_u(2, 2) + pb_u(3, 1) + pb_u(4, 3) + pb_u(8, 5)
assert HEARTBEAT_PB.hex() == "08081002180120034005"

APP_PB = (pb_u(1, 3) + pb_u(2, 1) + pb_u(3, 3) + pb_u(4, 2) + pb_u(5, 2) + pb_u(6, 4)
          + pb_bytes(7, pb_u(2, 2) + pb_u(3, 5) + pb_bytes(4, b"abc"))
          + pb_bytes(7, pb_u(2, 2) + pb_u(3, 6) + pb_bytes(4, b"de"))
          + pb_u(8, 4))

COMMAND = b'{"v":1,"reqId":"r-1","origin":2,"kind":"firework.reserve","resource":"firework:a","amount":0}'
REQUEST_CBOR = head(5, 3) + u(1) + tstr("r-1") + u(2) + u(1) + u(3) + bstr(COMMAND)

# --- 正しい Bundle（TestEncodeFixtures・TestDecodeFixtures） ---
# heartbeat: letterId 1_000_000_001（島 1 の 1 通目）。MsgHeartbeat 1 → 2、term 3、commit 5。
HB_PRIMARY = primary(ipn(2, 1), ipn(1, 1), seq=1)
HB_AGE = age_block(1500)
HB_PAYLOAD = payload_block(HEARTBEAT_PB)
HEARTBEAT = bundle(HB_PRIMARY, HB_AGE, HB_PAYLOAD)
# 手で書いたバイトと一致すること（README.md の表と同じ）。
assert HB_PRIMARY.hex()[:-8] == "89070402" "8202820201" "8202820101" "820100" "820001" "1a000a1220" "44"
assert HB_AGE.hex()[:-8] == "860702000243" "1905dc" "44"
assert HB_PAYLOAD.hex()[:-8] == "86010100024a" "08081002180120034005" "44"

GOOD = {
    "heartbeat": HEARTBEAT,
    # letterId 3_999_999_999（島 3 の 999,999,999 通目）。MsgApp 3 → 1、エントリ 2 つ。Age 0。
    "msgapp": bundle(primary(ipn(1, 1), ipn(3, 1), seq=999_999_999), age_block(0), payload_block(APP_PB)),
    # letterId 2_000_000_007。依頼書 2 → 3（hops 1）。Age 2^32 + 1（ms）。
    "request": bundle(primary(ipn(3, 2), ipn(2, 2), seq=7), age_block(2**32 + 1), payload_block(REQUEST_CBOR)),
}


def flip(b: bytes, i: int, bit: int = 0) -> bytes:
    return b[:i] + bytes([b[i] ^ (1 << bit)]) + b[i + 1:]


P, A, L = HB_PRIMARY, HB_AGE, HB_PAYLOAD
# --- 断るべき Bundle（TestDecodeErrors）。ファイル名の最初の "." の前が期待するコード ---
BAD = {
    "bpv7_malformed.empty": b"",
    "bpv7_malformed.no_blocks": bundle(),
    "bpv7_malformed.definite_outer": arr(P, A, L),
    "bpv7_malformed.missing_break": HEARTBEAT[:-1],
    "bpv7_malformed.truncated": HEARTBEAT[:-12],
    "bpv7_malformed.garbage_after_break": HEARTBEAT + b"\x00",
    "bpv7_malformed.not_shortest_int": bundle(
        primary(ipn(2, 1), ipn(1, 1), lifetime_raw=b"\x1b" + (660000).to_bytes(8, "big")), A, L),
    "bpv7_malformed.block_not_array": bundle(P, A, u(5)),
    "bpv7_malformed.age_not_uint": bundle(P, block(7, 2, tstr("x")), L),
    "bpv7_version.v6": bundle(primary(ipn(2, 1), ipn(1, 1), version=6), A, L),
    "bpv7_unsupported_flags.fragment": bundle(
        primary(ipn(2, 1), ipn(1, 1), flags=5, extra=u(0) + u(100)), A, L),
    "bpv7_unsupported_flags.admin_record": bundle(primary(ipn(2, 1), ipn(1, 1), flags=6), A, L),
    "bpv7_unsupported_flags.no_flags": bundle(primary(ipn(2, 1), ipn(1, 1), flags=0), A, L),
    "bpv7_unsupported_flags.payload_block_flags": bundle(P, A, payload_block(HEARTBEAT_PB, flags=0x10)),
    # 1 ビットの違い。中身のバイトを変え、CRC の欄はそのまま。
    "bpv7_crc.primary_bit": bundle(flip(P, len(P) - 7), A, L),  # lifetime の最後のバイト
    "bpv7_crc.age_bit": bundle(P, flip(A, len(A) - 6), L),  # Age の値の最後のバイト
    "bpv7_crc.payload_bit": bundle(P, A, flip(L, len(L) - 6)),  # ペイロードの最後のバイト
    "bpv7_crc.crc_bit": bundle(P, A, flip(L, len(L) - 1, 7)),  # CRC の欄そのもの
    "bpv7_crc.no_crc": bundle(P, A, arr(u(1), u(1), u(0), u(0), bstr(HEARTBEAT_PB))),
    "bpv7_eid.source_ferry": bundle(primary(ipn(2, 1), ipn(1001, 0)), A, L),
    "bpv7_eid.source_island_1000": bundle(primary(ipn(2, 1), ipn(1000, 1)), A, L),
    "bpv7_eid.dest_none": bundle(primary(DTN_NONE, ipn(1, 1)), A, L),
    "bpv7_eid.service_3": bundle(primary(ipn(2, 3), ipn(1, 3)), A, L),
    "bpv7_eid.service_mismatch": bundle(primary(ipn(2, 2), ipn(1, 1)), A, L),
    "bpv7_eid.report_to_ipn": bundle(primary(ipn(2, 1), ipn(1, 1), report=ipn(1, 1)), A, L),
    "bpv7_eid.dtn_uri": bundle(primary(ipn(2, 1), ipn(1, 1), report=arr(u(1), tstr("//x/"))), A, L),
    "bpv7_eid.ipn_three_items": bundle(primary(arr(u(2), arr(u(0), u(2), u(1))), ipn(1, 1)), A, L),
    "bpv7_timestamp.seq_0": bundle(primary(ipn(2, 1), ipn(1, 1), seq=0), A, L),
    "bpv7_timestamp.seq_1e9": bundle(primary(ipn(2, 1), ipn(1, 1), seq=10**9), A, L),
    "bpv7_timestamp.dtn_time": bundle(primary(ipn(2, 1), ipn(1, 1), dtn_time=1), A, L),
    "bpv7_age.missing": bundle(P, L),
    "bpv7_age.two": bundle(P, A, age_block(1, num=3), L),
    "bpv7_unsupported_block.hop_count": bundle(P, A, block(10, 3, arr(u(30), u(0))), L),
    "bpv7_unsupported_block.previous_node": bundle(P, A, block(6, 3, ipn(1001, 0)), L),
    "bpv7_unsupported_block.unknown_critical": bundle(P, A, block(192, 3, b"\x00", flags=0x04), L),
    "bpv7_structure.payload_not_last": bundle(P, L, A),
    "bpv7_structure.duplicate_number": bundle(P, age_block(1500, num=1), L),
    "bpv7_structure.payload_number": bundle(P, A, payload_block(HEARTBEAT_PB, num=3)),
    "bpv7_structure.age_number": bundle(P, age_block(1500, num=5), L),
    "bpv7_structure.no_payload": bundle(P, A),
    "bpv7_structure.two_payloads": bundle(P, A, payload_block(b"x", num=3), L),
}

# 16 KiB を 1 バイト超える（形は正しい）。ペイロードの長さを調整する。
def sized(total: int) -> bytes:
    n = total - len(bundle(P, A, payload_block(b"")))
    while True:
        b = bundle(P, A, payload_block(b"\xaa" * n))
        if len(b) == total:
            return b
        n -= len(b) - total


BAD["bpv7_too_large.16385"] = sized(16385)


def write_hex(path: str, data: bytes, note: str) -> None:
    with open(path, "w") as f:
        f.write("# " + note + "\n")
        for i in range(0, len(data), 32):
            f.write(data[i:i + 32].hex() + "\n")


def main() -> None:
    out = sys.argv[1]
    os.makedirs(os.path.join(out, "errors"), exist_ok=True)
    notes = {
        "heartbeat": "MsgHeartbeat 1 -> 2 (term 3, commit 5), letterId 1000000001, age 1500, lifetime 660000",
        "msgapp": "MsgApp 3 -> 1 (2 entries), letterId 3999999999, age 0, lifetime 660000",
        "request": "request 2 -> 3 (reqId r-1, hops 1), letterId 2000000007, age 4294967297, lifetime 660000",
    }
    for name, data in GOOD.items():
        write_hex(os.path.join(out, name + ".hex"), data, notes[name] + " (mkfixtures.py)")
    for name, data in BAD.items():
        write_hex(os.path.join(out, "errors", name + ".hex"), data, name + " (mkfixtures.py)")
    for name, data in GOOD.items():
        print(name, len(data), "bytes")
    print("primary crc", HB_PRIMARY[-4:].hex(), "age crc", HB_AGE[-4:].hex(), "payload crc", HB_PAYLOAD[-4:].hex())


if __name__ == "__main__":
    main()
