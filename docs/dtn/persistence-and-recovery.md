# Actor Host の保存・確定の順序・復元・fencing

DTN-07（#164）・DTN-10（#167）の契約。

## 1. 原則

- **正本は Actor Host**。Cloudflare では Actor ごとの Durable Object `ActorHost` の SQLite、ローカルでは Go の SQLite adapter（Actor ごとに別のファイル）。Container のメモリとディスクは正本にしない
- **Actor は決定的な関数**。同じ `init` と同じ要求の列を与えれば、同じ状態と出力になる。だから journal（要求の列）を保存しておけば、Container を作り直して再生できる
- **Coordinator の台帳（所有権・journal）と Actor の保存は分ける**。Durable Object の間にまたがる 1 つのトランザクションがあるとは仮定しない。境界は Handoff の状態機械（[handoff-and-contact.md](./handoff-and-contact.md)）で合わせる
- ローカルの SQLite のライブラリは `modernc.org/sqlite`（純 Go・BSD-3-Clause。CGO を使わない）。採用の記録は DTN-07 の PR で `NOTICE` に足す

## 2. Actor Host の保存の形

```sql
-- 1 Actor の 1 つのデータベース（Durable Object なら ctx.storage.sql、ローカルならファイル）
CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL);
-- schemaVersion, sessionId, actor, epoch, maxCoordEpoch, initPayload, createdAt

CREATE TABLE journal (
  event_seq   INTEGER PRIMARY KEY,
  kind        TEXT    NOT NULL,
  req_digest  TEXT    NOT NULL,          -- 要求の正準の JSON の SHA-256
  payload     TEXT    NOT NULL,          -- 要求の JSON。bundle の bytes も送ったとおりに含める（再生はこれだけを使う）
  status      TEXT    NOT NULL,          -- 'intent' | 'applied'
  response    TEXT,                      -- 応答の JSON（bundle の bytes は除く）
  resp_digest TEXT,
  epoch       INTEGER NOT NULL           -- 適用した世代
);

CREATE TABLE bundles (
  letter_id   INTEGER PRIMARY KEY,
  bundle      BLOB,                      -- 今の版の bytes（送り出すたびに Bundle Age が変わるので上書きする）。collected の後は NULL にしてよい
  digest      TEXT    NOT NULL,
  size        INTEGER NOT NULL,
  state       TEXT    NOT NULL,          -- 'held' | 'prepared' | 'pending' | 'gone'
  place       TEXT    NOT NULL,          -- 'port' | 'relay' | 'cargo' | 'delivered' | 'dropped' | 'moved'
  transfer_id TEXT,
  updated_seq INTEGER NOT NULL
);

CREATE TABLE transfers (
  transfer_id TEXT PRIMARY KEY,
  role        TEXT NOT NULL,             -- 'source' | 'dest'
  peer        TEXT NOT NULL,
  contact_id  TEXT NOT NULL,
  letter_ids  TEXT NOT NULL,             -- JSON の配列
  digests     TEXT NOT NULL,
  state       TEXT NOT NULL,             -- 'prepared' | 'pending' | 'activated' | 'collected' | 'aborted'
  ack         TEXT
);

CREATE TABLE contacts (contact_id TEXT PRIMARY KEY, body TEXT NOT NULL, state TEXT NOT NULL);
```

- `bundles` は「今その Actor が持っている bytes」の表。同じ手紙が同じ Actor に戻ってくる（積む → 倉庫に下ろす → また積む）と、Bundle Age の違う bytes で上書きされる。**再生は `bundles` を使わず、`journal.payload` に入れた bytes だけを使う**。だから `bundles` の bytes は collected の後に消してよく、`journal` はセッションが終わるまで消さない
- `advance` は、直前の journal の行も `advance` なら、2 つを 1 つの行にまとめる（島の進み方は上限の区切り方に依存しない。D-9）
  - まとめた行は、新しい `event_seq`・新しい上限・**2 つの応答の `outputs` をつなげたもの**を持ち、`resp_digest` はつなげた `outputs` で計算し直す。再生では、まとめた上限への 1 回の `advance` が、つなげた `outputs` と同じ出力を出す（D-9）ので、digest が一致する
  - まとめて消えた古い `eventSeq` が後から届いたら `stale_event`（上限は単調なので、古い上限は意味を持たない）
- `inspect`・`bundle.export`・`transfer.status`・`host.status` は journal に入れない（状態を変えない）。`bundle.export` の bytes は、送り元の Host が `transfers` と `bundles` に prepared の版として保存する

## 3. 確定の順序

### 3.1 Container に送る要求（`init`・`advance`・`deliver` など）

