# 通信プロトコル仕様（HTTP / WebSocket）

| 項目 | 内容 |
|---|---|
| 版 | v0.3（プロトコルバージョン `1`） |
| 更新日 | 2026-11-06 |
| 状態 | レビュー済み |
| 関連 | [DesignDoc §12, §13](./03-design-doc.md) / [ADR-0009](./adr/0009-server-authoritative-simulation-over-websocket.md) / [ADR-0014](./adr/0014-monorepo-and-shared-types.md) / [ADR-0016](./adr/0016-infrastructure-cloudflare-with-cf-cli.md) / [ADR-0017](./adr/0017-persist-input-log-in-durable-object.md) |

型の正本は Go の `server/internal/protocol` です。TypeScript の型は `tygo` で `web/src/protocol/` に生成します。この文書と Go のコードが食い違ったら、**コードを直してからこの文書を更新**してください。

---

## 1. 共通

- 文字コード UTF-8、JSON
- 時刻 `t` はすべてゲーム内時間（ミリ秒、整数）。実時間は送らない
- ID の表記
  - 島（= Raft ノード）: 数値 `1..N`（Raft の ID と同じ）
  - フェリー: 文字列 `"f1"`, `"f2"` …
  - 手紙: 数値（セッション内の連番）
  - 依頼: 文字列 `"<stageId>-<連番4桁>"`（例: `"stage2-0007"`）
  - 航路: 文字列 `"<小さい島ID>-<大きい島ID>"`（例: `"1-3"`）

---

## 2. HTTP API

本番では、§2 の API はすべて Cloudflare の Worker が受けます。WebSocket は Worker から Durable Object `GameSession` に転送され、Durable Object がコンテナ（Go）との間を中継します。§3〜§7 の電文は、ブラウザと Go の間でそのまま通ります（Durable Object は中身を書き換えない）。ローカルの `standalone` モードでは、Go サーバーが §2 の API を自分で提供します。

### `GET /api/healthz`

`200 {"status":"ok","build":"git:3f2c1ab"}`

### `GET /api/stages`

```json
{
  "stages": [
    {
      "id": "stage1",
      "title": "はじめての郵便船",
      "region": "瀬戸内（香川県）",
      "islands": 3,
      "ferries": 1,
      "durationMs": 180000,
      "unlock": null
    }
  ]
}
```

### `POST /api/sessions`

リクエスト:

```json
{ "stageId": "stage2", "seed": 20261101, "demo": false, "turnstileToken": "…" }
```

- `seed` を省略するとサーバーが決める
- `turnstileToken` は Cloudflare Turnstile のウィジェットが返すトークン。Worker が検証する（コストガード G4。無効にした場合は省略可）
- `demo: true` は `demo` モードでデプロイした Worker（`DEMO_CONTROLS=1`）のときだけ受け付ける
- Worker は Durable Object を作り、コンテナの起動を始めてから応答する（ステージ紹介の間に起動を終える）

レスポンス `201`:

```json
{ "sessionId": "s_<ランダム 128bit>.<HMAC 署名>", "token": "base64url(128bit)", "wsPath": "/ws/sessions/s_…" }
```

- `sessionId` には Worker の秘密鍵（`SESSION_SIGNING_KEY`）による署名が付く。Worker は WS の接続時に署名を確かめ、合わなければ Durable Object を呼ばずに `404` を返す（コストガード G3）

エラー:

| HTTP | `code` | 意味 | 画面の表示 |
|---|---|---|---|
| 400 | `bad_request` | 不正なステージなど | — |
| 403 | `turnstile_failed` | Turnstile の確認に失敗 | 「もう一度お試しください」 |
| 429 | `rate_limited` | レート制限 | 「少し待ってからお試しください」 |
| 503 | `budget_exhausted` | 費用の上限に達した（G1） | 「本日の受付は終了しました」 |
| 503 | `sessions_disabled` | 管理者が受付を一括で止めている（G6） | 「ただいま受付を停止しています」 |

エラーの本文は `{ "code": "…", "message": "…" }` です。

### `GET /ws/sessions/{id}?token=…`

WebSocket にアップグレードします。Worker が `Origin` を許可リストで検査し、Durable Object がトークンを検証します。同じセッションに 2 本目の接続が来たら、古い接続を閉じます（再接続のため）。

