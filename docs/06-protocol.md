# 通信プロトコル仕様（HTTP / WebSocket）

| 項目 | 内容 |
|---|---|
| 版 | v0.6（プロトコルバージョン `1`） |
| 更新日 | 2026-11-07 |
| 状態 | レビュー済み |
| 関連 | [DesignDoc §12, §13](./03-design-doc.md) / [ADR-0009](./adr/0009-server-authoritative-simulation-over-websocket.md) / [ADR-0014](./adr/0014-monorepo-and-shared-types.md) / [ADR-0016](./adr/0016-infrastructure-cloudflare-with-cf-cli.md) / [ADR-0017](./adr/0017-persist-input-log-in-durable-object.md) / [ADR-0018](./adr/0018-cost-guard.md) / [ADR-0023](./adr/0023-durable-object-fetch-handler.md) |

型の正本は Go の `server/internal/protocol` です。TypeScript の型は `tygo` で `web/src/protocol/protocol.gen.ts` に生成します（生成物。直接編集しない）。イベントと電文の判別可能なユニオン型（`ServerEvent`・`ClientMessage`・`ServerMessage`・`InternalMessage`）は手書きの `web/src/protocol/events.ts`・`messages.ts` にあり、画面などからは入口の `web/src/protocol/index.ts` から import します。`sim` の型との変換は `server/internal/protocol/simconv` が行います。生成は `make gen`、生成物に差分がないかの検査は `make gen-check` です。この文書と Go のコードが食い違ったら、**コードを直してからこの文書を更新**してください。

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

### 共通の応答とエラー

Worker（`web/worker/routes/`）が返す本文はすべて JSON で、ヘッダーは `content-type: application/json; charset=utf-8` と `cache-control: no-store` です（`201` でトークンを返すため、すべての応答に付ける）。CORS のヘッダーは返しません（画面と API は同じオリジン）。

エラーの本文は `{ "code": "…", "message": "…" }` です。`message` は短い英語で、内部の詳細（例外のメッセージ・スタック・クエリ文字列）を含みません。

| HTTP | `code` | いつ |
|---|---|---|
| 400 | `bad_request` | `POST /api/sessions`・`PATCH /api/admin/guards` の本文が不正 |
| 400 | `locked` | `PATCH /api/admin/guards` で変えられない項目（`signature`・`cpuMs`・`gameTime.enabled`・`gameTime.maxAlarms`）を変えようとした |
| 401 | `unauthorized` | 管理用 API の `Authorization` がない・違う。`www-authenticate: Bearer` を付ける |
| 403 | `forbidden_origin` | `Origin` が許可リストにない・必須のルートで `Origin` がない |
| 403 | `turnstile_failed` | Turnstile の確認に失敗 |
| 404 | `not_found` | 知らないパス、`/ws/sessions/{id}` の id が不正・検証に失敗・部品がない、管理用 API の部品がない・`ADMIN_TOKEN` が未設定や不正 |
| 405 | `method_not_allowed` | パスは知っているがメソッドが違う（`HEAD`・`OPTIONS` も）。`Allow` ヘッダーに許すメソッドを `, ` 区切りで入れる |
| 426 | `upgrade_required` | `/ws/sessions/{id}` に `Upgrade: websocket` がない |
| 429 | `rate_limited` | レート制限 |
| 500 | `internal_error` | Worker の中で例外が出た |
| 503 | `budget_exhausted` | 費用の上限に達した（G1） |
| 503 | `sessions_disabled` | 管理者が受付を一括で止めている（G6）、または受付の部品がそろっていない・`GUARDS` が不正 |
| 503 | `guards_invalid` | 管理用 API で、コストガードの設定 `GUARDS` が未設定・不正 |

ルートはパスの**完全一致**で決まります（大文字・小文字を区別し、末尾の `/` を許さない）。下の表にないパス（`/api`・`/api/`・`/api/healthz/`・`/ws/sessions/`・`/ws/sessions/a/b` など）は、メソッドや `Origin` に関係なく `404 not_found` です。検査の順序は「パス → メソッド（405）→ `Origin`（403）→ ルートごとの処理」です。

| パス | メソッド | `Origin` |
|---|---|---|
| `/api/healthz` | `GET` | あれば照合 |
| `/api/stages` | `GET` | あれば照合 |
| `/api/sessions` | `POST` | **必須** |
| `/api/admin/guards` | `GET`・`PATCH` | あれば照合 |
| `/ws/sessions/{id}` | `GET` | **必須** |

`Origin` の規則（NFR-07）:

