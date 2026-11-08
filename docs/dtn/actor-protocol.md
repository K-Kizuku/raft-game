# Actor の電文（内部 API）

DTN-02（#159）の契約。Coordinator・Session Front・Actor Host・Actor（Container）の間で使う。ブラウザからは届かない経路にだけ置く。

## 1. 経路と符号化

「RPC CLA」の RPC は「要求と応答の呼び出し」という意味で、Cloudflare の Durable Object の RPC の機能は使わない（ADR-0023 のとおり `fetch` ハンドラで呼ぶ）。


| 区間 | Cloudflare | ローカル | 符号化 |
|---|---|---|---|
| Coordinator ⇔ Session Front | DtnSession DO → Coordinator Container の `/ws` | `dtnfabric` → `dtnactor` の `/ws` | JSON テキストフレーム。1 フレーム = 1 つの JSON。`{"ch": "ctl" \| "browser", "msg": …}` で制御（§3.3）とブラウザの電文を分ける |
| Session Front ⇔ Actor Host | Durable Object の `fetch` ハンドラ（[ADR-0023](../adr/0023-durable-object-fetch-handler.md)） | Go の関数呼び出し | 同じ JSON の構造 |
| Actor Host ⇔ Actor | ActorHost DO → Actor Container の `/ws` | Go の Host → `dtnactor` の `/ws` | JSON テキストフレーム |
| Actor Host ⇔ Actor Host（RPC CLA） | Durable Object の `fetch` ハンドラ | Go の関数呼び出し | JSON（Bundle は base64） |

