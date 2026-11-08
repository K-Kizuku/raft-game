# bpv7 の fixture（DTN-06 #163）

`server/internal/dtn/bpv7` の試験が読む、16 進で書いた Bundle。プロファイルは [bpv7-profile.md](../../../../../docs/dtn/bpv7-profile.md)。

| ファイル | 中身 | 使う試験 |
|---|---|---|
| `heartbeat.hex` | `MsgHeartbeat` 1 → 2（term 3・commit 5）。letterId 1,000,000,001・Age 1,500・Lifetime 660,000 | `TestEncodeFixtures`・`TestDecodeFixtures`・`TestLetterCodecFixtures`・`TestCRCLayout`・`TestSetAge` |
| `msgapp.hex` | `MsgApp` 3 → 1（エントリ 2 つ）。letterId 3,999,999,999（連番の最大）・Age 0 | 同上 |
| `request.hex` | 依頼書 2 → 3（`reqId` `r-1`・hops 1・`ledger.Command` の JSON）。letterId 2,000,000,007・Age 2^32 + 1 | 同上 |
| `errors/<code>.<説明>.hex` | 断るべき Bundle。ファイル名の最初の `.` の前が期待するエラーコード | `TestDecodeErrors` |

すべての `.hex` は `FuzzDecode` の種にもなる。`#` で始まる行と空白は読み飛ばす。

## 作り方と検算

`mkfixtures.py` が作る（`python3 -I mkfixtures.py .`）。Go の実装と `fxamacker/cbor` を使わず、

- CBOR の頭（主の型と最短の引数）を RFC 8949 §3 のとおりに 1 バイトずつ組み立て、
- CRC-32C を、反転した多項式 `0x82F63B78` でビットごとに計算する（検査値 `crc32c("123456789") = e3069283` を確かめる）。

Go の `Encode` がこれとバイト単位で一致することを `TestEncodeFixtures` で、`Decode` が期待する値に戻すことを `TestDecodeFixtures` で確かめる。独立した BPv7 の実装（µD3TN の `pyd3tn` など）で作った Bundle は、まだ入れていない（外部のコードを実行する許可が取れなかったため。DTN-06 のコミットを参照）。

### `heartbeat.hex` を 1 バイトずつ

```
9f                                  外側: 長さ不定の配列の始まり
  89                                Primary Block: 長さ確定の配列（9 要素）
    07                              版 7
    04                              Bundle の処理の制御フラグ 0x0004（断片にしてはならない）
    02                              CRC の種類 2（CRC-32C）
    82 02 82 02 01                  宛先 ipn:2.1 = [2, [2, 1]]
    82 02 82 01 01                  送信元 ipn:1.1 = [2, [1, 1]]
    82 01 00                        Report-to dtn:none = [1, 0]
    82 00 01                        作成時刻 [0, 1]（DTN time 0・連番 1）
    1a 00 0a 12 20                  Lifetime 660,000 ms
    44 34 c8 53 c5                  CRC（4 バイトの bstr）
  86                                Bundle Age Block: 長さ確定の配列（6 要素）
    07 02 00 02                     type 7・ブロック番号 2・フラグ 0・CRC の種類 2
    43 19 05 dc                     data: 3 バイトの bstr。中身は CBOR の uint 1,500（19 05 dc）
    44 2f 8e 92 bf                  CRC
  86                                Payload Block（6 要素）
    01 01 00 02                     type 1・ブロック番号 1・フラグ 0・CRC の種類 2
    4a 08 08 10 02 18 01 20 03 40 05   data: 10 バイトの bstr（raftpb.Message の protobuf）
    44 7b 90 42 5c                  CRC
ff                                  外側の終わり（break）
```

protobuf（`proto.MarshalOptions{Deterministic: true}`。proto2 で値を入れた欄だけが、欄の番号の順に出る）:

| bytes | 欄 |
|---|---|
| `08 08` | type（1）= 8（MsgHeartbeat） |
| `10 02` | to（2）= 2 |
| `18 01` | from（3）= 1 |
| `20 03` | term（4）= 3 |
| `40 05` | commit（8）= 5 |

### CRC

各ブロックの CRC は「CRC の欄を `44 00 00 00 00` にしたブロックの CBOR 全体」に対する CRC-32C を、4 バイトのビッグエンディアンで入れたもの。

| ブロック | CRC を計算する bytes（CRC の欄は 0） | CRC-32C |
|---|---|---|
| Primary | `89070402 8202820201 8202820101 820100 820001 1a000a1220 4400000000` | `34c853c5` |
| Bundle Age | `8607020002 431905dc 4400000000` | `2f8e92bf` |
| Payload | `86010100024a08081002180120034005 4400000000` | `7b90425c` |
| `msgapp` の Primary・Age・Payload | `mkfixtures.py` の `GOOD["msgapp"]` | `b2eab630`・`6bf35568`・`44d3b176` |
| `request` の Primary・Age・Payload | `mkfixtures.py` の `GOOD["request"]` | `29a35928`・`3b1f0b9a`・`a4ce18ab` |

### `msgapp.hex` と `request.hex` の要点

- `msgapp`: 連番 999,999,999 は `1a 3b 9a c9 ff`。Age 0 の data は `41 00`。ペイロード 35 バイトは `0803 1001 1803 2002 2802 3004`（type 3・to 1・from 3・term 2・logTerm 2・index 4）、エントリ `3a 09 [1002 1805 2203 616263]`・`3a 08 [1002 1806 2202 6465]`（Term 2・Index 5/6・Data `abc`/`de`）、`4004`（commit 4）
- `request`: 宛先 ipn:3.2・送信元 ipn:2.2・連番 7。Age 2^32 + 1 の data は `49 1b 00 00 00 01 00 00 00 01`。ペイロードは CBOR の map `a3 01 63 722d31 02 01 03 58 5d <93 バイトの JSON>`（`{1: "r-1", 2: 1, 3: h'…'}`）

## 断るべき Bundle（`errors/`）

`heartbeat.hex` の 3 つのブロックを部品にして、1 か所だけを変えて作る。CRC は、変えた後のブロックに対して計算し直す（`bpv7_crc` のものだけは、中身の 1 ビットを変えて CRC をそのままにする）。検査の順は bpv7-profile.md §6 のとおりで、各 fixture はそれより前の検査には当たらない。
