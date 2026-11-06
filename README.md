# 合意、乗船中。— 離島Raft共和国

> その手紙が届かないと、島のお祭りが始まらない。

日本の離島を舞台にした、フェリー配送パズルゲームです。プレイヤーは郵便船の船長として島々の手紙を運びます。その手紙の正体は、本物の分散合意アルゴリズム Raft（[`etcd-io/raft`](https://github.com/etcd-io/raft)）が送受信するメッセージです。**船を止めると、分散合意も止まります。**

- ハッカソンのテーマ: オープンデータ × 推し（推し: 分散システム・分散合意アルゴリズム）
- オープンデータ: 国土交通省 Project LINKS アイディアソン公開データ（2026年度）「離島に関するデータ」（離島振興対策実施地域一覧・離島振興計画書 ほか。政府標準利用規約 第 2.0 版）
- 舞台: 瀬戸内（小豆島・豊島・直島）、五島列島（福江島・奈留島・中通島）、伊豆諸島（大島・利島・三宅島・御蔵島・八丈島・青ヶ島）、全国の 7 島

## 状態

**設計段階**（コードはまだありません）。ドキュメントは [`docs/`](./docs/README.md) にあります。計画とタスクは GitHub の Issue で管理します。

公開予定の URL: `https://raft.kizuku-hackathon.work`（デモ用は `https://raft-demo.kizuku-hackathon.work`）

| ドキュメント | |
|---|---|
| [企画書](./docs/00-proposal.md) | 原文 |
| [要件定義書](./docs/01-requirements.md) | 何を作るか |
| [アーキテクチャ](./docs/02-architecture.md) | 全体の構成図 |
| [DesignDoc](./docs/03-design-doc.md) | どう作るか |
| [ゲームの進行表](./docs/05-game-progression.md) | プレイヤーが何をどの順で体験するか |
| [ADR](./docs/adr/README.md) | 技術判断の記録 |
| [判断の記録](./docs/09-assumptions-and-open-questions.md) | 企画書の段階で決めたことと、その後の確認の理由と決定 |
| [実装計画](./docs/08-implementation-plan.md) | 次にやること |

## 技術構成

| 領域 | 技術 |
|---|---|
| フロントエンド | React 19 + TypeScript + PixiJS v8 + Vite |
| バックエンド | Go 1.26、`go.etcd.io/raft/v3` v3.7.0（乱数源を注入するパッチ付き） |
| 通信 | WebSocket（JSON） |
| インフラ | Cloudflare（Workers ＋ Durable Objects ＋ Containers ＋ R2）。構成は Cloudflare CLI（`cf`）だけで管理。コストガードで追加料金を月 30 USD までに抑える |

## リポジトリ構成（予定）

```text
server/   Go のゲームサーバーとシミュレーションのコア（Cloudflare Containers で動く）
web/      ブラウザのクライアント、Worker と Durable Object、cloudflare.config.ts
data/     オープンデータの原本・島データ・ステージ定義
docs/     ドキュメント
```

## ライセンス

[Apache License 2.0](./LICENSE)。同梱する OSS・オープンデータ・生成した素材の表示は [`NOTICE`](./NOTICE) にあります。