- Bundle の bytes は JSON の中で base64（RFC 4648 §4、パディングあり）
- 1 つの電文の上限は 1 MiB（Workers の WebSocket の 1 メッセージの上限に合わせる）。超えたら `too_large`
- digest と署名に使う「正準の JSON」は RFC 8785（JSON Canonicalization Scheme）。digest は `sha256:` ＋ 16 進の小文字
  - この電文の数は整数だけ。小数点・指数を含む数（`1.0`・`1e3` も）と、絶対値が 2^53 − 1 を超える整数は `invalid`（`-0` は `0`）。重複したキー・不正な UTF-8・対にならないサロゲートも `invalid`（[§7](#7-実装の細部dtn-02)）

- `dtnactor` は起動したら、開いたポートを標準出力の 1 行目に `{"listen":"127.0.0.1:NNNNN"}` の形で出す（ポートは `:0` で開く）

## 2. エンベロープ

### 2.1 要求

```json
{
  "v": 1,
  "sessionId": "s_…",
  "actor": "island:2",
  "epoch": 3,
  "coordEpoch": 1,
  "eventSeq": 1207,
  "kind": "advance",
  "payload": { "horizon": [12500, 1] }
}
```

| フィールド | 意味 |
|---|---|
| `v` | 電文の版（schemaVersion）。今は 1。違えば `unsupported_version` |
| `sessionId` | 署名済みのセッション ID。Host が持つ値と違えば `session_mismatch` |
| `actor` | `coordinator`・`island:<raftNodeId>`（例: `island:2`）・`ferry:<ferryId>`（ステージ定義の `ferries[].id` の文字列。例: `ferry:f1`） |
| `epoch` | Actor の世代。Actor Host が Container を起動するたびに 1 増やし、永続化する。Host → Actor の要求と Actor → Host の応答の両方に付ける。古ければ `stale_epoch` |
| `coordEpoch` | Coordinator の世代。Session Front が Coordinator の Container を起動するたびに 1 増やす。Host は、受けた最大の値より古い要求を `stale_coord_epoch` で断る |
| `eventSeq` | Coordinator が振る、ゲーム全体で単調に増える番号。Coordinator の journal の番号でもある |
| `kind` | §3 |
| `payload` | `kind` ごとの中身 |
| `replay` | 省略可。`true` は復元の再生（[persistence-and-recovery.md §4.1](./persistence-and-recovery.md#41-actor-が止まったとき止まった-actor-だけを復元する)）。Actor は同じ処理をし、Host が応答の digest を照合する |

### 2.2 応答

```json
{
  "v": 1,
  "actor": "island:2",
  "epoch": 3,
  "eventSeq": 1207,
  "status": "ok",
  "outputs": [
    { "t": 12000, "class": 3, "seq": 88, "type": "node.state", "data": { } }
  ],
  "cursor": { "horizon": [12500, 1], "stateDigest": "sha256:…" },
  "error": null
}
```

- `outputs` は §4。`(t, class, seq)` は島の中の順序（[simulation-time.md §3](./simulation-time.md#3-島の出力をまとめる順序)）
- `stateDigest` は Actor の状態の要約（DTN-03・04 で中身を決める。台帳・港・Cargo の ID の列・Raft の HardState を正準に並べた SHA-256）。復元の後の照合に使う
- Actor が作った Bundle の bytes は、Actor → Host の応答にだけ入れる（`outputs[].bundle`）。Host は bytes を保存し、Session Front と Coordinator への応答からは取り除く（digest と長さだけ残す）

## 3. 種類

### 3.1 Actor（Container）が受ける

| kind | 相手 | payload | 処理 |
|---|---|---|---|
| `init` | すべて | `{stage, stageHash, seed, actor, plan?}` | Actor を作る。島は開幕式を手元で再計算する。`plan` は、その島が発生元の依頼の計画（Coordinator が渡す） |
| `advance` | 島 | `{horizon: [t, c]}` | [simulation-time.md §2.1](./simulation-time.md#21-上限horizon) |
| `deliver` | 島 | `{t, letters: [手紙]}` | 到着した手紙を、この順に処理する（Raft の手紙は `Deliver` → `Process` → 返事を港へ、依頼の手紙は窓口へ）。最後に `Process` する |
| `port.remove` | 島 | `{letterIds}` | 船に積まれた（所有権が移った）手紙を港から除く |
| `unload` | 島 | `{t, letters: [手紙 ＋ `place`]}` | 下ろされた手紙を、この順（手紙の ID の昇順。今の `Unload` と同じ）に処理する。`place: "port"`（自島の手紙）は港に戻して規則 R1〜R6 をかけ直し、`place: "relay"`（他島の手紙）は倉庫に入れる |
| `relay.remove` | 島 | `{letterIds}` | 倉庫から船に積まれた手紙を除く |
| `arrive` | 船 | `{t, island}` | `Docked` にし、`ReadyAt = t + dockMs` |
| `depart` | 船 | `{t, to, arriveAt}` | `Sailing` にする |
| `cargo.add` | 船 | `{t, letters: [手紙]}` | Cargo の末尾に、この順に加える |
| `cargo.remove` | 船 | `{letterIds}` | Cargo から除く |
| `bundle.export` | 島・船 | `{t, letterIds}` | 送り出す Bundle の Bundle Age と CRC を更新した bytes を、出力 `bundle.exported{letterId, digest, size}`（Host への応答には `bundle` も入る）で返す。状態は変えない・journal に入れない（[bpv7-profile.md §3](./bpv7-profile.md#3-時刻)） |
| `inspect` | すべて | `{}` | 状態の要約を返す（状態は変えない。journal に入れない） |
| `shutdown` | すべて | `{}` | 処理を止めて終わる |

- 「手紙」は `{letterId, createdAt, digest, bundle}`（`createdAt` は手紙を作った仮想時刻。Bundle Age の計算に使う）
- `deliver`・`unload`・`cargo.add` の bundle は、Handoff で所有権が確定したものだけ（[handoff-and-contact.md](./handoff-and-contact.md)）。Actor は、受けた bundle の digest を確かめる
- `init` の応答には、開幕式の後の最初の出力（`node.state` など、t = 0）を入れる。Coordinator はこれを `cmd.game.start` のときに適用する（今の開幕式の役割の変化の再生と同じ）
- 出力の `class`: `deliver`（到着の配達）は 1（`ClassFerryArrive`。同じ時刻の `RaftTick` より前）。プレイヤーのコマンドが起こした出力（`unload` など）は 8（[simulation-time.md §2.1](./simulation-time.md#21-上限horizon) のコマンドの位置）。コードの `ClassCommandApplied = 0` は使われていないので使わない
- Actor の状態を変える要求は、Actor ごとに直列に処理する。並行した要求は届いた順に待たせる

### 3.2 Actor Host が受ける（Container には送らない）

| kind | 送る側 | payload | 処理 |
|---|---|---|---|
| `contact.open`・`contact.close` | Session Front → 島と船の Host | `{contact}` | Contact を記録する・閉じた ID を保存する（[handoff-and-contact.md §2](./handoff-and-contact.md#2-contact寄港)） |
| `transfer.prepare` | Session Front → 送り元の Host | `{transferId, t, to, contact, letterIds}`（`t` は送り出す仮想時刻。Bundle Age の計算に使う） | [handoff-and-contact.md §3](./handoff-and-contact.md#3-handoff所有権の引き渡し) |
| `transfer.accept` | 送り元の Host → 受け取る Host（CLA） | `{transferId, from, contact, bundles: [{letterId, bundle, digest}]}` | 同上 |
| `transfer.activate` | Session Front → 受け取る Host | `{transferId, then: {kind, payload}}` | 同上。`then` は Container に送る要求（`deliver`・`cargo.add` など） |
| `transfer.gc` | Session Front → 送り元の Host | `{transferId, then: {kind, payload}}` | 同上。`then` は `port.remove`・`cargo.remove` など |
| `transfer.abort` | Session Front → 両方の Host | `{transferId}` | 同上 |
| `transfer.status` | Session Front → どちらかの Host | `{transferId}` | 状態を返す（状態は変えない） |
| `host.status` | Session Front | `{}` | epoch・最後の `eventSeq`・Container の状態を返す |

### 3.3 Coordinator が受ける

Session Front → Coordinator の電文。Coordinator → Session Front は `dispatch{calls: [{actor, envelope}]}`（並行に送ってよい要求の組）と、ブラウザ向けのイベント。

| kind | payload | 処理 |
|---|---|---|
| `init` | `{stage, stageHash, seed, sessionId}` | ゲームを作る。開幕式を手元で計算する |
| `dispatch.result` | `{results: [応答…]}` | `dispatch` の応答 |
| `command` | `{cmdId, type, payload}` | ブラウザのコマンド（`cmd.*`・`demo.trigger`）。Session Front が journal に記録できるよう、コマンドは必ず `ch: "ctl"` の `command` で渡す。`ch: "browser"` は、Coordinator → ブラウザのイベントと、コマンドでないブラウザの電文（`hello`・`ping`・`cmd.sync`・`sub.engineer`）だけに使う |
| `restore.begin`・`restore.chunk`・`restore.end` | `{stage, stageHash, seed, entries}`・`{index, entries}`・`{}` | journal を分けて受け取り、記録した応答で再計算する（[persistence-and-recovery.md §4.2](./persistence-and-recovery.md#42-coordinator-が止まったとき)） |

## 4. 出力（Actor → Coordinator）

`data` は、**今のドメインイベントの中身（同じ型・同じフィールド）** に、下の表の追加のフィールドを足したもの。ブラウザ向けのイベントと正規化トレースを、Coordinator が島の出力から作れるようにするため（Raft のメッセージの中身の表示用のフィールド `logTerm`・`index`・`commit`・`entries`・`reject` なども、今の `letter.created` と同じく入る）。

| type | 出す Actor | data（今のドメインイベントの中身に足すもの） |
|---|---|---|
| `letter.created` | 島 | `digest`・`size`・`msgTerm`（Raft のメッセージ自身の Term `m.GetTerm()`。古い手紙の検出に使う。今の中身の `term` は表示用の送信元の Term で、PreVote などで違うことがある）（Host への応答には `bundle` も入る） |
| `letter.dropped` | 島 | `rule`（`R1`〜`R6`） |
| `letter.delivered` | 島 | なし |
| `bundle.exported` | 島・船 | `{letterId, digest, size}`（Host への応答には `bundle` も入る） |
| `node.state`・`node.leader_elected`・`node.stepped_down` | 島 | 今のドメインイベントと同じ中身 |
| `term.changed` | 島 | `{term}`（古い手紙の検出に使う） |
| `ledger.applied` | 島 | なし（今の中身 `islandId`・`index`・`reqId`・`result`・`reason` のまま） |
| `request.routed` | 島 | `{reqId, action, to?}`（`action`: `proposed`・`forwarded`・`waiting`・`dropped`） |
| `ferry.state` | 船 | `{state, at?, to?, arriveAt?, readyAt, cargo: [letterId…]}`。`t`・`class` は起こした要求のもの（`arrive` は 1、`depart`・`cargo.*` は 8） |

Coordinator は、これらを CoordinatorCore に渡し、ブラウザ向けのドメインイベント（今と同じ型）を作る。

## 5. 冪等と順序

- 要求の digest は、エンベロープから `epoch`・`coordEpoch`・`replay` を除いたもの（`v`・`sessionId`・`actor`・`eventSeq`・`kind`・`payload`）の RFC 8785 の正準の JSON の SHA-256。世代が変わった後の送り直しを `conflict` にしないため
- Host は、処理した `eventSeq` ごとに、要求の digest と応答を保存する（[persistence-and-recovery.md](./persistence-and-recovery.md)）
  - 同じ `eventSeq` で同じ digest → 保存した応答をそのまま返す（Container に送り直さない）
  - 同じ `eventSeq` で違う digest → `conflict`
  - 保存した最後の `eventSeq` より小さく、保存がない → `stale_event`
- 処理の後に応答が失われたら、Session Front は**同じ `eventSeq`** で送り直す。別の番号で送り直さない
- 応答の保存はセッションが終わるまで消さない（上限は §6）

## 6. エラーと上限

| code | 意味 | Coordinator の扱い |
|---|---|---|
| `invalid` | 形が不正・上限が減った・未知の手紙の ID | バグ。ゲームを `dtn_internal` で終える |
| `unsupported_version` | `v` が違う | 同上 |
| `session_mismatch`・`unknown_actor` | 宛先の取り違え・偽造 | 同上（ログに残す） |
| `stale_epoch`・`stale_coord_epoch` | 古い世代 | 古い側の応答を捨てる。新しい世代で状態を照会してやり直す |
| `stale_event` | 古い `eventSeq` | 捨てる |
| `conflict` | 同じ番号で中身が違う | バグ。`dtn_internal` で終える |
| `too_large` | 上限を超えた | 同上 |
| `not_ready` | Container の起動・復元の途中 | 仮想時間を止めて待つ（[simulation-time.md §2.3](./simulation-time.md#23-仮想時間を止める条件)） |
| `contact_closed`・`contact_mismatch` | Contact の外の転送 | Handoff を取り消す（[handoff-and-contact.md](./handoff-and-contact.md)） |
| `restore_failed` | 再生の応答の digest が記録と一致しない | ゲームを `dtn_restore_failed` で終える |
| `internal` | Actor の中の失敗 | Container を作り直して復元する |

| 上限（初期値。`docs/dtn/` と設定に置く） | 値 |
|---|---|
| 1 つの電文 | 1 MiB |
| 1 つの Bundle | 16 KiB（このゲームの Raft のメッセージは小さい。16 通 × 16 KiB を base64 にしても 1 つの電文に収まる） |
| 1 回の `deliver`・`cargo.add` の手紙 | 船の積載の上限まで（最大 16） |
| 1 回の応答の `outputs` | 4,096 |
| 1 Actor の journal の行 | 20,000（上限の更新は上書きするので、ふつうは数千） |
| 1 Actor の保存の合計 | 8 MiB |

## 7. 実装の細部（DTN-02）

実装は `server/internal/dtn/protocol`（import の別名は `dtnproto`）・`server/internal/dtn/runtime`・`server/cmd/dtnactor`。

### 7.1 検証（`dtnproto.Validate`）

- 確かめる順: 長さ（`too_large`）→ JSON の読み込み（`invalid`）→ `v`（整数でなければ `invalid`、1 でなければ `unsupported_version`）→ エンベロープの形・`actor`・`kind`・宛先の役割 → payload
- フィールドの名前は大文字と小文字を区別する。未知のフィールド・足りないフィールド・`null`（省略可のフィールドを除く）は `invalid`。Bundle の base64 は RFC 4648 §4 のパディングありの形だけ（改行などを含む別の書き方は `invalid`）
- journal に入れる種類は `eventSeq` が 1 以上。入れない種類（`inspect` など）は 0 でもよい
- 手紙の列（`letters`・`letterIds`・`bundles`）は 1 通以上 16 通以下（空は `invalid`、17 通以上は `too_large`）で、ID の重複は `invalid`。`unload` の手紙は ID の狭義の昇順。手紙の `createdAt` は `t` 以下
- `init` の `payload.actor` はエンベロープの `actor` と同じ。`plan` は島だけで、`origin` はその島
- §3.1・§3.2 の「すべて」は島と船。Coordinator が受ける種類（§3.3。Coordinator への `init`・`inspect`・`shutdown` を含む）は DTN-05 が種類の表（`kinds.go`）に足す。それまで `coordinator` 宛ての要求は `invalid`
- §3.2 の種類（Host だけが受ける）が Container に届いたら `invalid`。`transfer.activate`・`transfer.gc` の `then` は、宛先の役割に合う journal に入れる種類（`init`・`shutdown` を除く）で、その payload も同じ規則で確かめる

### 7.2 Runtime（Container の中）

- 応答の `epoch` は、`init` の後は Runtime の epoch（要求の epoch が違っても）。`init` の前は要求の epoch
- Actor を呼んだ後の誤りの応答（Actor の誤り・`internal`・outputs が 4,096 を超えた・応答が 1 MiB を超えた `too_large`）も、成功と同じく最後の 1 件として持ち、同じ `eventSeq` の送り直しには同じ bytes を返す。Actor を呼ぶ前に断ったもの（`stale_*`・`conflict` など）は持たない
- `init` が失敗したら、Runtime は状態を決めない（Host は Container を作り直す）
- `inspect` の応答は、`outputs` に `{"type": "inspect", "data": <Actor の要約>}` を 1 つ入れる（`init` の前は `data` が `{"ready":false}` で `cursor` は `null`）。`inspect`・`bundle.export` は `eventSeq` を確かめず、進めない
- `shutdown` の後は、同じ `shutdown` の送り直し（保存した応答）を除いて `not_ready`。`/ws` は `shutdown` の応答を書いてから閉じ、`dtnactor` は終了コード 0 で終わる
- 応答の digest（`dtnproto.ResponseDigest`。復元の照合に使う）は、`{v, actor, eventSeq, status, outputs, cursor, error}` の正準の JSON の SHA-256。`epoch`（世代が変わった後の再生でも同じにするため）と `outputs[].bundle`（Host が保存する応答には bytes を入れない。bundle の digest は `data` に残る）を含めない

### 7.3 `dtnactor`

- `PORT` があれば全てのアドレスのそのポートで待ち受け、1 行目にはその実際のアドレス（例 `{"listen":"[::]:8080"}`）を出す
- 終了コード: 0 = シグナルか `shutdown` で止めた、1 = 起動の失敗、2 = 未知の `ROLE`・まだ実装のない `ROLE`・`PORT` の誤り

### 7.4 fixture（TS 側の契約テストも読む）

- `server/internal/dtn/protocol/testdata/requests/*.json`: `{description, request | requestText, limits?, expect, digest?}`。`request` は電文（JSON の値。空白を除いて送る）、`requestText` は bytes をそのまま書いた電文（重複したキー・小数など）。`limits` は §6 の上限を部分的に上書きする（`maxMessageBytes` など）。`expect` は `"ok"` かエラーコード、`digest` は `ok` のときの要求の digest（§5）
- `server/internal/dtn/protocol/testdata/canonical/*.json`: `{description, input, canonical, digest}` か `{description, input, error}`。`input` は JSON の文書の文字列