---

### `GET /api/admin/guards`・`PATCH /api/admin/guards`（管理用）

コストガード（[ADR-0018](./adr/0018-cost-guard.md)）の確認と切り替えです。`Authorization: Bearer <ADMIN_TOKEN>` が必要です。

`GET` のレスポンス:

```json
{
  "guards": {
    "budget":      { "enabled": true, "monthlyUsd": 30, "dailyUsd": 1 },
    "gameTime":    { "enabled": true, "maxMinutes": 30, "maxAlarms": 20 },
    "signature":   { "enabled": true, "locked": true },
    "turnstile":   { "enabled": true },
    "rateLimit":   { "enabled": true, "perIpPerMinute": 10 },
    "cpuMs":       { "value": 50, "note": "変更には再デプロイが必要" },
    "acceptGames": true
  },
  "usage": { "todayUsd": 0.12, "monthUsd": 1.48, "activeGames": 2 },
  "unitCostUsd": { "lite": 0.0065, "standard-1": 0.040 }
}
```

`PATCH` のリクエスト（変えたい項目だけ）:

```json
{ "budget": { "enabled": false }, "reason": "デモ当日のため一時的に外す" }
```

- `signature` は変更できません（`locked: true`。変えようとすると `400 locked`）
- `acceptGames: false` で、新しいゲームの受付を一括で止めます（遊んでいるゲームは続きます）
- 変更は数秒で反映され、`CostGuard` に記録（いつ・何を・理由）が残ります

---

## 3. WebSocket のエンベロープ

すべてのメッセージは次の形です。

```json
{ "v": 1, "type": "events", "seq": 42, "payload": { } }
```

| フィールド | 方向 | 内容 |
|---|---|---|
| `v` | 双方向 | プロトコルバージョン。一致しなければサーバーは `error` を返して切断する |
| `type` | 双方向 | メッセージの種類 |
| `seq` | サーバー → クライアント | サーバーからのメッセージの連番（1 から）。抜けを検出したらクライアントは `cmd.sync` を送る |
| `cmdId` | クライアント → サーバー | クライアントが付けるコマンドの ID（任意の文字列）。`ack` で返る |
| `payload` | 双方向 | 種類ごとの中身 |

---

## 4. クライアント → サーバー

| type | payload | 説明 | 優先 |
|---|---|---|---|
| `hello` | `{ "client": "web/0.1.0", "resume": false }` | 接続直後に送る。サーバーは `snapshot` を返す | P0 |
| `cmd.game.start` | `{}` | 開会式を始める（ステージ紹介の「出航する」） | P0 |
| `cmd.game.pause` | `{ "paused": true }` | 一時停止・再開 | P0 |
| `cmd.game.speed` | `{ "speed": 2 }` | 0.5 / 1 / 2 / 4。デモ操作が有効なセッションでは 8 も | P0 |
| `cmd.game.skip` | `{ "until": "next_notable" }` | デモ用。次の船の到着・台本の出来事・依頼の発生まで進めて一時停止する（最大 30 秒先まで） | P0 |
| `cmd.ferry.load` | `{ "ferryId": "f1", "letterIds": [12, 13] }` | 港の手紙を積む。全部積めない場合は全体を拒否する | P0 |
| `cmd.ferry.unload` | `{ "ferryId": "f1", "letterIds": [12] }` | 積んだ手紙をいまいる港へ戻す（P1 で中継倉庫へ預けるのにも使う） | P0 |
| `cmd.ferry.autoload` | `{ "ferryId": "f1", "to": 2 }` | おまかせ積み込み | P0 |
| `cmd.ferry.depart` | `{ "ferryId": "f1", "to": 2 }` | 出航 | P0 |
| `cmd.relay.load` | `{ "ferryId": "f1", "letterIds": [20] }` | 中継倉庫の手紙を積む | P1 |
| `cmd.sync` | `{}` | スナップショットを要求する | P0 |
| `sub.engineer` | `{ "enabled": true, "nodeId": 2 }` | エンジニアモードの購読（`nodeId` の詳細を 2Hz で送る） | P0 |
| `demo.trigger` | `{ "event": "storm" }` | デモ用。ステージ定義の `demo.storm`（対象の航路・時間）で嵐を起こす。デモ操作が有効なセッションだけ | P0 |
| `ping` | `{ "clientTime": 123456 }` | 15 秒ごと | P0 |

