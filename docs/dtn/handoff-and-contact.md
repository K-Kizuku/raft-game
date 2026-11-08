# 寄港（Contact）と所有権の引き渡し（Handoff）

DTN-08（#165）・DTN-09（#166）・DTN-15（#172）の契約。判断は [ADR-D001](./adr/D001-architecture.md)（所有権は Coordinator の台帳で確定する）。

## 1. 用語

| 用語 | 意味 |
|---|---|
| `letterId` | ゲームの手紙の ID（`islandId × 10^9 + n`）。Bundle の一生を通じて変わらない |
| `bundleId` | BPv7 の Bundle の識別（送信元の EID と作成時刻の組。[bpv7-profile.md](./bpv7-profile.md)）。`letterId` と 1 対 1 |
| `transferId` | 1 回の引き渡し（送り元 → 受け取り）の ID。`x<eventSeq>` |
| `contactId` | 1 回の寄港の ID。`k<eventSeq>` |
| 論理的な持ち主 | Coordinator の台帳が記録する、その手紙の持ち主（`island:X/port`・`island:X/relay`・`ferry:F/cargo`・`delivered`・`dropped`）。常に 1 つ |
| 物理的な写し | Actor Host に保存された bytes。引き渡しの途中は 2 つあってよい |

## 2. Contact（寄港）

### 2.1 形

```json
{
  "contactId": "k1207",
  "sessionId": "s_…",
  "island": 2,
  "ferry": "f1",
  "islandEpoch": 1,
  "ferryEpoch": 2,
  "openedAt": 12500,
  "sig": "base64url(HMAC-SHA256(DTN_CONTACT_KEY, sig を除いた RFC 8785 の正準の JSON))"
}
```

- Coordinator が開く・閉じることを決め、Session Front が `DTN_CONTACT_KEY`（DTN の環境のシークレット。ローカルは起動ごとの乱数）で署名する。ブラウザの入力から直接は作らない
- 開く: 船が島に着いたとき（到着のイベント）と、ゲームの開始時に島に停泊している船
- 閉じる: 出航のコマンドを適用するとき。閉じてから船を `Sailing` にする
- Session Front は、開く・閉じるを両方の Actor Host に知らせる（`contact.open`・`contact.close`）。Host は、閉じた Contact の ID を保存し、それを使う転送を `contact_closed` で断る
- Actor の epoch が上がったら（Container の作り直し）、Coordinator は同じ寄港の Contact を新しい epoch で開き直す

### 2.2 Host が確かめること

`transfer.prepare`・`transfer.accept` を受けた Host は、次をすべて確かめる。1 つでも違えば断る。

1. 署名が正しい
2. `sessionId` が自分のもの
3. 自分が `island` か `ferry` のどちらかで、相手がもう一方
4. 自分の epoch が Contact の epoch と同じ
5. Contact が閉じられていない
6. （送り元）対象の手紙をすべて自分が持っていて、別の transfer の途中でない

これで、島から島への転送・船から船への転送・別のゲームへの転送・航行中の転送・古い世代からの転送が通らない（D-1・D-2・D-7）。

## 3. Handoff（所有権の引き渡し）

### 3.1 手順

```mermaid
sequenceDiagram
  participant C as Coordinator
  participant S as Session Front
  participant HS as 送り元の Host
  participant HD as 受け取る Host
  C->>S: dispatch transfer.prepare (journal に intent)
  S->>HS: transfer.prepare
  HS->>HS: bytes を prepared(transferId) にする（保存）
  HS->>HD: transfer.accept（CLA。bytes と digest）
  HD->>HD: pending(transferId) で保存（まだ使えない）
  HD-->>HS: ACK{transferId, digests}
  HS-->>S: ACK
  S-->>C: ACK
  C->>C: 台帳を CAS（持ち主: 送り元 → 受け取り）・journal に committed
  C->>S: transfer.activate（then: deliver / cargo.add など）
  S->>HD: transfer.activate → Container に then を送る
  C->>S: transfer.gc（then: port.remove / cargo.remove など）
  S->>HS: transfer.gc → Container に then を送る
```

| 段階（Coordinator の台帳） | 起きたこと | 次へ進む条件 |
|---|---|---|
| `proposed` | Coordinator が journal に intent を書いた | 送り元の Host が `transfer.prepare` を保存した |
| `persisted` | 受け取る Host が bytes を保存し ACK した | Coordinator が CAS した |
| `committed` | 論理的な持ち主が受け取り側になった | 受け取る Host が `activate` を適用した |
| `activated` | 受け取り側の Actor が使えるようになった | 送り元の Host が `gc` を適用した |
| `collected` | 送り元の写しが不要になった | — |

