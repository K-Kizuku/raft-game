# ドキュメント一覧

「合意、乗船中。— 離島Raft共和国」の企画・要件・設計のドキュメントです。

## 読む順番

| # | ドキュメント | 内容 | こんな人に |
|---|---|---|---|
| 0 | [00-proposal.md](./00-proposal.md) | 企画書（原文） | 全員 |
| 1 | [01-requirements.md](./01-requirements.md) | **要件定義書** — 目的・スコープ・機能要件（ID と優先度つき）・非機能要件・検証要件 | 全員 |
| 2 | [02-architecture.md](./02-architecture.md) | **アーキテクチャ図** — コンテキスト・コンテナ・コンポーネント・シーケンス・デプロイ構成・ディレクトリ構成 | 実装者 |
| 3 | [03-design-doc.md](./03-design-doc.md) | **DesignDoc** — 時間モデル・Raft との統合・Ferry Transport・台帳・依頼の処理・決定性・テスト戦略 | 実装者 |
| 4 | [04-game-design.md](./04-game-design.md) | ゲームデザイン仕様 — 手紙・フェリー・依頼・スコア・イベント・画面 | 全員 |
| 5 | [05-game-progression.md](./05-game-progression.md) | **ゲームの進行表** — ステージ一覧・ラウンドの流れ・ステージごとの時刻表・チュートリアル・デモ台本 | 全員 |
| 6 | [06-protocol.md](./06-protocol.md) | 通信プロトコル仕様（HTTP / WebSocket） | 実装者 |
| 7 | [07-data-spec.md](./07-data-spec.md) | データ仕様 — オープンデータ・島データ・ステージ定義 | 実装者・データ担当 |
| 8 | [08-implementation-plan.md](./08-implementation-plan.md) | 実装計画 — マイルストーン・タスク一覧・最初の手順 | 実装者 |
| 9 | [09-assumptions-and-open-questions.md](./09-assumptions-and-open-questions.md) | **判断の記録と未決事項** — 24 項目の判断の理由・決定・変更箇所 | プロダクトオーナー |
| 10 | [10-glossary.md](./10-glossary.md) | 用語集（ゲームの言葉 ⇔ Raft の言葉） | 全員 |
| — | [adr/](./adr/README.md) | **ADR** — 技術判断の記録（17 件。うち 2 件は置き換え済み） | 全員 |

## プロダクトオーナーの方へ

2026-11-06 に、企画書の段階で仮に決めていた 24 項目をすべて確認していただき、その結果を全ドキュメントに反映しました。

1. 何をどう決めたかは [判断の記録](./09-assumptions-and-open-questions.md) の表にあります。仮決めから変わった項目には 🔁 が付いています
2. 大きな技術判断は [ADR](./adr/README.md) に、検討した選択肢と長所・短所を残しています
3. 判断を変えたくなったときの手順は [adr/README.md](./adr/README.md#判断を差し替える手順) にあります
4. 残っている [未決事項](./09-assumptions-and-open-questions.md#3-未決事項オーナーに確認したいこと)（Q-01・Q-03 の一部・Q-04・Q-06〜Q-08・Q-10・Q-12・Q-13）に回答いただけると、計画をさらに具体化できます

## ドキュメントの約束

- 仕様を変えるときは、**先にドキュメント（または ADR）を更新し、同じ PR でコードを変える**
- 要件には ID（`FR-…`・`NFR-…`・`V-…`）があります。PR やコミットで参照してください
- ゲームの数値は、ドキュメントでは既定値を示し、実際の値はステージ定義（`data/stages/*.yaml`）で管理します
- 図は Mermaid で書きます（GitHub 上でそのまま表示されます）
