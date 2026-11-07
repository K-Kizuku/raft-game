# ADR-0022: YAML の読み込みに go.yaml.in/yaml/v3 を使い、未知のキーを拒否する

- 状態: Accepted（2026-11-08 に #132 でプロダクトオーナーが承認）
- 日付: 2026-11-07
- 決めた人: Claude（T-205 / #33）
- 関連: FR-DATA-01・FR-DATA-03、[ADR-0013](./0013-open-data-build-time-etl.md)、[データ仕様 §2.6・§3.4・§5](../07-data-spec.md)、[実装計画 T-201・T-205・T-206](../08-implementation-plan.md)

## 背景

Go のサーバーは、次の YAML を読む。

- `data/islands-select.yaml`（ETL `server/cmd/islandsdata` の設定。T-205）
- `data/curated/*.yaml`（計画書からの抜き出し。T-205 で形式と検証を作り、T-206 で中身を書く）
- `data/stages/*.yaml`（ステージ定義。T-201 の `internal/stage` が読む）

どれも人が手で書くファイルなので、キーの書き間違い（`designatedarea` など）が黙って無視されると、値が抜けたまま島のデータやステージが作られてしまう。そのため、次のことが必要になる。

- 未知のキー・重複したキー・型の違いをエラーにできる
- エラーに行番号が出る（どの島・どの抜き出しかを示すため）
- 標準ライブラリには YAML の読み込みがないので、外部のライブラリを 1 つだけ選び、ETL と `stage` で共有する

## 検討した選択肢

### 案 A: `go.yaml.in/yaml/v3`

- 長所: `gopkg.in/yaml.v3` と同じ API（`Decoder.KnownFields(true)`・`yaml.Node`）。YAML の組織（github.com/yaml/go-yaml）が引き継いで保守している。重複したキーは既定でエラーになり、エラーに行番号が付く。依存がない
- 短所: 小数を整数のフィールドに読むと黙って切り捨てる（`1.5` → `1`）。整数だけを受け付けたい欄では、タグ（`!!int`）を確かめる小さな型が要る

### 案 B: `gopkg.in/yaml.v3`

- 長所: 案 A と同じ API。広く使われている
- 短所: 元のリポジトリ（go-yaml/yaml）がアーカイブされ、更新が止まった。案 A がその後継

### 案 C: `github.com/goccy/go-yaml`

- 長所: 活発に開発されている。`yaml.Strict()` で未知のキーを拒否できる。エラーの表示が詳しい
- 短所: API が `yaml.v3` と違う。依存が増える。`yaml.Node` に相当するものの扱いが違い、行番号から要素を探す処理を書き直す必要がある

### 案 D: `sigs.k8s.io/yaml`

- 長所: YAML を JSON に変えてから `encoding/json` で読むので、JSON のタグだけで済む。`UnmarshalStrict` で未知のキーを拒否できる
- 短所: JSON に変える段階で行番号が失われ、エラーの場所が分からない。内部で別の YAML のライブラリに依存する

## 決定

案 A。`go.yaml.in/yaml/v3`（着手時の最新の v3.0.6）を `server/go.mod` に足す。

- 読むときは必ず `yaml.NewDecoder` に `KnownFields(true)` を付ける（未知のキーをエラーにする）
- 整数だけを受け付ける欄（`format`・抜き出しの `value`）は、タグが `!!int` のスカラーだけを受け付ける型で読む（案 A の短所への対策）
- T-201（`internal/stage`）も同じライブラリと同じ方針で `data/stages/*.yaml` を読む。`islandsdata` と `stage` は型を共有しない（形式の正本は `docs/07-data-spec.md`）

## 結果（この判断によって起きること）

- 良いこと: 手で書く YAML の誤りが、生成やステージの読み込みの時点でエラーになる。ETL とサーバーで YAML の扱いがそろう
- 悪いこと・受け入れるリスク: 外部の依存が 1 つ増える。小数の切り捨てのように、ライブラリの既定の動きに合わせた対策をコードに持つ必要がある

## 差し替えるときの影響範囲

- `server/go.mod`・`server/go.sum`
- `server/cmd/islandsdata`（`yamlutil.go` の読み込みと、エラーの行番号から島・抜き出しの `id` を探す処理）
- `server/internal/stage`（T-201）
- データ仕様 §2.6・§3.4 の「未知のキー・重複したキー・型の違いはエラー」の記述

## 見直すきっかけ

- `go.yaml.in/yaml/v3` の保守が止まる、または v4 が安定版になる
- YAML の形式を JSON Schema などで検証する方針に変える
- ステージ定義の読み込みで、行番号よりも詳しいエラーの表示（列の位置など）が必要になる