1. エンベロープを確かめる（版・セッション・epoch・`coordEpoch`・`eventSeq`）
2. 冪等の確認（[actor-protocol.md §5](./actor-protocol.md#5-冪等と順序)）
3. `journal` に `intent` で書く
4. Container に送り、応答を待つ（実時間の上限 10 秒）
5. 1 つのトランザクションで: 応答の `letter.created` の bundle を `bundles` に保存・`then` の効果（`deliver` なら `place = 'delivered'` など）・`journal` を `applied` にして応答を保存
6. 応答を返す（bundle の bytes を除く）

4 と 5 の間で Container が止まっても、`intent` の行が残る。復元（§4）で、その行も再生して応答を作る。**Host の確定の前に、応答を相手に返さない**。

### 3.2 Host だけで処理する要求（`transfer.*`・`contact.*`）

[handoff-and-contact.md §3](./handoff-and-contact.md#3-handoff所有権の引き渡し) の各段階を、1 つのトランザクションで保存してから応答する。`activate`・`gc` の `then` は、§3.1 の手順で Container に送る。

## 4. 復元

### 4.1 Actor が止まったとき（止まった Actor だけを復元する）

1. Host が止まったことに気づく（Container の WS が閉じた・ping に 3 回答えない（5 秒ごと）・応答が上限を超えた）
2. Host は、新しい要求に `not_ready` を返し始める。Coordinator は仮想時間を止める（[simulation-time.md §2.3](./simulation-time.md#23-仮想時間を止める条件)）
3. 古い Container を止める（`destroy()`）。`epoch` を 1 増やして保存する
4. 新しい Container を起動し、`meta.initPayload` で `init` を送る
5. `journal` を `event_seq` の順に再生する（bundle の bytes は `journal.payload` のものを使う）。`applied` の行は `replay: true` を付けて送り、応答の digest が保存したものと一致することを確かめる（Actor の作る bundle の digest も一致すること）。一致しなければ `restore_failed` で、ゲームを `dtn_restore_failed` で終える
6. 最後の `intent` の行（あれば）は、ふつうに処理して §3.1 の 5 から続ける
7. `not_ready` をやめる

- 他の Actor は止めない。止まっている間に届いた要求は、Coordinator が復元の後に同じ `eventSeq` で送り直す
- 再生は、Container の中の計算だけなので速い（島の journal は数千行。上限の更新は上書きされるので行が増えない）
- 初期はスナップショットを作らない。`MemoryStorage` と HardState だけでは、`RawNode` の選挙のタイマーなどまで戻せないため。スナップショットによる高速化は後続の候補

### 4.2 Coordinator が止まったとき

- Session Front の SQLite に、Coordinator の journal を置く:
  - 適用したプレイヤーのコマンド（`cmdId`・`t`・中身・結果）
  - `dispatch` ごとの `eventSeq`・宛先・要求の digest・応答
  - Handoff の台帳の段階・Contact・`Now`
- Session Front は `coordEpoch` を 1 増やし、新しい Coordinator の Container に journal を分けて送る（`restore.begin{stage, stageHash, seed, entries}` → `restore.chunk{index, entries}` を何回か → `restore.end{}`。1 つの電文の上限に収まるように分ける。[actor-protocol.md §3.3](./actor-protocol.md#33-coordinator-が受ける)）
- Coordinator は、記録した応答を使って（Actor を呼ばずに）journal の最後まで再計算する。決定的なので同じ状態になる
- 応答のない最後の `dispatch` は、同じ `eventSeq` で送り直す（Host の冪等で安全）。`proposed`・`persisted` の Handoff は [handoff-and-contact.md §3.3](./handoff-and-contact.md#33-止まったときの回復) のとおりに収束させる
- 一時停止の状態で待ち、ブラウザに `snapshot` を送る（今の復元と同じ）

### 4.3 Session Front・Actor Host が止まったとき

- Durable Object が作り直されても、SQLite は残る。メモリの状態（WS・待っている要求）は失われるので、Session Front は Actor Host に `host.status` を聞き、Coordinator の journal と照らし合わせて、応答のない `dispatch` を送り直す
- ローカルでは、`dtnfabric` の Host を「SQLite を開き直す」で模擬する

## 5. fencing（古い世代を断る）

| 世代 | 持つ場所 | 断るもの |
|---|---|---|
| Actor の `epoch` | Actor Host（`meta.epoch`） | 古い Container からの応答・古い epoch の Contact を使う転送 |
| `coordEpoch` | Session Front が振り、各 Host が受けた最大の値（`meta.maxCoordEpoch`）を持つ | 古い Coordinator からの要求 |

- Durable Object は 1 つのインスタンスの中で直列に動くので、Host がそのまま fence になる
- 同じ Actor の Container が 2 つ動いても、古い epoch の応答は Host が捨てる。Host は新しい Container を起動する前に古いものを止める

## 6. 上限・片付け

| 項目 | 初期値 | 超えたとき |
|---|---|---|
| 1 Actor の Container の起動の回数 | 4（最初の 1 回＋復元 3 回） | ゲームを `dtn_actor_lost` で終える |
| 1 ゲームの Actor の復元の回数の合計 | 6 | 同上 |
| 1 回の復元の実時間 | 60 秒 | 同上 |
| 1 ゲームの Actor の復元にかかった実時間の合計 | 180 秒 | 同上 |
| 1 ゲームの Coordinator の作り直しの回数 | 3 | 同上 |
| 1 Actor の journal の行・保存の合計 | 20,000 行・8 MiB | `too_large` → `dtn_limit` で終える |
| Coordinator の journal の行・保存の合計（Session Front） | 50,000 行・16 MiB | 同上 |

- ゲームが終わったら（ラウンドの終了・時間の上限・切断・中断）、Session Front がすべての Host に `shutdown` を送り、Host は Container を止めてから保存をすべて消す（`deleteAll`）。止められなかったら、アラームで再試行する（[environment.md](./environment.md)）。片付けそのものも再実行できるようにする（D-8）
- 送り元の写しは、`collected` になったら bytes を消してよい（tombstone の行は残す）。`pending`・`prepared`・`activated` の bytes は、ゲームの途中では消さない

## 7. 実装の細部（DTN-07）

実装は `server/internal/dtn/host`（`Host`・`Store`・`Tx`・行の型・`ActorConn`・`Hooks`）・`server/internal/dtn/host/sqlitestore`（Go の SQLite adapter）・`server/internal/dtn/host/contracttest`（fixture の runner）。

### 7.1 保存

- `meta` の k は §2 のものに `journalRows`・`storedBytes`（§6 の上限を数える値。トランザクションごとに差分で更新する）を足す。値はすべて TEXT（数は 10 進）。`createdAt` は RFC 3339（UTC）
- `journal.payload` は要求の `payload` の JSON を受けたとおりの bytes で持つ（エンベロープの残りは列と `meta` から作れる）。`meta.initPayload` は `init` の intent を書くトランザクションで書く（intent のまま止まっても、復元が `init` を送れるように）
- SQLite は `PRAGMA journal_mode=WAL`・`synchronous=FULL`・`BEGIN IMMEDIATE`、接続は 1 つ。`schemaVersion` が 1 でないファイルは開かない（`unsupported_version`）
- 保存の合計（`storedBytes`）は、`bundles.bundle` の長さ＋`journal.payload` の長さ＋`journal.response` の長さの和。intent を書く前に「行の数＋1」と「合計＋payload」を、確定の前に確定後の合計を確かめ、超えたら `too_large`（何も書かない。確定の前なら行は intent のまま）

### 7.2 確定の規則

- Container の応答のうち、確定する（applied にする）のは `ok` と、Actor が決定的に出した誤り（`invalid`・`too_large`。再生でも同じ誤りになる）だけ。それ以外の誤り（`not_ready`・`stale_*`・`internal` など。Container が要求を適用していない）は確定せず、行は intent のまま応答を返す。`internal` のときは Container を失ったとみなす
- Container が上限の時間までに応答しない・接続が切れた・壊れた応答（版・`actor`・`eventSeq` が要求と合わない）・`internal` → `Hooks.OnActorLost` を 1 回呼び、`Host.Restart`（DTN-10 が使う。古い接続を閉じて `meta.epoch` を 1 増やす）までの新しい要求に `not_ready` を返す。applied の行の送り直しには、保存した応答を返し続ける
- 行が intent のまま残っているとき、それより大きい `eventSeq` の要求は `not_ready`（先に同じ `eventSeq` で送り直す）
- 確定の効果（`bundles`。`ok` の応答だけ。要求の効果を先に、出力の効果を出力の順に）:

| きっかけ | `bundles` の行 |
|---|---|
| 要求 `cargo.add` | 手紙ごとに要求の bytes で上書き。`held`・`cargo` |
| 要求 `unload` | 手紙ごとに要求の bytes で上書き。`held`・手紙の `place`（`port`・`relay`） |
| 要求 `deliver` | 手紙ごとに要求の bytes で上書き。`gone`・`delivered` |
| 要求 `port.remove`・`relay.remove`・`cargo.remove` | 行があれば `gone`・`moved`、`bundle` を NULL（所有権が移った後。tombstone） |
| 出力 `letter.created` | `data.letterId` の行を出力の bytes で上書き。`held`・`port`。bytes が無い・16 KiB を超える・`sha256(bundle) ≠ data.digest`・`data.size` が長さと違う → `invalid`（確定しない） |
| 出力 `letter.dropped`・`letter.delivered` | `data.letterId` の行があれば `gone`・`dropped`／`delivered` |

- `advance` のまとめ（§2）: 直前の行が applied で `ok` の `advance` なら、まとめる。ただし、まとめた上限への 1 回の再生の応答が上限（`outputs` の 4,096・電文の 1 MiB。bytes は `data.size` を base64 にした長さで見積もる）を超えるときは、まとめない
- まとめた行の `eventSeq` の送り直しには、保存した応答（2 つの `outputs` をつなげたもの）を返す。最初の応答（この要求の `outputs` だけ）とは違うので、Coordinator（DTN-05）は、適用済みの上限以下の `(t, class, seq)` の出力を捨てる
- `coordEpoch` の更新は、journal に入れない種類（`inspect`・`bundle.export`・`host.status`）でも行う（小さいトランザクション）
- `host.status` の応答は `outputs: [{type: "host.status", data: {epoch, lastEventSeq, container}}]`。`lastEventSeq` は applied の行の最大の `eventSeq`（intent の行は数えない）、`container` は `running`・`lost`・`stopped`
- `shutdown` は journal に入れない。Container に `shutdown` を送り（応答が無くても続ける）、接続を閉じ、保存を消す。消した後は `shutdown` に成功を、それ以外に `not_ready` を返す
- `transfer.*`・`contact.*` は受け口だけ（`internal`。処理は DTN-08・09）

### 7.3 契約テストの fixture

`server/internal/dtn/host/testdata/contract/*.json`。1 つのファイルが 1 つの場面で、ファイル名（拡張子を除く）が `name`。Go は `contracttest.RunDir`、TS（DTN-12）は同じ規則の runner で読む。

```json
{
  "name": "C01-idempotent",
  "description": "…",
  "meta": { "sessionId": "s_contract", "actor": "island:1", "epoch": 1 },
  "limits": { "maxJournalRows": 3 },
  "actorTimeoutMs": 50,
  "defaults": { "v": 1, "sessionId": "s_contract", "actor": "island:1", "epoch": 1, "coordEpoch": 1 },
  "steps": [
    { "note": "…", "request": { "eventSeq": 1, "kind": "init", "payload": { … } },
      "actorReply": { "outputs": [ … ], "cursor": { … } }, "crashAt": null,
      "expect": { "response": { … }, "actorCalled": true, "actorRequest": { … },
                  "rows": { "meta": { … }, "journal": [ … ], "bundles": [ … ] }, "crashed": false, "destroyed": false } }
  ]
}
```

- `meta`: Host の設定（新しい保存の `meta.sessionId`・`actor`・`epoch`）。`limits`（省略可）は actor-protocol.md §6 の上限を JSON の名前（`maxMessageBytes`・`maxBundleBytes`・`maxJournalRows`・`maxStoredBytes` など）で上書きする。`actorTimeoutMs`（省略可）は Container を待つ上限（既定 10 秒）
- `request` は電文の JSON（`defaults` の足りないキーを浅く補い、空白なしで送る）。`requestText` は bytes をそのまま送る電文（補わない）
- `actorReply` は偽の Container の応答: 省略か `null` は「呼ばれない」（呼ばれたら接続の誤り）、オブジェクトは応答（足りない `v`・`actor`・`epoch`・`eventSeq` は Host が送った要求の値、`status` は `"ok"`、`outputs` は `[]`、`cursor`・`error` は `null` で補う）、`"timeout"` は応答しない、`"disconnect"` は接続が切れる
- `crashAt`（省略可）は止める位置 `afterIntent`・`afterActorReply`・`afterCommit`。止めたら応答を返さず（`expect.crashed: true`）、Store を開き直して新しい Host で次の step へ進む（Container の台本は step ごと）
- `expect.actorCalled` を省略したら「`actorReply` が `null` でない」。Container は 1 つの step で 2 回以上呼ばれないこと
- 照合は部分一致: オブジェクトは期待のキーだけを比べる（期待が `null` ならキーが無くてもよい。期待が `"$absent"` ならキーが無いこと）、配列は同じ長さで要素ごと、数・文字列・真偽・`null` は等しいこと
- `rows` の形: 表ごとに主キーの順の行の配列（`meta` だけは `{k: v}` で v は TEXT）。列の名前は §2 の SQL のまま。`journal.payload`・`journal.response`・`transfers.letter_ids`・`digests`・`ack`・`contacts.body` は JSON の値として、`bundles.bundle` は base64（パディングあり）で、NULL は `null` で比べる
- `destroyed: true` は保存がすべて消えたこと（Go はファイルが無い、DO は `deleteAll` の後）
- 実装ごとに長さの違う値（`storedBytes`・応答の JSON の bytes）は fixture で比べない