- 許可リストは `ALLOWED_ORIGINS`（カンマ区切り）。各要素の前後の空白を除き、`http:`・`https:` の URL として読めるものだけを、そのオリジン（パスや末尾の `/` を落としたもの）として使う。読めない要素は無視する
- 有効な要素が 1 つもない（未設定・空・不正な要素だけ）ときは、リクエスト自身のオリジンだけを許す（同じオリジンだけ。ローカルの開発で設定が要らないため）。有効な要素があるときは、その集合だけを許す
- 照合は文字列の完全一致。`null`・複数の値・大文字の違いは一致しない
- 必須のルートで `Origin` がなければ `403`。必須でないルートでは、`Origin` がなければ通す（`curl` でのヘルスチェック・管理用 API のため）

### `GET /api/healthz`

`200 {"status":"ok","build":"git:3f2c1ab"}`

- `build` は Worker の `BUILD_ID`。未設定・空なら `"dev"`

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

- `seed` を省略すると Worker が決める（0 以上 2^32 未満の整数）。指定するときは 0 以上 `Number.MAX_SAFE_INTEGER`（2^53 − 1）以下の整数（`"1"` のような文字列は不可）
- `turnstileToken` は Cloudflare Turnstile のウィジェットが返すトークン（文字列、2048 文字まで）。Worker が検証する（コストガード G4。無効にした場合は省略可）
  - `turnstile.enabled`（`CostGuard` の設定）が有効なら、トークンがない・空・検証に失敗した要求は `403 turnstile_failed`。無効なら、トークンがあっても無視する（siteverify を呼ばない）
  - Worker は siteverify（`POST https://challenges.cloudflare.com/turnstile/v0/siteverify`、5 秒で打ち切る）の応答の `success` が真偽値の `true` であることだけを確かめる。`hostname`・`action`・`cdata` は確かめない（ホスト名の制限はウィジェットの設定で行う）。シークレット `TURNSTILE_SECRET` がない Worker では、有効なときはすべて `403`
  - トークンは 1 回しか使えず、発行から 300 秒で切れる（Cloudflare の仕様）。`403 turnstile_failed` のあと、画面はウィジェットをやり直す
- `demo: true` は `demo` モードでデプロイした Worker（`DEMO_CONTROLS=1`）のときだけ受け付ける。それ以外の Worker に `demo: true` を送ると `400`（黙って `false` にしない）。省略すると `false`
- 本文は `Content-Type: application/json`（`; charset=utf-8` などの引数は可）の UTF-8 の JSON オブジェクトで、4096 バイトまで。`stageId`・`seed`・`demo`・`turnstileToken` 以外のキーがあれば `400`。`stageId` は `GET /api/stages` の `id` のどれか
- 検査の順序は `Origin`（必須）→ レート制限 → 本文 → Turnstile → 費用の予約（`CostGuard`）。どこかで止まったら、それより後は行わない
- レート制限（コストガード G4）は IP ごと（IPv6 は先頭の 64 ビットごと。IPv4 射影の IPv6 は IPv4 として数える）に 1 分 10 回。`CF-Connecting-IP` がない・読めない要求は、全部で 1 つの枠を共有する。本文を読む前に数える（`429` のとき本文を読まない）
  - 回数は Workers の Rate Limiting の束縛 `SESSION_RATE_LIMIT`（`simple: { limit: 10, period: 60 }`）で、デプロイのときに決まる。Cloudflare の拠点ごとに数える近似の値で、厳密な上限ではない（[ADR-0024](./adr/0024-worker-rate-limit-binding.md)。ゾーンの WAF と合わせて使う）
  - `rateLimit.enabled`（`CostGuard` の設定）が無効なら数えない。有効で束縛がない・壊れているときは、すべて `429`
- `turnstile.enabled`・`rateLimit.enabled`・`gameTime.maxMinutes` は `CostGuard` の設定を読む。読めない（`GUARDS` が不正・`CostGuard` がない）ときは、Turnstile とレート制限を有効、`maxMinutes` を 60 として扱う
- Worker は Durable Object を作り、コンテナの起動を始めてから応答する（ステージ紹介の間に起動を終える）。Durable Object の作成に失敗したら、予約を取り消して `500 internal_error`
- 受付の部品（レート制限・Turnstile・署名・`CostGuard`・`GameSession`）がそろっていない Worker は、`503 sessions_disabled` を返す

レスポンス `201`:

```json
{ "sessionId": "s_<base64url(21 バイト)>.<base64url(32 バイト)>", "token": "<base64url(16 バイト)>", "wsPath": "/ws/sessions/s_…" }
```

