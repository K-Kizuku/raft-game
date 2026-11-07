# ADR-0024: Worker の IP ごとのレート制限に、Workers の Rate Limiting の束縛を使う

- 状態: Proposed
- 日付: 2026-11-07
- 決めた人: 実装エージェント（T-310、#46）。プロダクトオーナーのレビュー待ち
- 関連: FR-COST-04、NFR-07・NFR-15、[ADR-0016](./0016-infrastructure-cloudflare-with-cf-cli.md)、[ADR-0018](./0018-cost-guard.md)（G4）、[DesignDoc §12.5・§16](../03-design-doc.md)、[プロトコル §2](../06-protocol.md)

## 背景

コストガード G4（[ADR-0018](./0018-cost-guard.md)）は、`POST /api/sessions` を IP ごとに 1 分 10 回までに抑える。これはゲームの作成（Durable Object とコンテナ）の手前で効く、安くて速い入り口の制限である必要がある。

- 数えるために Durable Object を呼ぶと、制限したい要求そのものが Durable Object の要求（課金の対象）を増やす
- `CostGuard`（全体で 1 つ）で数えると、すべての作成の要求が 1 つの Durable Object に集まり、連打のときに詰まる
- Worker の isolate は拠点ごと・時間ごとに別々に作られて消えるので、isolate のメモリでは数え続けられない
- 回数は `GUARDS.rateLimit.perIpPerMinute`（既定 10）と同じ値にしたい。IPv6 は利用者が /64 の中で自由にアドレスを変えられる

## 検討した選択肢

### 案 A: Workers の Rate Limiting の束縛（`SESSION_RATE_LIMIT`）

`cloudflare.config.ts` に `{ type: "rate-limit", namespace, simple: { limit: 10, period: 60 } }` の束縛を置き、Worker は `limit({ key })` を呼ぶだけにする。キーは IPv4 はそのまま、IPv6 は先頭の 4 グループ（/64）。

- 長所: 追加の Durable Object の要求がなく、速い（拠点の中で完結する）。Worker の中で回数を数えるコードが要らない。`cf` の構成で管理できる
- 短所: Cloudflare の拠点ごとに別々に数える近似の値で、厳密な上限ではない（拠点をまたげば、合計で 10 回を超えられる）。回数（`limit`・`period`）はデプロイのときに決まり、管理用 API の `rateLimit.perIpPerMinute` の上書きでは変わらない

### 案 B: IP ごとの Durable Object で数える

IP（/64）ごとに `idFromName` で Durable Object を作り、その中で数える。

- 長所: 全体で厳密に数えられる。回数を `CostGuard` の設定から読めば、再デプロイなしに変えられる
- 短所: 1 回の作成の要求ごとに Durable Object の要求が 1 つ増える。でたらめな IP からの連打で Durable Object が増える（G3 で防ぎたいことと同じ問題を、別の場所で作る）。遅くなる

### 案 C: `CostGuard` で数える

`CostGuard` の SQLite に IP ごとの回数を保存する。

- 長所: 全体で厳密に数えられ、設定と同じ場所にある
- 短所: すべての作成の要求が全体で 1 つの Durable Object に集まり、連打のときに予約（G1）と管理用 API まで詰まる。帳簿の表が回数の行で大きくなる

### 案 D: isolate のメモリで数える

- 長所: 依存も束縛も要らず、最も速い
- 短所: isolate は拠点ごと・要求ごとに別になりうるうえ、いつでも消えるので、ほとんど数えられない。制限として意味がない

## 決定

案 A を選ぶ。G4 の目的は「安く・速く、明らかな連打を止める」ことで、厳密さはゾーンの WAF のレート制限（T-606）と、G1（費用の上限）・Turnstile が補う。Durable Object の要求を増やさないことを優先する。

- 束縛がない・`limit` が関数でない・例外・結果が `{ success: true }` でないときは、拒否する側（`429 rate_limited`）に倒す
- `rateLimit.enabled`（`CostGuard` の設定）が `false` なら束縛を呼ばない
- `CF-Connecting-IP` がない・読めない要求は、全部でキー `unknown` を共有する（本番では必ずある）

## 結果（この判断によって起きること）

- 良いこと: 作成の要求の手前で、Durable Object に触れずに連打を止められる。実装は `web/worker/security/rate-limit.ts` の小さな関数で済む
- 悪いこと・受け入れるリスク: 拠点をまたいだ大量の作成は、この制限だけでは止まらない（WAF と G1 で受け止める）。`rateLimit.perIpPerMinute` を管理用 API で変えても、Worker の回数は再デプロイまで変わらない（プロトコル §2 に書く）

## 差し替えるときの影響範囲

- `web/worker/security/rate-limit.ts`（`checkSessionRateLimit`・`rateLimitKey`）とそのテスト
- `web/cloudflare.config.ts`（束縛 `SESSION_RATE_LIMIT`。T-601）
- [プロトコル §2](../06-protocol.md) の `POST /api/sessions` と `/api/admin/guards` の説明、[DesignDoc §12.5](../03-design-doc.md) の表
- 案 B・C にするなら、Durable Object の要求の費用を [ADR-0018](./0018-cost-guard.md) の見積もりに足す

## 見直すきっかけ

- 拠点をまたいだ大量の作成（同じ IP や /64 からの、拠点ごとの上限を超える作成）がログや帳簿で見えたとき
- 回数を再デプロイなしに変える必要が出たとき（デモの当日に緩めたい、など）
- Rate Limiting の束縛の仕様（拠点ごとの近似、`period` の選択肢）が変わったとき
