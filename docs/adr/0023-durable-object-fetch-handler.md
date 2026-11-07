# ADR-0023: Durable Object は `fetch` のハンドラーで呼び、`cloudflare:workers`・RPC を使わない

- 状態: Proposed
- 日付: 2026-11-07
- 決めた人: 実装エージェント（T-309、#45）。プロダクトオーナーのレビュー待ち
- 関連: FR-COST-01・06・07、NFR-15、[ADR-0016](./0016-infrastructure-cloudflare-with-cf-cli.md)、[ADR-0018](./0018-cost-guard.md)、[DesignDoc §12.3.1](../03-design-doc.md)、[プロトコル §2](../06-protocol.md)

## 背景

T-309 で Durable Object `CostGuard`（コストガードの帳簿と設定）を作る。DesignDoc §12.3.1 の概略は `extends DurableObject` と RPC のメソッド（`reserve`・`settle` など）の形だった。しかし、

- `DurableObject` の基底クラスは `import { DurableObject } from "cloudflare:workers"` で読む。このモジュールは workerd の中にしかなく、テストを動かしている Node の Vitest（`environment: "node"`。workerd を起動しない）では読めない
- 型も `@cloudflare/workers-types` か、独自の `declare module "cloudflare:workers"` が要る。前者は依存を足すことになり（`web/package.json` と `pnpm-lock.yaml` は変えない約束）、後者は `GameSession`（T-308）が同じ宣言を書くと衝突しやすい
- コストガードは「拒否する側に倒れる」ことをテストで確かめたいので、Durable Object のクラスを Node のテストでそのまま組み立てたい

workerd は、`DurableObject` を継承しないクラスでも、`constructor(ctx, env)` と `fetch(request)` を持てば Durable Object として動かし、`ctx.storage`（SQLite のクラスなら `ctx.storage.sql`）を渡す。

## 検討した選択肢

### 案 A: RPC と `declare module "cloudflare:workers"`

`extends DurableObject` で RPC のメソッドを公開し、型は自前の `declare module` で書く。テストでは `cloudflare:workers` を Vitest の別名で偽のモジュールに差し替える。

- 長所: RPC の引数と戻り値に TypeScript の型が付く。DesignDoc の概略のまま
- 短所: `vitest.config.ts` に別名の設定が要る（変更禁止の範囲）。`declare module` が T-308 の宣言と衝突しやすい。偽の基底クラスと本物の挙動が離れる

### 案 B: `@cloudflare/workers-types` と Vitest の workerd のプールを足す

- 長所: 本物の workerd でテストでき、型も公式のもの
- 短所: 依存を足す（このタスクでは変えない約束）。テストが重くなり、ほかのテストの仕組みと分かれる。プールの導入は専用の判断が要る

### 案 C: `fetch` のハンドラーで内部の API を受ける

`CostGuard` は普通のクラスにし、`fetch(request)` で `POST /reserve` などの内部の API を JSON で受ける。Durable Object まわりの型は、使う分だけの構造の型（`web/worker/admin/cf-types.ts`）に書く。呼ぶ側は `web/worker/admin/client.ts` の `CostGuardClient` で、応答の形を確かめる。

- 長所: 依存を足さない。`new CostGuard(偽の ctx, env)` を Node の Vitest でそのままテストできる（SQLite は `node:sqlite`）。`cloudflare:workers` を使わないので、T-308 と型の宣言が衝突しない
- 短所: RPC の型の安全さを失う。代わりに、呼ぶ側と受ける側の両方で JSON を検証する手間が増える。`fetch` 1 回ごとに `Request` と JSON の組み立てが入る（呼ぶのはゲームの開始と終了だけなので、負荷は小さい）

## 決定

案 C。Durable Object は `fetch` のハンドラーで呼び、`extends DurableObject`・RPC・`import "cloudflare:workers"` を使わない。

- 内部の API の形は [DesignDoc §12.3.1](../03-design-doc.md) の表のとおり。受ける側（`cost-guard.ts`）も呼ぶ側（`admin/client.ts`）も、JSON を `unknown` で受けて形を確かめる
- typecheck と `cf build` は、このクラスを `web/worker/index.ts` から export し、`cloudflare.config.ts` に `exports.durableObject({ storage: "sqlite" })` を足した作業用のコピーで通ることを、T-309 の具体化のときに確認した。本番での動作の確認は T-601・T-605 で行う

## 結果（この判断によって起きること）

- 良いこと: コストガードの帳簿・認証・配線を、workerd なしの単体テストで確かめられる。依存が増えない
- 悪いこと・受け入れるリスク: 内部の API の形が変わったとき、型の検査では気づけない（クライアントの検証とテストで気づく）。workerd が `DurableObject` を継承しないクラスの扱いを変えたら、動かなくなるおそれがある

## 差し替えるときの影響範囲

- `web/worker/cost-guard.ts`（クラスの形）と `web/worker/admin/client.ts`（呼び出し）
- `web/worker/admin/cf-types.ts`（構造の型。RPC にするなら不要になる）
- T-308 が同じ形にした場合は `web/worker/game-session.ts`
- [DesignDoc §12.3.1](../03-design-doc.md) の内部の API の表

## 見直すきっかけ

- Vitest で workerd を使うテストの仕組み（`@cloudflare/vitest-pool-workers` など）を入れたとき
- `@cloudflare/workers-types` か、`cloudflare:workers` の型を依存に足したとき
- workerd が、`DurableObject` を継承しないクラスを Durable Object として扱わなくなったとき