- `sessionId` には Worker の秘密鍵（`SESSION_SIGNING_KEY`）による署名が付く。Worker は WS の接続時に署名を確かめ、合わなければ Durable Object を呼ばずに `404` を返す（コストガード G3）。署名の確認を外す設定はない
- `sessionId` の形式（74 文字。`=` の埋め草のない base64url、RFC 4648 §5）:

  ```text
  sessionId = "s_" + base64url(payload) + "." + base64url(signature)
  payload   = 版（1 バイト、0x01） ‖ 乱数（16 バイト = 128 bit） ‖ 期限（4 バイト、UNIX 秒の符号なし整数・ビッグエンディアン）
  signature = HMAC-SHA256(SESSION_SIGNING_KEY, UTF-8("raft-game.session.v1\n" + ホスト名 + "\n" + base64url(payload)))
  ```

  - ホスト名は要求の URL の `host`（小文字。ポートがあれば含む）。別のホスト名（一般公開とデモ用など）で発行した `sessionId` は通らない
  - 有効期限は発行 + `gameTime.maxMinutes` + 5 分（5 分は切断されてからの保持時間）。期限は `sessionId` の中にあるので、発行の後に `maxMinutes` を変えても、発行済みの期限は変わらない。期限ちょうどからは期限切れ
  - 復号は厳密に行う（`+`・`/`・`=`・末尾の余りのビットが 0 でないものは不正）。署名は定数時間で比べる
  - `SESSION_SIGNING_KEY` は 32〜256 文字の表示できる ASCII（`0x21`〜`0x7E`。例: `openssl rand -hex 32`）。合わない（未設定・空白や改行を含むなど）Worker は、`POST /api/sessions` を `503 sessions_disabled`、`/ws/sessions/{id}` を `404` にする
- `token` は乱数 16 バイトの base64url（22 文字、`^[A-Za-z0-9_-]{22}$`）。Worker と Durable Object はトークンそのものを保存せず、SHA-256 の 16 進の小文字（64 文字）を Durable Object に渡す。Durable Object は接続の `?token=` をこの形式で確かめてからハッシュし、定数時間で比べる

エラー:

| HTTP | `code` | 意味 | 画面の表示 |
|---|---|---|---|
| 400 | `bad_request` | 不正なステージ・本文（上の検証） | — |
| 403 | `forbidden_origin` | `Origin` がない・許可リストにない | — |
| 403 | `turnstile_failed` | Turnstile の確認に失敗 | 「もう一度お試しください」 |
| 429 | `rate_limited` | レート制限 | 「少し待ってからお試しください」 |
| 500 | `internal_error` | Worker の中で失敗した | — |
| 503 | `budget_exhausted` | 費用の上限に達した（G1） | 「本日の受付は終了しました」 |
| 503 | `sessions_disabled` | 管理者が受付を一括で止めている（G6）・受付の部品がそろっていない | 「ただいま受付を停止しています」 |

