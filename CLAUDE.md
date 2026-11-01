# CLAUDE.md — 開発者・実装エージェント向けの規約

## まず読むもの

1. `docs/01-requirements.md`（要件 ID と優先度）
2. `docs/03-design-doc.md`（設計）
3. `docs/08-implementation-plan.md`（タスク一覧）
4. 迷ったら `docs/adr/` と `docs/09-assumptions-and-open-questions.md`

## 絶対に守ること

- **Raft のアルゴリズムを変更しない。** `server/third_party/etcd-raft` への変更は ADR-0005 の乱数のパッチだけ。それ以外の変更が必要に見えたら、ADR を書いて相談する
- **ゲーム側で Leader や Commit の結果を決めない。** Raft の出力（`Ready`）を観察するだけ
- **Raft のメッセージを書き換えない。** 港キューでの合流・破棄（ネットワークでの欠落と同じ扱い）だけが許される
- **`server/internal/sim` 以下を決定的に保つ。** `time.Now`、goroutine、チャネル、グローバルな `math/rand`、map の走査順への依存、スコアや判定での浮動小数点を使わない（`forbidigo` で検出）
- **Ready の処理順序**は「Append → SetHardState → 送信 → 適用 → Advance」。`cluster.Node.processReady` 以外で Ready を扱わない
- ゲームの数値はコードに直接書かず、ステージ定義（`data/stages/*.yaml`）に置く

## 仕様の変更

- 仕様を変えるときは、先に `docs/` を更新し、同じ PR でコードを変える
- 新しい技術判断をしたら `docs/adr/` に ADR を追加する（`docs/adr/template.md`）
- PR やコミットには、関係する要件 ID（`FR-…`）やタスク ID（`T-…`）を書く

## コマンド（M0 で整備する予定）

```bash
make test       # Go と web のテスト
make lint       # golangci-lint・ESLint・型チェック
make gen        # Go のプロトコルの型から TS の型を生成
make sync-data  # data/ を server/internal/stage/embedded/ にコピー
make dev        # サーバーと Vite を起動
```

## etcd-io/raft v3.7 の注意点

- `raftpb` は protobuf-go 製。フィールドはポインタなので `m.GetTerm()` などのゲッターで読む
- `Ready.Messages` は `[]*pb.Message`。港に置く前に `proto.Clone` で複製する
- `raft.Status` は `*pb.HardState` を埋め込んでいる。`st.Term` はポインタなので `st.HardState.GetTerm()` を使う
- Web 上のサンプル（v3.5 以前）とは API の書き方が違う