- `committed` の前は、受け取り側はその bytes を使わない（配達・積み込み・Raft への適用をしない）
- `committed` の後は取り消さない。送り元の写しは `collected` まで消さない（D-5）
- 取り消し（`transfer.abort`）は `committed` の前だけ。起きるのは、確認（§2.2）で断られたとき（バグか古い世代）と、ゲームを終えるときだけ
- 仮想時間は、Handoff が `collected` になるまで止める。だから途中で港の規則や出航が割り込むことはない

### 3.2 ゲームの操作と Handoff

Coordinator は、今の `Transport` と同じ確認（停泊中か・Contact が開いているか・積載の上限・倉庫の上限・手紙がそこにあるか）を、メタデータで先に行う。すべて通ったときだけ Handoff を始める（全部か無しか）。

| 操作 | 送り元 → 受け取り | 受け取り側の `then` | 送り元の `then` |
|---|---|---|---|
| 積む（`cmd.ferry.load`・`autoload`） | 島 X の港 → 船 F | `cargo.add` | `port.remove` |
| 下ろす | 船 F → 島 X（自島の手紙は港、他島の手紙は倉庫） | `unload`（手紙の ID の昇順。港に戻す手紙は R1〜R6 をかけ直す） | `cargo.remove` |
| 倉庫から積む（`cmd.relay.load`） | 島 X の倉庫 → 船 F | `cargo.add` | `relay.remove` |
| 到着の配達 | 船 F → 島 X（宛先が X の手紙。Cargo の順） | `deliver` | `cargo.remove` |

1 つの下ろす操作は 1 つの transfer にする。`unload` は手紙の ID の昇順で処理する（今の `Unload` と同じ順）。倉庫の上限（`relayCap`）は、Coordinator が先に確かめる。

### 3.3 止まったときの回復

| 止まった場所 | 回復 |
|---|---|
| `transfer.prepare` の保存の前 | Session Front が同じ `eventSeq` で送り直す |
| 送り元の保存の後、`accept` の前 | 同上。送り元の Host は保存済みの prepared を見て、`accept` を送り直す |
| 受け取り側の保存の後、ACK が届く前 | 同上。受け取り側は同じ transferId に同じ ACK を返す（中身が違えば `conflict`） |
| ACK の後、CAS の前（Coordinator が止まった） | Coordinator の復元で、intent があって committed がない transfer を `transfer.status` で照会する。受け取り側が保存済みなら CAS する |
| CAS の後、`activate` の前 | `activate` を送り直す（冪等） |
| `activate` の後、`gc` の前 | `gc` を送り直す（冪等） |