### 4.1 コマンドの検証とエラーコード

コマンドは、セッションループがゲーム内時間の「いま」で適用します。結果は `ack` で返ります。

| code | 意味 |
|---|---|
| `ferry_not_docked` | 船が港にいない |
| `ferry_not_ready` | 停泊時間がまだ終わっていない |
| `ferry_broken` | 故障中 |
| `capacity_exceeded` | 積載量を超える |
| `letter_not_here` | 手紙がその港（または船）にない |
| `no_route` | 直接の航路がない |
| `route_blocked` | 嵐で航路が使えない |
| `phase_invalid` | いまのフェーズでは使えないコマンド |
| `rate_limited` | コマンドが多すぎる |
| `bad_request` | 形式が不正 |

---

## 5. サーバー → クライアント

| type | payload | 説明 |
|---|---|---|
| `snapshot` | `Snapshot`（§5.1） | 接続時・`cmd.sync` の応答・送信キューがあふれたとき |
| `events` | `{ "clock": Clock, "events": Event[] }` | 100ms ごとにまとめて送る（イベントがなくても 1 秒に 1 回は `clock` のために送る） |
| `ack` | `{ "cmdId": "c-12", "ok": true, "t": 61200 }` / `{ "cmdId": "c-12", "ok": false, "code": "route_blocked", "message": "…" }` | コマンドの結果。`t` は適用したゲーム内時刻で、Durable Object が入力ログに記録するのに使う |
| `node.detail` | `NodeDetail`（§5.4） | エンジニアモードの購読中、2Hz |
| `round.result` | `RoundResult`（§5.5） | ラウンド終了時 |
| `error` | `{ "code": "…", "message": "…", "fatal": true }` | `fatal` なら直後に切断する |
| `pong` | `{ "clientTime": 123456, "simTime": 61200 }` | — |

### 5.1 Snapshot

```json
{
  "protocol": 1,
  "session": { "id": "s_…", "stageId": "stage2", "seed": 20261101, "demo": false, "restored": false },
  "phase": "sailing",
  "clock": { "t": 61200, "speed": 1, "paused": false, "endsAt": 210000, "allowedSpeeds": [0.5, 1, 2, 4] },
  "stage": {
    "title": "島から返事が来ない！",
    "islands": [
      { "id": 1, "name": "福江島", "municipality": "五島市", "layout": {"x": 620, "y": 180},
        "population": { "value": 31945, "asOf": "令和8年4月1日", "provenance": "fact", "source": "mlit-ritou-2026" },
        "color": "#E07A5F" }
    ],
    "routes": [ { "id": "1-2", "a": 1, "b": 2, "travelMs": 4000, "provenance": "creative" } ],
    "resources": [ { "id": "firework:a", "kind": "firework", "label": "大花火 A", "critical": true } ]
  },
  "ferries": [
    { "id": "f1", "name": "ごとう丸", "capacity": 4, "state": "sailing", "from": 1, "to": 2,
      "departAt": 58000, "arriveAt": 67000, "readyAt": 68500, "cargo": [31, 32] }
  ],
  "routesState": [ { "id": "1-2", "blockedUntil": null } ],
  "letters": [ /* 港と船の上にある Letter（§5.2）。P1 では中継倉庫も。配達済み・破棄は含めない */ ],
  "requests": [ /* Request（§5.3）をすべて */ ],
  "nodes": [ { "id": 1, "role": "leader", "term": 2, "lead": 1, "commit": 14, "applied": 14, "lastIndex": 15 } ],
  "islandsState": [ { "id": 1, "satisfaction": 64, "lastDeliveredAt": 60500 } ],
  "score": { "total": 520, "successes": 3, "target": 5, "criticalSecured": ["firework:a"] },
  "lastSeq": 41
}
```

### 5.2 Letter

