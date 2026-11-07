# ADR-0014: モノレポにし、WS の型は Go から生成する

- 状態: Accepted（2026-11-06 にプロダクトオーナーが承認）
- 日付: 2026-11-01
- 決めた人: Claude
- 関連: [プロトコル仕様](../06-protocol.md)、[アーキテクチャ §6](../02-architecture.md)

## 背景

サーバー（Go）とクライアント（TypeScript）が、WebSocket のメッセージの型を共有する必要がある。リポジトリは 1 つ（`K-Kizuku/raft-game`）が用意されている。

## 検討した選択肢

### 案 A: モノレポ。メッセージは JSON。Go の構造体を正本とし、`tygo` で TypeScript の型を生成する

- 長所: 型の正本が 1 か所。JSON なのでエンジニアモードやデバッグでそのまま読める。ツールが軽い
- 短所: 実行時の検証はしない（サーバー側では Go のデコードで、クライアント側では信頼する）

### 案 B: Protocol Buffers（`buf`）で両方の型を生成する

- 長所: 型の安全性・互換性の管理が強い。通信量が小さい
- 短所: ツールチェーンが重い。エンジニアモードで中身を見るのに変換が要る。ハッカソンの規模には過剰

### 案 C: 手で両方に型を書く

- 長所: ツールが要らない
- 短所: ずれる

### 案 D: OpenAPI / JSON Schema を正本にする

- 長所: 言語に依存しない
- 短所: WebSocket のメッセージの表現には向かない。生成のツールが増える

## 決定

案 A。`make gen` で `server/internal/protocol` から `web/src/protocol/` を生成し、生成物もコミットする（CI で生成し直して差分がないことを確認する）。Cloudflare の Worker と Durable Object（`web/worker/`、TypeScript）も、同じ生成された型を使う（2026-11-06 の追記）。

ディレクトリ構成は [アーキテクチャ §6](../02-architecture.md) のとおり。

2026-11-07 の追記（T-301）: `tygo` は v0.2.21（MIT License）を使い、`Makefile` の `TYGO_VERSION` で版を固定して `go -C server run github.com/gzuidhof/tygo@<版>` で実行する。`server/go.mod` には入れない（`tygo` の依存を `server` のモジュールの依存の解決に混ぜないため）。設定は `server/tygo.yaml` で、`enum_style: union`（TS の `enum` を使わず、定数と `typeof` のユニオン型にする。`erasableSyntaxOnly` のため）。`sim` の型は `protocol` の型に置き換えず、`server/internal/protocol/simconv` で変換し、両者の JSON が一致することをテストで確かめる。CI での差分の検査は `make gen-check` で行う（ワークフローへの取り込みは人の作業）。

## 結果

- 良いこと: 型のずれが CI で検出される。1 つの PR でサーバーとクライアントを同時に変えられる
- 悪いこと: Go の型の表現力（ユニオン型がない）に TS の型が引きずられる → イベントの `type` を判別子にして、TS 側で判別可能なユニオン型を手で 1 か所だけ組み立てる

## 差し替えるときの影響範囲

`server/internal/protocol`、`web/src/protocol`、`Makefile`。

## 見直すきっかけ

- 通信量が問題になったとき、またはプロトコルの互換性を厳密に管理する必要が出たとき → 案 B
