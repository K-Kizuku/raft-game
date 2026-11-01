# 通信プロトコル仕様（HTTP / WebSocket）

| 項目 | 内容 |
|---|---|
| 版 | v0.1（プロトコルバージョン `1`） |
| 状態 | Draft |
| 関連 | [DesignDoc §12, §13](./03-design-doc.md) / [ADR-0009](./adr/0009-server-authoritative-simulation-over-websocket.md) / [ADR-0014](./adr/0014-monorepo-and-shared-types.md) |

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

### `GET /api/healthz`

`200 {"status":"ok","build":"git:3f2c1ab"}`

### `GET /api/stages`

```json
{
  "stages": [
    {
      "id": "stage1",
      "title": "はじめての郵便船",
      "region": "隠岐諸島",
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
{ "stageId": "stage2", "seed": 20261101, "demo": false }
```

- `seed` を省略するとサーバーが決める
- `demo: true` はサーバーの `DEMO_CONTROLS=1` のときだけ受け付ける

レスポンス `201`:

```json
{ "sessionId": "s_8f3k2…", "token": "base64url(128bit)", "wsPath": "/ws/sessions/s_8f3k2…" }
```

エラー: `400`（不正なステージ）/ `429`（レート制限）/ `503`（同時セッション数の上限）

### `GET /ws/sessions/{id}?token=…`

WebSocket にアップグレードします。`Origin` は許可リストで検査します。同じセッションに 2 本目の接続が来たら、古い接続を閉じます（再接続のため）。

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
| `cmd.game.speed` | `{ "speed": 2 }` | 0.5 / 1 / 2 | P1 |
| `cmd.ferry.load` | `{ "ferryId": "f1", "letterIds": [12, 13] }` | 港の手紙を積む。全部積めない場合は全体を拒否する | P0 |
| `cmd.ferry.unload` | `{ "ferryId": "f1", "letterIds": [12] }` | 積んだ手紙をいまいる港へ戻す（P1 で中継倉庫へ預けるのにも使う） | P0 |
| `cmd.ferry.autoload` | `{ "ferryId": "f1", "to": 2 }` | おまかせ積み込み | P0 |
| `cmd.ferry.depart` | `{ "ferryId": "f1", "to": 2 }` | 出航 | P0 |
| `cmd.relay.load` | `{ "ferryId": "f1", "letterIds": [20] }` | 中継倉庫の手紙を積む | P1 |
| `cmd.sync` | `{}` | スナップショットを要求する | P0 |
| `sub.engineer` | `{ "enabled": true, "nodeId": 2 }` | エンジニアモードの購読（`nodeId` の詳細を 2Hz で送る） | P0 |
| `demo.trigger` | `{ "event": "storm", "routeIds": ["1-2","1-3"], "durationMs": 20000 }` | デモ用。デモ操作が有効なセッションだけ | P0 |
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
| `ack` | `{ "cmdId": "c-12", "ok": true }` / `{ "cmdId": "c-12", "ok": false, "code": "route_blocked", "message": "…" }` | コマンドの結果 |
| `node.detail` | `NodeDetail`（§5.4） | エンジニアモードの購読中、2Hz |
| `round.result` | `RoundResult`（§5.5） | ラウンド終了時 |
| `error` | `{ "code": "…", "message": "…", "fatal": true }` | `fatal` なら直後に切断する |
| `pong` | `{ "clientTime": 123456, "simTime": 61200 }` | — |

### 5.1 Snapshot

```json
{
  "protocol": 1,
  "session": { "id": "s_…", "stageId": "stage2", "seed": 20261101, "demo": false },
  "phase": "sailing",
  "clock": { "t": 61200, "speed": 1, "paused": false, "endsAt": 210000 },
  "stage": {
    "title": "島から返事が来ない！",
    "islands": [
      { "id": 1, "name": "島後", "municipality": "隠岐の島町", "layout": {"x": 620, "y": 180},
        "population": { "value": null, "provenance": "fact", "source": "mlit-ritou-list-2026" },
        "color": "#E07A5F" }
    ],
    "routes": [ { "id": "1-2", "a": 1, "b": 2, "travelMs": 9000 } ],
    "resources": [ { "id": "firework:a", "kind": "firework", "label": "大花火 A", "critical": true } ]
  },
  "ferries": [
    { "id": "f1", "name": "おき丸", "capacity": 4, "state": "sailing", "from": 1, "to": 2,
      "departAt": 58000, "arriveAt": 67000, "readyAt": 68500, "cargo": [31, 32] }
  ],
  "routesState": [ { "id": "1-2", "blockedUntil": null } ],
  "letters": [ /* Letter（§5.2）をすべて */ ],
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
    "entries": [ { "index": 14, "term": 2, "type": "normal", "summary": "stall.reserve stage2-0005 (中ノ島)" } ],
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
  "log": [ { "index": 19, "term": 3, "type": "normal", "summary": "firework.reserve stage2-0009 (西ノ島)", "committed": false } ],
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
    { "t": 72000, "text": "嵐で島後が孤立しました。島後は過半数と連絡が取れなくなったので、自分から代表を降りました。", "concepts": ["CheckQuorum"] }
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
| `phase.changed` | `phase` | 開会式・航行・終了 |
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
| `node.state` | `id`, `role`, `term`, `lead`, `commit`, `applied`, `lastIndex` | いずれかの値が変わった（同じステップ内の変化はまとめる） |
| `node.leader_elected` | `id`, `term` | 新しい Leader が誕生した（開会式を含む） |
| `node.stepped_down` | `id`, `term`, `reason`（`check_quorum` / `higher_term`） | Leader でなくなった |
| `request.created` | `request`（Request 全体） | 依頼の発生 |
| `request.updated` | `id`, `status`, `expired`, `result` | 状態の変化 |
| `ledger.applied` | `islandId`, `index`, `reqId`, `result` | 台帳への適用（エンジニアモード・トレース用） |
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