```json
{
  "id": 31,
  "kind": "raft",
  "from": 1, "to": 2,
  "createdAt": 57000,
  "loc": { "type": "ferry", "ferryId": "f1" },
  "label": "append",
  "raft": {
    "type": "MsgApp", "term": 2, "logTerm": 2, "index": 13, "commit": 13,
    "entries": [ { "index": 14, "term": 2, "type": "normal", "summary": "stall.reserve stage2-0005 (奈留島)" } ],
    "reject": false, "rejectHint": 0,
    "stale": false
  }
}
```

- `loc.type`: `port`（`islandId`）/ `ferry`（`ferryId`）/ `relay`（`islandId`、P1）/ `delivered` / `dropped`
- `label`: ゲーム上の手紙の区分。`heartbeat` / `heartbeat_resp` / `append` / `append_probe` / `append_resp` / `append_reject` / `prevote` / `prevote_resp` / `vote` / `vote_resp` / `request` / `other`
- `raft`: `kind` が `raft` のとき。**通常の表示には `label` だけを使い**、`raft` の中身はエンジニアモードでだけ使う
- `stale`: 手紙を作った島の今の Term より、手紙の Term が古いとき `true`（`letter.stale` イベントで更新される）
- `kind` が `request` のときは `raft` の代わりに `"request": { "reqId": "stage2-0005", "hops": 1 }`

### 5.3 Request

```json
{
  "id": "stage2-0005",
  "origin": 2,
  "kind": "stall.reserve",
  "resource": "stall:main",
  "critical": false,
  "createdAt": 10000,
  "deadline": 70000,
  "status": "proposed",
  "expired": false,
  "result": null
}
```

`status`: `received`（受付）/ `waiting`（送付待ち）/ `in_transit`（輸送中）/ `proposed`（審議中）/ `committed`（共和国で確定）/ `succeeded`（確定）/ `rejected`（予約失敗）

### 5.4 NodeDetail（エンジニアモード）

```json
{
  "id": 2, "role": "follower", "term": 3, "vote": 2, "lead": 2,
  "commit": 18, "applied": 18, "lastIndex": 19,
  "log": [ { "index": 19, "term": 3, "type": "normal", "summary": "firework.reserve stage2-0009 (中通島)", "committed": false } ],
  "progress": [ { "id": 1, "match": 15, "next": 16, "state": "probe", "recentActive": false } ],
  "ledger": { "firework:a": "stage2-0002", "stall:main": ["stage2-0001", "stage2-0005"] },
  "electionEstimate": { "ticksSinceLeaderContact": 12, "note": "推定値" }
}
```

`progress` は、そのノードが Leader のときだけ入ります。`log` は末尾の 50 件です。

### 5.5 RoundResult

```json
{
  "score": { "total": 1520, "breakdown": { "requests": 1300, "expired": -100, "ferryCost": -180, "fairness": 500 } },
  "stars": 2,
  "stats": {
    "succeeded": 7, "rejected": 2, "expired": 2,
    "lettersDelivered": { "heartbeat": 40, "append": 22, "append_resp": 19, "vote": 3, "request": 9 },
    "leaderChanges": 1,
    "avgCommitLatencyMs": 18400,
    "longestSilenceMs": 41000,
    "finalLogs": [ { "id": 1, "lastIndex": 24, "commit": 24, "terms": [[1, 1, 9], [3, 10, 24]] } ]
  },
  "story": [
    { "t": 72000, "text": "嵐で福江島が孤立しました。福江島は過半数と連絡が取れなくなったので、自分から代表を降りました。", "concepts": ["CheckQuorum"] }
  ],
  "inputLogAvailable": true
}
```

`finalLogs[].terms` は `[term, 最初の index, 最後の index]` の配列で、ログを Term ごとの帯として描くのに使います。

---

## 6. イベント（`events.events[]`）

すべてのイベントは `{ "seq": 1203, "t": 61200, "type": "…", ... }` の形です（`seq` はイベントの連番で、エンベロープの `seq` とは別）。このイベント列がそのままトレースになります（[DesignDoc §10.2](./03-design-doc.md)）。

