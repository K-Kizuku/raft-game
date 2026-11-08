# ADR-0025: ステージ定義は、ビルドタグ `embeddata` を付けたときだけバイナリに埋め込み、ローカルでは `DATA_DIR` で読む

- 状態: Proposed
- 日付: 2026-11-07
- 決めた人: オーナーの判断待ち（D-2）
- 関連: T-302（#38）・T-306（#42）、[アーキテクチャ §6](../02-architecture.md#6-ディレクトリ構成モノレポ)、[データ仕様 §5.6](../07-data-spec.md#56-読み込みinternalstage)、[DesignDoc §12.1](../03-design-doc.md#121-役割分担)、[ADR-0019](./0019-parallel-local-development.md)

## 背景

Go のゲームサーバー（`server/cmd/server`）は、起動時に `data/islands.json` と `data/stages/*.yaml` を読む。アーキテクチャ §6 は「`data/` を `server/internal/stage/embedded/` にコピーして（`make sync-data`）`go:embed` で取り込む。コピー先は `.gitignore` に入れる」としていた。

- Go の `go:embed` は、パッケージのディレクトリの外（親）を参照できない。`server/internal/stage` から `data/` を直接は埋め込めない
- コピー先は `.gitignore` に入れているので、コピーしていない作業ツリーと CI には存在しない
- そのまま `//go:embed embedded` を書くと、コピーしていない作業ツリーと CI で `go build`・`go vet`・`go test` が `pattern embedded: no matching files found` で失敗する。CI のワークフロー（保護されたパス）を変えずに済ませたい

## 検討した選択肢

### 案 a: ビルドタグ `embeddata` を付けたときだけ埋め込む

- 長所: タグなしのビルド（`make test`・`make lint`・CI）はコピーを要らず、ワークフローを変えずに済む。本番のイメージはデータを同梱した 1 つのバイナリになる。`DATA_DIR` を指定すればタグなしでも読める
- 短所: ビルドの方法が 2 つになる（`embed.go` と `embed_none.go`）。タグを付け忘れたバイナリは `DATA_DIR` なしでは起動しない（起動時に `make sync-data` を案内して終了コード 1 で止まる）

### 案 b: 埋め込まず、`DATA_DIR` とイメージへのファイルのコピーだけにする

- 長所: 仕組みが 1 つで分かりやすい
- 短所: Dockerfile でデータのコピーを忘れると、本番で起動しない。バイナリだけを配る使い方（デモの保険）でディレクトリも一緒に運ぶ必要がある

### 案 c: コピー先（`server/internal/stage/embedded/`）をコミットする

- 長所: タグも `DATA_DIR` も要らない
- 短所: `data/` と二重に管理することになり、食い違いが起きる。食い違いを見つける検査がさらに要る

## 決定

案 a。`server/internal/stage/embed.go`（`//go:build embeddata`）が `//go:embed all:embedded` で埋め込み、`stage.Embedded()` で返す。`embed_none.go`（`//go:build !embeddata`）は常に `(nil, false)` を返す。`all:` は、`stages/` に `.gitkeep` しかないときにもディレクトリを埋め込むため。

- `make sync-data` が `data/` の `islands.json` と `stages/` の直下の通常のファイルを `server/internal/stage/embedded/` にコピーする（コピー先の末尾が `/embedded` でなければ誤りにして、`rm -rf` の誤爆を防ぐ）
- `server` は `--data`（`DATA_DIR`）があればそのディレクトリを、なければ `stage.Embedded()` を読む（`server.OpenData`）
- `make dev` は `DATA_DIR` で作業ツリーの `data/` を読む（コピーは要らない。ステージ定義を書き換えたら再起動で反映される）

## 結果（この判断によって起きること）

- 良いこと: `make test`・`make lint`・CI はタグなしのままで、ワークフローを変えずに済む。本番（T-306 の Dockerfile）は `make sync-data` の後に `go build -tags embeddata` で 1 つのバイナリにできる
- 悪いこと・受け入れるリスク: 埋め込みの経路（`embed.go`）は、タグを付けたときだけビルド・テストされる（`go test -tags embeddata -run Embedded ./internal/stage/`）。CI では通常は通らないので、T-306 のイメージのビルドで確かめる

## 差し替えるときの影響範囲

- `server/internal/stage/embed.go`・`embed_none.go`・`embed_test.go`
- `Makefile` の `sync-data`、`scripts/tests/test_makefile.py`
- `server/internal/server/catalog.go` の `OpenData`
- T-306 の Dockerfile、アーキテクチャ §6、データ仕様 §5.6

## 見直すきっかけ

- T-306 で、イメージにデータのディレクトリをコピーする（案 b）ほうが簡単だと分かった
- CI でタグつきのビルドを常に確かめたくなった（ワークフローに `make sync-data` を足すなら、タグをやめて常に埋め込む案も取れる）
