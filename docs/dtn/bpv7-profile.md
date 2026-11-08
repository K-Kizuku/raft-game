# BPv7 のプロファイル（このゲームで使う範囲）

DTN-06（#163）の契約。判断は [ADR-D003](./adr/D003-bpv7-profile.md)。参照: [RFC 9171](https://www.rfc-editor.org/rfc/rfc9171.html)。

> このプロファイルは RFC 9171 の**一部**だけを実装する。「BPv7 に準拠」とは書かず、「RFC 9171 の単一宛先・非フラグメントのプロファイル」と書く。

## 1. 範囲

| 機能 | 扱い |
|---|---|
| Primary Block | 対応する（版 7） |
| Payload Block（type 1） | 対応する |
| Bundle Age Block（type 7） | 対応する（作成時刻が 0 なので必須） |
| Previous Node・Hop Count・その他の拡張ブロック | 作らない。受けたら断る |
| Fragmentation | 作らない。断片の Bundle を受けたら断る |
| 管理レコード（ステータスレポートなど） | 作らない。受けたら断る |
| custody transfer・BPSec | 使わない。所有権の引き渡しは独自の Handoff（[handoff-and-contact.md](./handoff-and-contact.md)） |
| Lifetime による破棄 | 起きない値にする（§3） |

## 2. EID と識別

| 対象 | EID |
|---|---|
| 島の Raft の窓口 | `ipn:<raftNodeId>.1` |
| 島の依頼の窓口 | `ipn:<raftNodeId>.2` |
| 船（中継のノード） | `ipn:<1000 + ferryId>.0`（Bundle の宛先にはならない） |
| Report-to | `dtn:none` |

- 送信元は、手紙を作った島の EID（Raft なら `.1`、依頼なら `.2`）
- 作成時刻は `[0, n]`。DTN time は 0（このゲームには同期した時計がない）、連番 `n` は `letterId` の島の中の通し番号
- だから `bundleId`（送信元のノード番号と作成時刻）と `letterId`（`islandId × 10^9 + n`）は互いに計算できる。`transferId` は Bundle と関係のない別の ID

## 3. 時刻

| 欄 | 値 |
|---|---|
| 作成時刻の DTN time | 0 |
| Lifetime（ms） | ステージの `durationMs` ＋ 60,000（ラウンドの中で期限切れにならない） |
| Bundle Age（ms） | 仮想時間で、作成から最後に送り出したときまでの経過。`送り出した t − letter.CreatedAt` |

- 仮想時間（SimTime）を DTN time の欄にそのまま入れない
- Bundle Age は、Bundle を送り出す Actor が更新する。Actor Host は `transfer.prepare` を受けたら、自分の Container に `bundle.export{letterIds, t}` を送り、Bundle Age と CRC を更新した bytes を受け取って、それを prepared の bytes として保存する（BPv7 の符号化を Go だけに置くため。TS の Host は bytes を解釈しない）
- 期限切れ・Hop Count による破棄はしない。ゲームのルール（港の規則 R1〜R6）以外で Raft のメッセージを落とさない

## 4. ペイロード

| サービス | ペイロード（ADU） |
|---|---|
| `.1`（Raft） | `raftpb.Message` の protobuf（`proto.MarshalOptions{Deterministic: true}`）。中身は書き換えない |
| `.2`（依頼） | CBOR の map `{1: reqId（text）, 2: hops（uint）, 3: command（bytes。今の `ledger.Command` の JSON）}` |

依頼の手紙に台帳のコマンドを入れて運ぶので、転送先の島は依頼の計画を持たなくても Propose できる。

## 5. 符号化

- Bundle 全体は、RFC 9171 のとおり**長さ不定の CBOR の配列**（`0x9f` … `0xff`）。その中の各ブロックは長さ確定の配列
- ブロックの並び: Primary → Bundle Age（ブロック番号 2）→ Payload（ブロック番号 1、最後）
- Bundle の処理の制御フラグ: `0x0004`（断片にしてはならない）だけ
- ブロックの処理の制御フラグ: 0
- CRC: すべてのブロックで CRC-32C（CRC の種類 2）。CRC の欄を 4 バイトの 0 にしたブロックの CBOR に対して計算する
- CBOR のライブラリは `fxamacker/cbor/v2`（MIT）。長さ不定の外側の配列だけ自前で書く。符号化は最短の整数・長さ確定（Core Deterministic Encoding）

## 6. 読み込みの検査（不正な Bundle は断る）

| 検査 | エラー |
|---|---|
| CBOR が壊れている・余分なバイトがある | `bpv7_malformed` |
| 版が 7 でない | `bpv7_version` |
| フラグが `0x0004` でない（断片・管理レコードを含む） | `bpv7_unsupported_flags` |
| CRC の種類が 2 でない・CRC が合わない | `bpv7_crc` |
| EID が §2 の形でない・送信元が島でない | `bpv7_eid` |
| 作成時刻の DTN time が 0 でない・連番が 0 | `bpv7_timestamp` |
| Bundle Age がない・2 つ以上ある | `bpv7_age` |
| Bundle Age・Payload 以外のブロックがある（フラグに関係なく） | `bpv7_unsupported_block` |
| Payload が最後でない・ブロック番号が重複 | `bpv7_structure` |
| 64 KiB を超える | `bpv7_too_large` |

RFC 9171 では、処理できないブロックの扱いをフラグで決めてよい。このプロファイルは閉じた系なので、より厳しく「すべて断る」にする（黙って読み飛ばさない）。

## 7. 試験

- 手で検算した固定の Bundle（16 進）を `server/internal/dtn/bpv7/testdata/` に置き、符号化と読み込みの両方で一致を確かめる
- 可能なら独立した実装（例: µD3TN の Python のツール。ライセンスを確かめて、リンクはせず fixture を作るのにだけ使う）で作った Bundle を fixture にする
- Raft のメッセージの protobuf の往復・依頼のペイロードの往復・時刻の境界・§6 の各エラー・`go test -fuzz` で読み込みの fuzz を回す