| type | フィールド | 発生するとき |
|---|---|---|
| `phase.changed` | `phase`（`opening` / `sailing` / `ended`） | 開会式・航行・終了 |
| `letter.created` | `letter`（Letter 全体） | Raft が出力した / 依頼書を書いた |
| `letter.moved` | `letterId`, `loc` | 積む・降ろす・預ける |
| `letter.delivered` | `letterId`, `islandId`, `accepted`（`Step` がエラーを返さなかったか） | 船が着いて配達した |
| `letter.dropped` | `letterId`, `reason`（`superseded` / `overflow`） | 港の規則で処分した |
| `letter.stale` | `letterId` | 送信元の Term が上がり、手紙が古くなった |
| `ferry.departed` | `ferryId`, `from`, `to`, `departAt`, `arriveAt` | 出航 |
| `ferry.arrived` | `ferryId`, `islandId`, `readyAt` | 到着 |
| `ferry.broken` / `ferry.repaired` | `ferryId`, `until` | 故障（P1） |
| `route.blocked` / `route.unblocked` | `routeIds`, `until` | 嵐 |
| `forecast` | `event`, `at`, `routeIds` | 嵐の予報 |
| `node.state` | `id`, `role`（`follower` / `pre_candidate` / `candidate` / `leader`）, `term`, `lead`, `commit`, `applied`, `lastIndex` | いずれかの値が変わった（同じステップ内の変化はまとめる） |
| `node.leader_elected` | `id`, `term` | 新しい Leader が誕生した（開会式を含む） |
| `node.stepped_down` | `id`, `term`, `reason`（`check_quorum` / `higher_term`） | Leader でなくなった |
| `request.created` | `request`（Request 全体） | 依頼の発生 |
| `request.updated` | `id`, `status`, `expired`, `result` | 状態の変化 |
| `ledger.applied` | `islandId`, `index`, `reqId`, `result`（`ok` / `rejected` / `duplicate`）, `reason`（`rejected` のときだけ） | 台帳への適用（エンジニアモード・トレース用） |
| `island.updated` | `id`, `satisfaction`, `lastDeliveredAt` | 満足度の変化 |
| `score.updated` | `total`, `successes`, `criticalSecured` | スコアの変化 |
| `notice` | `islandId`, `key`, `params` | 島のキャラクターの通知。文言はクライアントが `key` から組み立てる |

`node.stepped_down.reason` はゲーム側の推定です（`etcd-io/raft` は理由を外部に出さないため、直前に受け取ったメッセージの Term などから判定します）。

---

## 7. Clock

```json
{ "t": 61200, "speed": 1, "paused": false, "endsAt": 210000 }
```

クライアントはこれを受け取った時刻（`performance.now()`）と組にして保持し、描画のたびにゲーム内時刻を推定します（[DesignDoc §13.3](./03-design-doc.md)）。

---

## 8. 互換性の方針

- 後方互換な変更（フィールドやイベントの追加）では `v` を上げない。クライアントは知らないフィールド・イベントを無視する
- 互換性のない変更では `v` を上げる。MVP の間は、サーバーとクライアントを常に同時にデプロイするので、旧バージョンとの共存はしない

---

## 9. 内部の電文（Durable Object ⇔ コンテナ）

Durable Object だけが送る電文です。ブラウザから届いた `internal.*` は、Durable Object が破棄します。エンベロープは §3 と同じです。

| type | 方向 | payload | 説明 |
|---|---|---|---|
| `internal.init` | DO → コンテナ | `{ "sessionId": "s_…", "stageId": "stage2", "seed": 20261101, "demo": false }` | 新しいゲームを始める。コンテナは `snapshot` を返す |
| `internal.restore` | DO → コンテナ | `{ "sessionId": "s_…", "stageId": "stage2", "seed": 20261101, "demo": false, "inputLog": InputLog }` | コンテナが止まった後の復元。コンテナは `sim.Replay` で入力ログの最後の時刻まで進め、一時停止の状態で `snapshot`（`session.restored: true`）を返す |
| `internal.result` | コンテナ → DO | `RoundResult`（§5.5） | ラウンドの終了時。DO が SQLite に保存する（ブラウザには別に `round.result` が届く） |
| `internal.ping` / `internal.pong` | 双方向 | `{}` | 死活監視（5 秒ごと）。3 回続けて応答がなければ、DO はコンテナが止まったとみなして復元する |

`InputLog` の形式は [DesignDoc §10.1](./03-design-doc.md) のとおりです。