- **配達の重複を防ぐ（D-4）**: 島は配達した `letterId` を覚え、同じ手紙の 2 回目の `deliver` を `invalid` で断る。Host の `eventSeq` の冪等（[actor-protocol.md §5](./actor-protocol.md#5-冪等と順序)）と二重に守る
- **Raft の再送とは混同しない**: Raft が同じ内容を送り直したものは、別の `letterId` の別の手紙。宛先で捨てない

### 3.4 実装の細部（DTN-08）

実装は `server/internal/dtn/handoff`（台帳と §3.3 の表。決定的）と `server/internal/dtn/host`（`transfer.*`）。Host の保存の規則と fixture は [persistence-and-recovery.md §7.4](./persistence-and-recovery.md#74-handoffdtn-08)。

- **台帳 `handoff.Ledger`**: `Propose → Persisted(ack) → Commit → Activated → Collected`、`Proposed`・`Persisted` からだけ `Abort`。それ以外の遷移は `ErrTransition`（バグ）、取り消した transfer への ACK は `ErrAborted`（捨てる）。誤りのときは何も変えない
  - `Commit` の CAS: すべての手紙の持ち主が `From` で、別の transfer に握られていないときだけ持ち主を変える。手紙は `Commit` から `Collected` まで、その transfer に握られる（`Propose` は握らない。同じ手紙の 2 つの transfer は先に `Commit` した方だけが通る）
  - `Transfer.To` は受け取る Actor の置き場所、`Transfer.Final` は手紙ごとの `Commit` の後の持ち主（省略なら全部 `To`）。下ろす操作は `To = island:X/port`・`Final` に港と倉庫が混ざる。到着の配達は `Final` がすべて `delivered`
  - Handoff の外の持ち主の変化は `Create`（`letter.created`。島の港）と `Move`（港の規則での破棄など。CAS で、握られている手紙は `ErrHeld`）で記録する
  - 島から島・船から船の transfer は `Propose` で断る（D-1）
- **`handoff.Resume(stage, src, dst)`**: §3.3 の表を、台帳の段階と両方の Host の `transfer.status` の `state` で引く。台帳は Coordinator の journal から復元したもので Host より遅れることがあるので、`activate`・`gc` は「送り直す（eventSeq の冪等）」を返す。`proposed` で受け取り側が `pending` なら CAS（`ActionCommit`。受け取り側の `ack` を使う）。表に無い組み合わせ（committed なのに受け取り側に bytes が無い など）は `ActionFail`（`dtn_internal`）
- **`then` の手紙は bytes を持たない**: `transfer.activate` の `then`（`cargo.add`・`deliver`・`unload`）の手紙は `{letterId, createdAt, digest}`（`unload` は ＋`place`）で、`bundle` を書くと `invalid`。`digest` は ACK の値（受け取る Host は pending の行の digest と照らす）。`createdAt` は Coordinator が `letter.created` の `t` から持つ（Bundle の作成時刻は 0 なので bytes からは分からない）。受け取る Host が pending の bytes を付けた要求を Container に送り、それを journal に入れる
- `transfer.activate` の `then` にできるのは `cargo.add`・`deliver`・`unload`、`transfer.gc` の `then` は `port.remove`・`relay.remove`・`cargo.remove` だけ（違えば `invalid`）
- **ACK と状態の形**: 応答の `outputs` に、`transfer.prepare`・`transfer.accept` は `{type: "transfer.ack", data: {transferId, digests}}`、`transfer.status`・`transfer.abort` は `{type: "transfer.status", data: {transferId, state, ack?}}` を 1 つ入れる。`digests` は `letterIds` の順。`state` は `transfers.state` か、行が無ければ `none`
- **CLA と Contact の確認**: `host.Peer`（`Accept(ctx, 電文) → 応答の電文`。同じプロセスでは受け取る Host の `Handle`）と `host.ContactVerifier`。DTN-09 までの試験の実装 `RecordOnlyContacts` は、署名と epoch を見ず、`sessionId`・島と船の組・記録が閉じていないことだけを確かめる。どちらかが設定されていない Host は、`transfer.prepare`・`transfer.accept` を `internal` で断る。`contact.open`・`contact.close` は DTN-09（今は `internal`）
- **送り元の誤りの返し方**: CLA が届かない（受け取る Host が止まった・応答が失われた）は `not_ready`（Session Front は同じ eventSeq で `prepare` を送り直す）。受け取る Host の誤りの応答は、その code をそのまま返す。受け取る Host が別の bytes を ACK したら `conflict`
- **Coordinator の手順（DTN-05 が入れる）**: Coordinator は今の `Transport` と同じ確認の後、journal に intent を書いて `Ledger.Propose` → `transfer.prepare`（`eventSeq` は transferId の番号）→ ACK で `Persisted`・`Commit`・journal に committed → `transfer.activate` → `Activated` → `transfer.gc` → `Collected`。dispatch は送る前に journal に書く（復元で同じ eventSeq で送り直すため）。この手順の試験の役は、今は `server/internal/dtn/host/handoff_test.go` の `world` が持つ（DTN-05 の Coordinator に置き換える）

## 4. RPC CLA（初期）

- `transfer.accept` を、送り元の Actor Host が受け取る Actor Host の Durable Object を `fetch` ハンドラ（ADR-0023）で呼ぶ（`ACTOR_HOST.get(idFromName("<sessionId>/<actor>"))`）。ローカルは `dtnfabric` の中の関数呼び出し
- bytes は Actor Host の間だけを動く。Session Front と Coordinator は digest だけを見る
- Container の外向きの通信は使わない（`enableInternet: false` のまま）

## 5. MoQ CLA（DTN-15）

DTN-14 の PoC で採用と決めたときだけ実装する。CLA だけを差し替え、§2・§3 の契約は変えない。

- 送り元の Host（または PoC で決めた接続の主体）が、Track `dtn/<sessionId>/<contactId>/<from>-<to>`（例: `…/island:2-ferry:f1`。向きで分ける）に、1 つの transfer を 1 つの Group、1 つの Bundle を 1 つの Object として publish する
- 受け取り側は Contact が開いている間だけ subscribe し、§2.2 の確認と digest の確認をしてから保存する。ACK は RPC の経路で返す（MoQ の配信の成功を ACK とみなさない）
- Relay は永続の正本にしない。閉じた Contact の Object が後から届いても使わない
- RPC CLA は残し、どちらを使うかはセッションの作成時に決めて記録する。障害時に自動で切り替えない
