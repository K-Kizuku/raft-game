# Actor の電文（内部 API）

DTN-02（#159）の契約。Coordinator・Session Front・Actor Host・Actor（Container）の間で使う。ブラウザからは届かない経路にだけ置く。

## 1. 経路と符号化

| 区間 | Cloudflare | ローカル | 符号化 |
|---|---|---|---|
| Coordinator ⇔ Session Front | DtnSession DO → Coordinator Container の `/ws` | `dtnfabric` → `dtnactor` の `/ws` | JSON テキストフレーム |
| Session Front ⇔ Actor Host | DO RPC（`ActorHost` のメソッド） | Go の関数呼び出し | 同じ JSON の構造 |
| Actor Host ⇔ Actor | ActorHost DO → Actor Container の `/ws` | Go の Host → `dtnactor` の `/ws` | JSON テキストフレーム |
| Actor Host ⇔ Actor Host（RPC CLA） | DO RPC | Go の関数呼び出し | JSON（Bundle は base64） |

- Bundle の bytes は JSON の中で base64（RFC 4648 §4、パディングあり）
- 1 つの電文の上限は 1 MiB（Workers の WebSocket の 1 メッセージの上限に合わせる）。超えたら `too_large`
- digest と署名に使う「正準の JSON」は RFC 8785（JSON Canonicalization Scheme）。digest は `sha256:` ＋ 16 進の小文字

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
| `deliver` | 島 | `{t, letters: [{letterId, bundle}]}` | 到着した手紙を、この順に処理する（Raft の手紙は `Deliver` → `Process` → 返事を港へ、依頼の手紙は窓口へ）。最後に `Process` する |
| `port.remove` | 島 | `{letterIds}` | 船に積まれた（所有権が移った）手紙を港から除く |
| `port.return` | 島 | `{t, letters: [{letterId, bundle}]}` | 下ろされた自島の手紙を港に戻し、規則 R1〜R6 をかけ直す |
| `relay.store` | 島 | `{letters: [{letterId, bundle}]}` | 下ろされた他島の手紙を倉庫に入れる |
| `relay.remove` | 島 | `{letterIds}` | 倉庫から船に積まれた手紙を除く |
| `arrive` | 船 | `{t, island}` | `Docked` にし、`ReadyAt = t + dockMs` |
| `depart` | 船 | `{t, to, arriveAt}` | `Sailing` にする |
| `cargo.add` | 船 | `{letters: [{letterId, bundle}]}` | Cargo の末尾に、この順に加える |
| `cargo.remove` | 船 | `{letterIds}` | Cargo から除く |
| `bundle.export` | 島・船 | `{t, letterIds}` | 送り出す Bundle の Bundle Age と CRC を更新した bytes を返す（状態は変えない。[bpv7-profile.md §3](./bpv7-profile.md#3-時刻)） |
| `inspect` | すべて | `{}` | 状態の要約を返す（状態は変えない。journal に入れない） |
| `shutdown` | すべて | `{}` | 処理を止めて終わる |

- `deliver`・`port.return`・`relay.store`・`cargo.add` の bundle は、Handoff で所有権が確定したものだけ（[handoff-and-contact.md](./handoff-and-contact.md)）。Actor は、受けた bundle の digest を確かめる
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
| `command` | `{cmdId, type, payload}` | ブラウザのコマンド（今の外部の電文） |
| `restore.begin`・`restore.chunk`・`restore.end` | `{stage, stageHash, seed, entries}`・`{index, entries}`・`{}` | journal を分けて受け取り、記録した応答で再計算する（[persistence-and-recovery.md §4.2](./persistence-and-recovery.md#42-coordinator-が止まったとき)） |

## 4. 出力（Actor → Coordinator）

| type | 出す Actor | data |
|---|---|---|
| `letter.created` | 島 | `{letterId, kind, from, to, msgType, term, reqId?, digest, size}`（Host への応答には `bundle` も入る） |
| `letter.dropped` | 島 | `{letterId, rule}`（`R1`〜`R6`） |
| `letter.delivered` | 島 | `{letterId}` |
| `node.state`・`node.leader_elected`・`node.stepped_down` | 島 | 今のドメインイベントと同じ中身 |
| `term.changed` | 島 | `{term}`（古い手紙の検出に使う） |
| `ledger.applied` | 島 | `{reqId, outcome, index}`（`outcome`: `ok`・`rejected`・`duplicate`） |
| `request.routed` | 島 | `{reqId, action, to?}`（`action`: `proposed`・`forwarded`・`waiting`・`dropped`） |
| `ferry.state` | 船 | `{state, at?, to?, arriveAt?, readyAt, cargo: [letterId…]}` |

Coordinator は、これらを CoordinatorCore に渡し、ブラウザ向けのドメインイベント（今と同じ型）を作る。

## 5. 冪等と順序

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
