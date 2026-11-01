# データ仕様（オープンデータ・島データ・ステージ定義）

| 項目 | 内容 |
|---|---|
| 版 | v0.1（仮決め版） |
| 状態 | Draft。**提供データの実ファイルを確認したら §2 を更新すること** |
| 関連 | [ADR-0013](./adr/0013-open-data-build-time-etl.md) / [要件 FR-DATA](./01-requirements.md#410-オープンデータfr-data) |

---

## 1. 方針

1. オープンデータの数値は、元の意味を保ったまま使う（人口は人口として表示し、加工した値は加工したと明示する）
2. データにない値（航行時間・積載量・イベントの発生率など）はゲーム用のパラメータとして作り、**作ったものだと分かるように**する
3. すべての値に**出典区分**を付ける

| 出典区分 | 意味 | 画面での表示 | 例 |
|---|---|---|---|
| `fact` | オープンデータの値そのまま | 「出典：国土交通省『…』」 | 人口、面積、所属自治体 |
| `derived` | オープンデータから計算・要約したもの | 「国土交通省『…』をもとに作成」 | 人口から決めた依頼の発生の重み、計画書の記述の要約 |
| `creative` | ゲームのための創作 | 「ゲーム上の設定」 | 航行時間、積載量、キャラクターのセリフ、マップ上の配置 |

---

## 2. 入力データ

| # | データ | 使う項目 | 取り込み方 | 優先 |
|---|---|---|---|---|
| D1 | 離島振興対策実施地域一覧 | 島名、所属自治体、都道府県、人口、面積（あれば島のコード） | ETL（`cmd/islandsdata`）で機械的に取り込む | P0 |
| D2 | 離島振興計画書 | 海上交通（航路・港）、気象（冬の季節風・台風など）、地域の特徴 | 人手で読み、要約とページ番号を YAML に書く（`data/curated/`） | P0（ステージに出す島のみ） |
| D3 | 離島活性化交付金等事業計画書 | 事業の目的・課題 | 人手で読み、発展ミッションの案にする | P2 |

**未確認事項**: 提供されるファイルの形式（CSV / Excel / PDF）、列名、データの年次、利用規約（政府標準利用規約 第2.0版 = CC BY 4.0 互換を想定）。ハッカソンの資料（「2026年度 オープンデータ紹介スライド」）で確認したら、この節と `data/raw/README.md` を更新します（[Q-02](./09-assumptions-and-open-questions.md#q-02)）。

### 2.1 `data/raw/` の置き方

```text
data/raw/
├── README.md                 # 各ファイルの出典 URL・取得日・利用規約・加工の有無
├── ritou-list-2026.xlsx      # D1 の原本（ライセンス上、再配布できる場合のみコミット）
├── ritou-list-2026.csv       # D1 を CSV にしたもの（Excel の場合は人手で変換してよい）
└── plans/                    # D2 の PDF（再配布できない場合は URL だけ README に書く）
```

### 2.2 ETL（`server/cmd/islandsdata`）

```bash
go run ./cmd/islandsdata \
  --list ../data/raw/ritou-list-2026.csv \
  --curated ../data/curated \
  --select ../data/islands-select.yaml \
  --out ../data/islands.json
```

- `islands-select.yaml` に、ゲームで使う島の ID と、CSV の行を特定するキー（島名 + 自治体名）を書く。CSV にない島があればエラーにする
- 出力の JSON はキーの順序を固定し、差分が読みやすいようにする。**生成した `islands.json` はコミットする**（実行時に ETL は動かさない）
- 同じ入力からは同じ出力になること（テストで確認）

---

## 3. 島データ `data/islands.json`

```json
{
  "format": 1,
  "generatedAt": "2026-11-01",
  "sources": {
    "mlit-ritou-list-2026": {
      "title": "離島振興対策実施地域一覧",
      "publisher": "国土交通省",
      "url": "TBD",
      "license": "TBD（政府標準利用規約 第2.0版を想定）",
      "retrievedAt": "TBD"
    }
  },
  "islands": [
    {
      "id": "oki-dogo",
      "name": "島後",
      "kana": "どうご",
      "prefecture": "島根県",
      "municipality": "隠岐の島町",
      "group": "隠岐諸島",
      "population": { "value": null, "asOf": "TBD", "provenance": "fact", "source": "mlit-ritou-list-2026" },
      "areaKm2":    { "value": null, "provenance": "fact", "source": "mlit-ritou-list-2026" },
      "requestWeightPermille": { "value": null, "provenance": "derived", "note": "人口の 0.5 乗で、ステージ内の合計が 1000 になるよう正規化（§4）" },
      "notes": [
        {
          "topic": "weather",
          "text": "（計画書の気象に関する記述の要約が入る）",
          "provenance": "derived",
          "source": { "doc": "隠岐地域離島振興計画", "page": null }
        }
      ]
    }
  ]
}
```

- `value: null` は「ETL で埋める」ことを表します。**このドキュメントでは実際の数値を書いていません**（記憶に頼った数値を混ぜないため）
- `requestWeightPermille` はステージごとに正規化が変わるので、実際にはステージの読み込み時に計算します。ETL の出力にはステージに依存しない元の値（人口の 0.5 乗）を入れ、ステージの読み込み時に整数の千分率へ変換します（§4）

### 3.1 使う島（仮決め）

| ID | 島 | 所属自治体 | ステージ |
|---|---|---|---|
| `oki-dogo` | 島後 | 隠岐の島町 | 1, 2, demo |
| `oki-nakanoshima` | 中ノ島 | 海士町 | 1, 2, demo |
| `oki-nishinoshima` | 西ノ島 | 西ノ島町 | 1, 2, demo |
| `izu-oshima` | 伊豆大島 | 大島町 | 3 |
| `izu-toshima` | 利島 | 利島村 | 3 |
| `izu-niijima` | 新島 | 新島村 | 3 |
| `izu-shikinejima` | 式根島 | 新島村 | 3 |
| `izu-kozushima` | 神津島 | 神津島村 | 3 |
| `goto-fukue` | 福江島 | 五島市 | 4 |
| `goto-hisaka` | 久賀島 | 五島市 | 4 |
| `goto-naru` | 奈留島 | 五島市 | 4 |
| `goto-wakamatsu` | 若松島 | 新上五島町 | 4 |
| `goto-nakadori` | 中通島 | 新上五島町 | 4 |
| `goto-ojika` | 小値賀島 | 小値賀町 | 4 |
| `goto-uku` | 宇久島 | 佐世保市 | 4 |

いずれも離島振興法の対象地域として選んでいます（奄美・小笠原・沖縄は別の法律の対象なので、D1 に含まれない可能性が高く、避けました）。D1 に含まれているかは ETL で確認し、含まれていなければ差し替えます。

---

## 4. 人口から依頼の発生率を決める

### 4.1 式

ステージ内の島 `i` の重みを、人口の 0.5 乗で決めます。

```text
w_i = round( 1000 × sqrt(pop_i) / Σ_j sqrt(pop_j) )   # 千分率の整数。合計が 1000 になるよう最大剰余法で調整
```

- 0.5 乗にするのは、人口の大きい島に依頼が集中しすぎて、小さい島が「何も起きない島」になるのを防ぐためです（指数はステージ定義の `populationExponent` で変えられます。既定 0.5）
- 浮動小数点の計算はステージの**読み込み時に 1 回だけ**行い、以降のシミュレーションでは整数の `w_i` だけを使います（CPU アーキテクチャによる浮動小数点の差で決定性が崩れるのを防ぐため。[DesignDoc §9.2](./03-design-doc.md)）

### 4.2 ランダムな依頼の発生

ラウンドを `slotMs`（既定 10 秒）ごとの区間に分け、各区間・各島で次の確率で依頼を 1 件発生させます。

```text
p_i [ppm] = w_i × rateMilliPerMin × (slotMs / 1000) / 60
発生する ⇔ rng("requests").IntN(1_000_000) < p_i
```

- `rateMilliPerMin`: ステージ全体での 1 分あたりの発生数 × 1000（例: 500 = 毎分 0.5 件）
- 確認: `w_i = 1000`（島が 1 つ）、`rateMilliPerMin = 500`、`slotMs = 10000` → `p = 83,333 ppm`（10 秒に 8.3%、1 分で 0.5 件）
- 発生時刻は区間内で `rng.IntN(slotMs)` によって決める
- 種類は、ステージ定義の `random.kinds` の重みで選ぶ

---

## 5. ステージ定義 `data/stages/*.yaml`

### 5.1 例（Stage 1）

```yaml
format: 1
id: stage1
title: はじめての郵便船
region: 隠岐諸島
intro:
  text: 隠岐の島々は、本土から離れた日本海に浮かぶ島々です。…
  provenance: derived           # 計画書の記述を要約した場合
  source: { doc: 隠岐地域離島振興計画, page: null }
learning: [log_replication, commit, quorum]   # リザルト後に解放するヘルプのキー
durationMs: 180000
unlock: null

islands:                        # 並び順が Raft のノード ID（1, 2, 3…）になる
  - ref: oki-dogo
    layout: { x: 640, y: 160 }  # creative（マップ上の配置）
    color: "#E07A5F"
  - ref: oki-nakanoshima
    layout: { x: 300, y: 420 }
    color: "#3D85C6"
  - ref: oki-nishinoshima
    layout: { x: 160, y: 300 }
    color: "#81B29A"

routes:                         # 航路の有無は計画書の海上交通の記述をもとに作る（derived）、時間は creative
  - { a: oki-dogo,        b: oki-nakanoshima,  travelMs: 9000 }
  - { a: oki-dogo,        b: oki-nishinoshima, travelMs: 10000 }
  - { a: oki-nakanoshima, b: oki-nishinoshima, travelMs: 4000 }

ferries:
  - { id: f1, name: おき丸, home: oki-dogo, capacity: 4, speed: 1.0 }

raft:
  initialLeader: oki-dogo
  tickMs: 500
  heartbeatTick: 2
  electionTick: 1200            # 600 秒。Stage 1 では選挙を起こさない
  checkQuorum: true
  preVote: true
  maxInflightMsgs: 4
  maxSizePerMsg: 4096

transport:
  dockMs: 1500
  perLinkAppCap: 4
  portCap: 24
  relayCap: 12

resources:
  - { id: "firework:a", kind: firework, label: 大花火, critical: true }
  - { id: "stall:main", kind: stall, label: 屋台の出店枠, capacity: 5 }

requests:
  deadlineMs: { normal: 60000, critical: 90000 }
  retryMs: 25000
  script:
    - { at: 5000,   island: oki-dogo,         kind: stall.reserve,    resource: "stall:main" }
    - { at: 30000,  island: oki-nishinoshima, kind: firework.reserve, resource: "firework:a" }
    - { at: 60000,  island: oki-nakanoshima,  kind: stall.reserve,    resource: "stall:main" }
    - { at: 80000,  island: oki-dogo,         kind: stall.reserve,    resource: "stall:main" }
    - { at: 105000, island: oki-nishinoshima, kind: stall.reserve,    resource: "stall:main" }
    - { at: 130000, island: oki-nakanoshima,  kind: stall.reserve,    resource: "stall:main" }
  random:
    rateMilliPerMin: 0          # Stage 1 ではランダムな依頼なし
    slotMs: 10000
    populationExponent: 0.5
    kinds: [ { kind: stall.reserve, resource: "stall:main", weight: 1 } ]

events: []                      # Stage 1 ではイベントなし

scoring:                        # 省略時は既定値（04-game-design.md §5）
  target: { successes: 4, score3: 900 }

satisfaction:
  initial: 60
  silenceAfterMs: 30000
  silencePenalty: { everyMs: 10000, amount: 2 }

tutorial: stage1                # クライアントのチュートリアル台本のキー
```

### 5.2 イベントの書き方（Stage 2 の例）

```yaml
events:
  - kind: storm
    at: 60000                   # 固定時刻。幅を持たせる場合は window: [55000, 65000]（シードで決まる）
    durationMs: 40000
    routes: [[oki-dogo, oki-nakanoshima], [oki-dogo, oki-nishinoshima]]
    forecastMs: 10000
    flavor:                     # 計画書の気象の記述を題材にする
      text: 冬の北西の季節風で、島後のまわりの海が荒れています
      provenance: derived
      source: { doc: 隠岐地域離島振興計画, page: null }
  - kind: breakdown             # P1
    window: [80000, 120000]
    ferry: any                  # any = シードで選ぶ
    durationMs: 30000
  - kind: rush                  # P1
    at: 105000
    requests: 4
    forecastMs: 5000
  - kind: drift                 # P1：過去に配達した Raft の手紙を n 通、複製して流す
    at: 130000
    letters: 3
    minAgeMs: 30000
```

### 5.3 読み込み時の検証

ステージの読み込み（`internal/stage`）は、次のどれかに当てはまればエラーにします。CI では全ステージを読み込むテストを行います。

- `islands` の `ref` が `islands.json` にない / 重複している
- 島が 3 つ未満、または 7 つより多い
- 航路グラフが連結でない
- `initialLeader`・`ferries[].home`・`requests.script[].island` が `islands` にない
- `resources` にない資源を参照している / `firework` に `capacity` がある / `stall` に `capacity` がない
- `at` や `window` が `durationMs` を超えている
- `electionTick ≤ heartbeatTick`（`etcd-io/raft` の制約）
- 選挙タイムアウト（`electionTick × tickMs`）が、Leader と最も近い Follower の往復時間（`2 × travelMs + dockMs`）より短い → **警告**（エラーにはしない。自由実験モードではわざと短くするため）

---

## 6. クレジット表示

クレジット画面と `NOTICE` に次を載せます（文言は利用規約を確認して確定する）。

```text
本ゲームは、国土交通省「離島振興対策実施地域一覧」「離島振興計画書」を加工して作成しています。
航行時間・積載量・イベントなどはゲーム上の設定であり、実際の航路・運航とは異なります。

etcd-io/raft — Copyright The etcd Authors — Apache License 2.0
（乱数源を注入する変更を加えています。詳細は server/third_party/etcd-raft/PATCHES.md）
```