エラーの本文は `{ "code": "…", "message": "…" }` です（[共通の応答とエラー](#共通の応答とエラー)）。

### `GET /ws/sessions/{id}?token=…`

WebSocket にアップグレードします。Worker が `Origin` を許可リストで検査し、Durable Object がトークンを検証します。同じセッションに 2 本目の接続が来たら、古い接続を閉じます（再接続のため）。

- `{id}` は `[A-Za-z0-9._-]` の 1〜256 文字（`sessionId` の `s_<base64url>.<base64url>` はこの範囲に収まる）。パーセントエンコードした文字は含められない
- Worker は署名・期限・ホスト名を、暗号の計算と時計だけで確かめる（Durable Object・`CostGuard`・ネットワークを使わない）。期限を過ぎた・別のホストで発行した `sessionId` も `404`
- Worker はアップグレードの応答（`101`）を包み直さずにそのまま返し、WebSocket の電文を読まない。`token` は Worker では検査せず、Durable Object に渡す

Worker が返すエラー:

| HTTP | `code` | いつ |
|---|---|---|
| 403 | `forbidden_origin` | `Origin` がない・許可リストにない |
| 404 | `not_found` | `{id}` の文字の範囲が不正・形式が違う・署名の検証に失敗・期限を過ぎた・別のホストで発行した・接続の部品がない（区別しない。Durable Object を呼ばない） |
| 405 | `method_not_allowed` | `GET` 以外 |
| 426 | `upgrade_required` | `Upgrade: websocket` がない（大文字・小文字は区別しない） |

---

### `GET /api/admin/guards`・`PATCH /api/admin/guards`（管理用）

コストガード（[ADR-0018](./adr/0018-cost-guard.md)）の確認と切り替えです。中身は Durable Object `CostGuard`（[DesignDoc §12.3.1](./03-design-doc.md#1231-durable-object-costguard)）が持ちます。`Origin` は、あれば許可リストと照合します（なければ通す）。管理用の部品がない Worker（`COST_GUARD` のバインディングがない）は `404 not_found` を返します。

認証（Worker が行う。通るまで本文を読まず、`CostGuard` を呼ばない）:

- `Authorization: Bearer <ADMIN_TOKEN>` が必要です。`Bearer` の大文字・小文字は区別せず、`Bearer` とトークンの間の空白はちょうど 1 つです
- `ADMIN_TOKEN`（Worker のシークレット）は、32〜256 文字の表示できる ASCII（`0x21`〜`0x7E`。例: `openssl rand -hex 32`）です。未設定・空・31 文字以下・空白や改行を含むときは、管理用 API が無いのと同じ扱いで `404 not_found` を返します（`--secrets-file` に書いた値の末尾に改行が入らないように注意）
- トークンがない・書式が違う・一致しないときは `401 unauthorized` と `www-authenticate: Bearer` を返します。比較は両方の SHA-256 を定数時間で比べます

`GET` のレスポンス（`PATCH` の成功も同じ形で、変更後の値を返す）:

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
  "usage": { "todayUsd": 0.0195, "monthUsd": 0.0195, "activeGames": 3 },
  "unitCostUsd": { "lite": 0.0065, "standard-1": 0.04 },
  "recentChanges": [
    { "at": "2026-11-07T03:00:00.000Z", "change": { "budget.enabled": false }, "reason": "デモ当日のため一時的に外す" }
  ]
}
```

- `guards` は、設定 `GUARDS`（`cloudflare.config.ts`）の既定値に、管理用 API で保存した上書きを重ねたものです。`gameTime.enabled` と `signature` は常に `true` です
- `usage.todayUsd`・`monthUsd` は、今日・今月に**予約した**ゲームの見込みの費用の合計（USD）です。1 日・1 か月は **UTC** の暦で区切ります（Cloudflare の請求と同じ）。取り消したゲームは 0、精算したゲームは実際に動いた時間の額、まだのゲームは予約額で数えます
- `usage.activeGames` は、精算されていない予約のうち、`gameTime.maxMinutes` 分以内に予約したものの数です（目安の値）
- `unitCostUsd` は 30 分あたりの最悪の費用（USD）です。管理用 API では変えられません
- `recentChanges` は切り替えの記録の新しい順に最大 10 件です。`at` は ISO 8601（UTC）、`change` は下の「平らな形」です

`PATCH` のリクエスト（`Content-Type: application/json`、4096 バイトまで。変えたい項目だけを送る）:

```json
{ "budget": { "enabled": false }, "acceptGames": true, "reason": "デモ当日のため一時的に外す" }
```

| キー | 変えられる項目と値 |
|---|---|
| `budget` | `enabled`（真偽値）・`monthlyUsd`・`dailyUsd`（有限の数、0 以上 1000 以下。0 はすべての予約を断る） |
| `gameTime` | `maxMinutes`（整数、1〜60） |
| `turnstile` | `enabled`（真偽値） |
| `rateLimit` | `enabled`（真偽値）・`perIpPerMinute`（整数、1〜1000） |
| `acceptGames` | 真偽値。`false` で新しいゲームの受付を一括で止める（G6。遊んでいるゲームは続く） |
| `reason` | **必須**。前後の空白を除いて 1〜200 文字の文字列。記録に残る |

- 項目の値に `null` を送ると、その項目の上書きを消して `GUARDS` の既定値に戻します
- 次の項目は変更できません。値に関係なく `400 locked` を返します（ほかの誤りより先に判定する）: `signature`（G3）・`cpuMs`（再デプロイが必要）・`gameTime.enabled`・`gameTime.maxAlarms`（G2 のアラームの回数の上限は常に有効）
- 未知のキー・型や範囲の違い・`reason` がない・変える項目が 1 つもない・`Content-Type` が違う・4096 バイトを超える・壊れた JSON は `400 bad_request` です
- 記録の `change` は、変えた項目をドット区切りのキーにした平らな形（`{"budget.enabled": false, "acceptGames": true}`。金額は USD のまま）です
- 変更は次の要求からすぐに効きます。記録（いつ・誰が・何を・理由）は `CostGuard` に残ります（新しい 1000 件まで）
  - ただし、Worker の入り口が使う `turnstile.enabled`・`rateLimit.enabled`・`gameTime.maxMinutes` は、Worker が isolate ごとに 5 秒だけ使い回すので、最大 5 秒遅れて効きます（予算と `acceptGames` は使い回さず、すぐに効く）
  - `rateLimit.perIpPerMinute` の上書きは、表示と記録だけです。Worker の回数は Rate Limiting の束縛 `SESSION_RATE_LIMIT` の `limit`（デプロイのときの値）で決まり、再デプロイするまで変わりません

エラー:

| HTTP | `code` | いつ |
|---|---|---|
| 400 | `bad_request` | `PATCH` の本文が不正（上の検証） |
| 400 | `locked` | 変えられない項目を変えようとした |
| 401 | `unauthorized` | `Authorization` がない・違う |
| 404 | `not_found` | 管理用の部品がない・`ADMIN_TOKEN` が未設定や不正 |
| 500 | `internal_error` | `CostGuard` の呼び出しに失敗した |
| 503 | `guards_invalid` | 設定 `GUARDS` が未設定・不正（このとき `POST /api/sessions` は `503 sessions_disabled`） |

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
| `cmd.ferry.autoload` | `{ "ferryId": "f1", "to": 2 }` | おまかせ積み込み。港の手紙のうち `to` 宛てのものを、ゲームデザイン §3.4 の順に空きの数だけ積む。`to` への直接の航路がなければ `no_route`、積む手紙がなければ `letter_not_here`、満載なら `capacity_exceeded` | P0 |
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
| `ack` | `{ "cmdId": "c-12", "ok": true, "t": 61200 }` / `{ "cmdId": "c-12", "ok": false, "t": 61200, "code": "route_blocked", "message": "…" }` | コマンドの結果。`t` は適用したゲーム内時刻で、Durable Object が入力ログに記録するのに使う。失敗のときも `t`（そのときのゲーム内時刻）を入れる（後方互換なフィールドの追加、§8） |
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
      "departAt": 58000, "arriveAt": 67000, "readyAt": 68500, "cargo": [31, 32] },
    { "id": "f2", "name": "…", "capacity": 4, "state": "docked", "islandId": 3, "readyAt": 61500, "cargo": [] }
  ],
  "routesState": [ { "id": "1-2", "blockedUntil": null } ],
  "letters": [ /* 港と船の上にある Letter（§5.2）。P1 では中継倉庫も。配達済み・破棄は含めない */ ],
  "requests": [ /* 発生済みの Request（§5.3）。ID の順 */ ],
  "nodes": [ { "id": 1, "role": "leader", "term": 2, "lead": 1, "commit": 14, "applied": 14, "lastIndex": 15 } ],
  "islandsState": [ { "id": 1, "satisfaction": 64, "lastDeliveredAt": 60500 } ],
  "score": { "total": 520, "successes": 3, "target": 5, "criticalSecured": ["firework:a"] },
  "lastSeq": 41
}
```

`phase` の値: `intro`（`cmd.game.start` の前。ステージ紹介）/ `opening` / `sailing` / `ended`

`ferries[]` の注記（後方互換なフィールドの追加、§8）:

- `state` は `docked` / `sailing`
- `islandId` は `docked` のときの島
- `from`・`to` は `sailing` のときだけ。`departAt`・`arriveAt` は `docked` のとき `0`
- `readyAt` は、`docked` なら出航できるようになる時刻、`sailing` なら着いてから出航できる時刻（`arriveAt + dockMs`）

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

`status`: `received`（受付）/ `waiting`（送付待ち）/ `in_transit`（輸送中）/ `proposed`（審議中）/ `committed`（共和国で確定）/ `succeeded`（確定）/ `rejected`（予約失敗）。各値の判定は [DesignDoc §8.2](./03-design-doc.md) を参照

`result`: `null`、または `{ "status": "ok" | "rejected", "reason": "…" }`。依頼元の島の台帳の結果で、`succeeded`・`rejected` のときだけ値を持つ。`reason` は `rejected` のときだけで、台帳の理由（例: `already_reserved`・`full`）

```json
{ "status": "rejected", "reason": "already_reserved" }
```

`result` は `null` か文字列です（文字列の中身は T-202 が決める）。

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
| `request.created` | `request`（Request 全体。`status` は `received`） | 依頼の発生 |
| `request.updated` | `id`, `status`, `expired`, `result`（§5.3） | 状態の変化（同じステップ内の変化はまとめる） |
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

