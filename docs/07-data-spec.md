# データ仕様（オープンデータ・島データ・ステージ定義）

| 項目 | 内容 |
|---|---|
| 版 | v0.4 |
| 更新日 | 2026-11-07 |
| 状態 | レビュー済み。提供データの実ファイルを確認して反映した |
| 関連 | [ADR-0013](./adr/0013-open-data-build-time-etl.md) / [ADR-0022](./adr/0022-yaml-library.md) / [要件 FR-DATA](./01-requirements.md#410-オープンデータfr-data) / [判断の記録 A-05・A-06・A-21](./09-assumptions-and-open-questions.md) |

---

## 1. 方針

1. オープンデータの数値は、元の意味を保ったまま使う（人口は人口として表示し、加工した値は加工したと明示する）
2. データにない値（航行時間・積載量・イベントの発生率など）はゲーム用のパラメータとして作り、**作ったものだと分かるように**する
3. すべての値に**出典区分**を付ける

| 出典区分 | 意味 | 画面での表示 | 例 |
|---|---|---|---|
| `fact` | オープンデータの値そのまま | 「出典：国土交通省『離島に関するデータ』」 | 人口、面積、所属自治体、計画書の記述 |
| `derived` | オープンデータから計算・要約したもの | 「国土交通省『離島に関するデータ』をもとに作成」 | 2 町の人口の合計、人口から決めた依頼の重み、所要時間から換算した航行時間、欠航率から決めた欠航の確率 |
| `creative` | ゲームのための創作 | 「ゲーム上の設定」 | データにない航路の航行時間、積載量、キャラクターのセリフ、マップ上の配置 |

---

## 2. 提供データ

### 2.1 データセットの情報

データ定義書（Excel）で確認した内容です。

| 項目 | 内容 |
|---|---|
| データセット | 国土交通省 Project LINKS アイディアソン公開データ（2026年度）「離島に関するデータ」 |
| 説明 | 離島地域の都道府県が策定する「離島振興計画」等をもとに作成したデータ |
| 提供者・作成者 | 国土交通省 総合政策局 情報政策課 |
| 公開日 | 2026-10-31 |
| 形式 | CSV（UTF-8、BOM 付き）＋データ定義書（xlsx） |
| ライセンス | 政府標準利用規約（第 2.0 版） |
| 利用規約 | https://www.mlit.go.jp/links/terms-of-use.html |
| ホームページ | https://www.mlit.go.jp/links/ |

### 2.2 ファイルと使い道

| # | データ | ファイル | 行数 | 使い道 | 優先 |
|---|---|---|---|---|---|
| D1 | 離島振興対策実施地域一覧 | `01_ritoushinkoutaisakujisshichiikiichiran.csv` | 2,077 | 島名・都道県・指定地域・自治体・人口・面積 | P0 |
| D2 | 離島振興計画書（全体情報） | `01_zentaijouhou.csv` | 26 地域 | 地理・気象・人口・海上交通・港湾・観光などの文章 | P0 |
| D2' | 離島振興計画書（所属市町村） | `02_shozokushichouson.csv` | 254 | 計画の地域と島・自治体の対応（ETL では使わない。§2.5） | P2 |
| D3 | 離島活性化交付金等事業計画書 | `01_ritoukasseikakoufukintoujigyoukeikakusho.csv` | 83 | 事業の目的・課題（発展ミッション） | P2 |
| D4 | 離島広域活性化事業（全体情報・成果目標・基幹事業・交付金の執行状況） | `01_zentaijouhou.csv`・`02_zenkeikakunoseikamokuhyou.csv`・`03_kikanjigyou.csv`・`04_koufukinnoshikoujoukyou.csv` | 25 計画 | 発展ミッションの候補 | P2 |

D2 と D4 は、どちらもファイル名が `01_zentaijouhou.csv` です。`data/raw/` では、データセットごとにディレクトリを分けて置きます（§2.4）。

### 2.3 列の定義

**D1 離島振興対策実施地域一覧**

| 列 | 型 | 内容 | 例 |
|---|---|---|---|
| `_id` | int | 基準日ごとの番号 | 1 |
| `DETAIL_NO` | int | 明細番号 | 1 |
| `REFERENCE_DATE` | string | 基準年・調査時点 | 令和8年4月1日 |
| `PREFECTURE_NAME_CODE` | string | 都道県名 | 長崎県 |
| `DESIGNATED_AREA_NAME_CODE` | string | 指定地域名 | 五島列島 |
| `ISLAND_NAME_CODE` | string | 島名 | 福江島 |
| `MUNICIPALITY_NAME_CODE` | string | 市町村名 | 五島市 |
| `POPULATION` | int | 人口（人） | 31945 |
| `AREA` | float | 面積（km²） | 326.36 |

- 基準日が令和 2 年〜令和 8 年の **8 時点分**並んでいます。**最新の基準日（令和 8 年 4 月 1 日）の行を使います**
- 1 つの島が複数の自治体にまたがる場合（小豆島＝小豆島町・土庄町、利尻島＝利尻町・利尻富士町 など）は、**自治体ごとに行が分かれています**。人口と面積は合計して使います（出典区分 `derived`）
- 同じ島名が別の地域にもあるので（例: 大島は 10 か所以上）、島は「都道県名＋指定地域名＋島名」で特定し、自治体を `islands-select.yaml` の `municipalities` と照合します（§2.5・§2.6）

**D2 離島振興計画書（全体情報）**

| 列 | 内容 | ゲームでの使い道 |
|---|---|---|
| `DESIGNATED_REGION_NAME` | 指定地域名 | ステージ紹介の見出し |
| `ISLAND_NAME` | 島名（読点区切り） | 計画の地域の取り違えを防ぐ照合（§2.5 の段階 4） |
| `GEO_LOCATION_AREA` | 地理的位置・面積 | ステージ紹介 |
| `NATURAL_CLIMATE_FEATURES` | 自然・気象特性 | 嵐のイベントの題材 |
| `POPULATION_HOUSEHOLDS` | 人口・世帯 | ステージ紹介 |
| `MARITIME_TRANSPORT` | 海上交通 | 航路の有無・所要時間・欠航率 |
| `PORTS` | 港湾 | ステージ紹介 |
| `LOCAL_INDUSTRY_TOURISM` | 商工業・観光 | 島の紹介文 |
| `DISASTER_LAND_CONSERVATION` | 防災・国土保全 | 嵐のイベントの題材（台風の被害の記録） |
| ほか（`AIR_TRANSPORT`・`AGRI_FORESTRY_FISHERIES` など 9 項目） | — | P2 |

- 26 地域の計画で、**地域ごとに情報量の差が大きい**ことに注意します

| 地域 | 情報量 |
|---|---|
| 小豆島地域 | 海上交通が 501 字。航路ごとの距離・所要時間・便数が書かれている |
| 伊豆諸島地域 | 島ごとの欠航率と、台風の被害の記録がある |
| 長崎県 | 海上交通が 36 字、港湾が 15 字と短い |

### 2.4 `data/raw/` の置き方

```text
data/raw/
├── README.md                          # 出典・取得日・利用規約・加工の有無（§6 の文言を含む）
├── ritou-list/                        # D1
│   ├── 01_ritoushinkoutaisakujisshichiikiichiran.csv
│   └── 99_ritoushinkoutaisakujissichiikiichiran_dataspecificationdocument.xlsx
├── shinkou-keikaku/                   # D2・D2'
│   ├── 01_zentaijouhou.csv
│   ├── 02_shozokushichouson.csv
│   └── 99_ritousinkoukeikakusho_dataspecificationdocument.xlsx
├── kasseika-koufukin/                 # D3
│   ├── 01_ritoukasseikakoufukintoujigyoukeikakusho.csv
│   └── 99_ritoukasseikakouhukinntoujigyoukeikakusho_dataspecificationdocument.xlsx
└── kouiki-kasseika/                   # D4
    ├── 01_zentaijouhou.csv … 04_koufukinnoshikoujoukyou.csv
    └── 99_ritoukouikikasseikajigyou_dataspecificationdocument.xlsx
```

利用規約（政府標準利用規約 第 2.0 版）は、出典を記載すれば複製・再配布を認めているので、元のファイルをそのままコミットします（[A-21](./09-assumptions-and-open-questions.md#a-21)）。

### 2.5 ETL（`server/cmd/islandsdata`）

```bash
# リポジトリのルートで実行する（パスは server/ からの相対）
go -C server run ./cmd/islandsdata \
  --raw ../data/raw --curated ../data/curated \
  --select ../data/islands-select.yaml --out ../data/islands.json [--check]
```

- `--raw`・`--curated`・`--select`・`--out` はすべて必須。足りない・未知のフラグ・余分な引数は、使い方を出して終了コード 2
- 入力をすべて読んで検証し、成功したときだけ `--out` を書く（同じディレクトリの一時ファイルに書いてから置き換える。失敗したときは `--out` を変えない）。成功したら終了コード 0
- `--check` を付けると、ファイルを書かずに、生成したバイト列と `--out` の中身を比べる。同じなら 0、違う・ないなら再生成のコマンドを出して 1
- 入力の誤りは、`islandsdata: ` で始まるメッセージ（ファイルのパスと、CSV なら行番号、YAML なら島や抜き出しの `id`）を出して終了コード 1
- 手順は [`data/raw/README.md`](../data/raw/README.md) にもある

| 段階 | 処理 |
|---|---|
| 1. 読み込み | D1（`ritou-list/01_ritoushinkoutaisakujisshichiikiichiran.csv`）と D2（`shinkou-keikaku/01_zentaijouhou.csv`）を読む。先頭の BOM は外す（なくてもよい）。UTF-8 でない・引用符が壊れている・列数の違う行はエラー。改行は CRLF・LF のどちらでもよい。1 行目を見出しとして列を**名前で**引く（列の順序は問わず、使わない列があってもよい。§2.3 の使う列がなければエラー）。名前の列は前後の空白を除き、文章の列はそのまま使う |
| 2. 島の特定 | `islands-select.yaml`（§2.6）の各島について、D1 の最新の基準日の行のうち（都道県名, 指定地域名, 島名）が（`prefecture`, `designatedArea`, `name`）と一致する行を集める。0 行ならエラー（都道県名と島名が同じで指定地域が違う行があれば、その指定地域名を候補として出す）。集めた行の市町村名の集合が `municipalities` と違う・同じ市町村の行が 2 つ以上あればエラー |
| 3. 人口・面積 | 基準日は和暦（`平成`・`令和`、`元年`、全角の数字）を読み、**D1 のすべての行**の基準日の最大を「最新の基準日」とする（読めない行が 1 つでもあればエラー）。選んだ島に最新の基準日の行がなければエラーにし、古い基準日の値で代わりにしない。`asOf` は、選んだ行の `REFERENCE_DATE` の文字列（ファイルの順で最初の行）。人口は全角の数字と 3 桁ごとの `,` を受け付ける 0 以上の整数。面積は小数第 4 位までで、**0.0001 km² 単位の整数**で扱う（浮動小数点を使わない）。自治体が 1 つなら `fact`、2 つ以上なら整数のまま合計して `derived` にし、`note` を付ける（§3.1） |
| 4. 計画書 | 島に `plan` があるときだけ、D2 の `DESIGNATED_REGION_NAME` が `plan.region` と一致する行（ちょうど 1 行。0 行・2 行以上はエラー）を使う。その行の `ISLAND_NAME` を `、` で分けた一覧に `plan.islandName`（省略時は島の `name`）がなければエラー。§3.1 の 7 つの列の文章を、そのまま（空白も除かずに）取り込む（`fact`）。空白だけの列はキーごと出さない |
| 5. 人手の値 | `data/curated/*.yaml`（§3.4）を読んで検証し、`excerpts` に出す（`id` の昇順） |
| 6. 出力 | `json.Encoder` に `SetEscapeHTML(false)` と 2 つの空白の字下げを付けて書く（末尾は改行 1 つ）。キーの順序は §3.1 の順に固定する（`sources` だけはキーの昇順）。`islands` と `municipalities` は `islands-select.yaml` の順。同じ入力からは同じバイト列になる（テストで確認） |

- 出力の決定性のため、ETL は実時間・乱数・goroutine・浮動小数点を使わず、map を走査して出力の順序を決めない。`generatedAt` は `islands-select.yaml` の値をそのまま書く
- 段階 4 では D2' を使わない。D2' の列の定義がこの仕様になく、提供データもまだリポジトリにないため、計画の地域を `islands-select.yaml` の `plan.region` に人が書き、D2 の `ISLAND_NAME` で取り違えを防ぐ形にした（T-205）。D2' を使う形に戻すかどうかは、提供データが入ってから判断する

### 2.6 ETL の設定 `data/islands-select.yaml`

出典の情報と、使う島（§3.2）を書きます。読み込みには `go.yaml.in/yaml/v3` を使い、未知のキー・重複したキー・型の違いはエラーにします（[ADR-0022](./adr/0022-yaml-library.md)）。

```yaml
format: 1
generatedAt: "2026-11-07"          # この設定で生成した日。ETL はこの値を出力にそのまま書く
dataset: mlit-ritou-2026           # 出力の各値の source（sources のキー）
sources:
  mlit-ritou-2026:
    title: Project LINKS アイディアソン公開データ（2026年度）離島に関するデータ
    publisher: 国土交通省 総合政策局 情報政策課
    license: 政府標準利用規約（第2.0版）
    termsUrl: https://www.mlit.go.jp/links/terms-of-use.html
    publishedAt: "2026-10-31"
islands:
  - { id: kagawa-shodoshima, prefecture: 香川県, name: 小豆島, designatedArea: 小豆島, municipalities: [小豆島町, 土庄町], plan: { region: 小豆島地域 } }
  - { id: kagawa-naoshima, prefecture: 香川県, name: 直島, designatedArea: 直島諸島, municipalities: [直島町] }
  # plan: { region: <D2 の DESIGNATED_REGION_NAME>, islandName: <D2 の ISLAND_NAME での書き方。省略時は name> }
```

検証（どれかが違えばエラー）:

- `format` が 1（整数）
- `generatedAt` と `sources.*.publishedAt` が実在する `YYYY-MM-DD`
- `sources` が 1 つ以上で、各項目の `title`・`publisher`・`license`・`termsUrl`・`publishedAt` が空でなく、`termsUrl` が `https://` で始まる
- `dataset` が `sources` のキーにある
- `islands` が 1 つ以上。`id` が `^[a-z]+(-[a-z0-9]+)+$` に合い一意。`prefecture`・`name`・`designatedArea` が空でない。（`prefecture`, `designatedArea`, `name`）が一意
- `municipalities` が 1 つ以上で、重複も空の要素もない
- `plan` があれば `plan.region` が空でない

`plan.region` は、地域名を確かめた島（小豆島地域・長崎県・伊豆諸島地域）にだけ書きます。直島と Stage 4 だけの島は、実データで確かめてから足します。

再生成するときは、`generatedAt` を人が更新します。

---

## 3. 島データ `data/islands.json`

### 3.1 形式

```json
{
  "format": 1,
  "generatedAt": "2026-11-06",
  "sources": {
    "mlit-ritou-2026": {
      "title": "Project LINKS アイディアソン公開データ（2026年度）離島に関するデータ",
      "publisher": "国土交通省 総合政策局 情報政策課",
      "license": "政府標準利用規約（第2.0版）",
      "termsUrl": "https://www.mlit.go.jp/links/terms-of-use.html",
      "publishedAt": "2026-10-31"
    }
  },
  "islands": [
    {
      "id": "kagawa-shodoshima",
      "name": "小豆島",
      "prefecture": "香川県",
      "designatedArea": "小豆島",
      "municipalities": ["小豆島町", "土庄町"],
      "population": { "value": 25881, "asOf": "令和8年4月1日", "provenance": "derived", "note": "小豆島町 13,870 人と土庄町 12,011 人の合計", "source": "mlit-ritou-2026" },
      "areaKm2":    { "value": 153.27, "asOf": "令和8年4月1日", "provenance": "derived", "note": "小豆島町 95.59 km²と土庄町 57.68 km²の合計", "source": "mlit-ritou-2026" },
      "populationSqrt": 160.88,
      "plan": {
        "region": "小豆島地域",
        "geography":            { "text": "…", "provenance": "fact", "source": "mlit-ritou-2026", "field": "GEO_LOCATION_AREA" },
        "climate":              { "text": "温暖、小雨の典型的な瀬戸内式気候であり、…", "provenance": "fact", "source": "mlit-ritou-2026", "field": "NATURAL_CLIMATE_FEATURES" },
        "populationHouseholds": { "text": "…", "provenance": "fact", "source": "mlit-ritou-2026", "field": "POPULATION_HOUSEHOLDS" },
        "maritime":             { "text": "小豆島(土庄町)には、土庄港を拠点として、高松~土庄(22km・60分 …", "provenance": "fact", "source": "mlit-ritou-2026", "field": "MARITIME_TRANSPORT" },
        "ports":                { "text": "…", "provenance": "fact", "source": "mlit-ritou-2026", "field": "PORTS" },
        "tourism":              { "text": "… エンジェルロード、土渕海峡など …", "provenance": "fact", "source": "mlit-ritou-2026", "field": "LOCAL_INDUSTRY_TOURISM" },
        "disaster":             { "text": "…", "provenance": "fact", "source": "mlit-ritou-2026", "field": "DISASTER_LAND_CONSERVATION" }
      }
    }
  ],
  "excerpts": [
    { "id": "setouchi-teshima-tonosho-minutes", "region": "小豆島地域", "field": "MARITIME_TRANSPORT", "quote": "豊島~小豊島~土庄(8km・25分", "provenance": "derived", "value": 25, "unit": "分", "note": "小豊島に寄る便の全体の所要時間", "source": "mlit-ritou-2026" }
  ]
}
```

- キーの順序は上の例の順で固定です（`sources` だけはキーの昇順）。`islands`・`municipalities`・`excerpts` は空でも `[]` です
- `population` と `areaKm2` の `asOf` は、使った D1 の行の `REFERENCE_DATE` の文字列です
- 自治体が 1 つの島は `provenance: "fact"` で `note` を出しません。2 つ以上の島は `derived` で、`note` を `municipalities` の順に「`A 1 人、B 2 人とC 3 人の合計`」「`A 1.5 km²とB 2 km²の合計`」の形にします（最後の 1 つの前が `と`、ほかは `、`。人口は 3 桁ごとに `,`）
- 面積と `populationSqrt` は、整数の値から 10 進の文字列を作り、小数部の末尾の `0` と残った末尾の `.` を消して、数として書きます（`14.5000` → `14.5`、`12.0000` → `12`）
- `populationSqrt` はステージに依存しない中間値（人口の 0.5 乗）で、**小数第 2 位に四捨五入**した値です。**整数だけで計算します**: `x = 人口 × 10000`、`n = ⌊√x⌋`（整数の平方根）、`x > n² + n` なら `n + 1`（0.01 単位）。例: 25881 → 160.88、768 → 27.71、169 → 13、2 → 1.41、0 → 0。ステージごとの重みは、ステージの読み込み時に整数の千分率へ変換します（§4）
- `plan` は、`islands-select.yaml` に `plan` がある島にだけ出します。7 つのキーは、`geography`（`GEO_LOCATION_AREA`）・`climate`（`NATURAL_CLIMATE_FEATURES`）・`populationHouseholds`（`POPULATION_HOUSEHOLDS`）・`maritime`（`MARITIME_TRANSPORT`）・`ports`（`PORTS`）・`tourism`（`LOCAL_INDUSTRY_TOURISM`）・`disaster`（`DISASTER_LAND_CONSERVATION`）です。文章が空白だけの列は、キーごと出しません
- `excerpts` は `data/curated/*.yaml`（§3.4）の抜き出しで、`id` の昇順です。`value`・`unit`・`note` は書いたものだけ出し、`source` は `islands-select.yaml` の `dataset` です
- 島の ID は「都道府県のローマ字＋島名」にします（同じ名前の島を区別するため）

### 3.2 使う島（確定）

人口・面積は D1 の令和 8 年 4 月 1 日の値です。

| ID | 島 | 都道県 | 指定地域（D1） | 自治体 | 人口 | 面積（km²） | ステージ |
|---|---|---|---|---|---|---|---|
| `kagawa-shodoshima` | 小豆島 | 香川県 | 小豆島 | 小豆島町・土庄町 | 25,881（合計） | 153.27（合計） | 1 |
| `kagawa-teshima` | 豊島 | 香川県 | 小豆島 | 土庄町 | 768 | 14.50 | 1 |
| `kagawa-naoshima` | 直島 | 香川県 | 直島諸島 | 直島町 | 3,071 | 7.82 | 1, 4 |
| `nagasaki-fukue` | 福江島 | 長崎県 | 五島列島 | 五島市 | 31,945 | 326.36 | 2, 4, demo-goto |
| `nagasaki-naru` | 奈留島 | 長崎県 | 五島列島 | 五島市 | 1,927 | 23.68 | 2, demo-goto |
| `nagasaki-nakadori` | 中通島 | 長崎県 | 五島列島 | 新上五島町 | 16,112 | 168.39 | 2, demo-goto |
| `tokyo-oshima` | 大島 | 東京都 | 伊豆諸島 | 大島町 | 7,102 | 90.73 | 3, demo-izu |
| `tokyo-toshima` | 利島 | 東京都 | 伊豆諸島 | 利島村 | 327 | 4.12 | 3, demo-izu |
| `tokyo-miyakejima` | 三宅島 | 東京都 | 伊豆諸島 | 三宅村 | 2,273 | 55.20 | 3, demo-izu |
| `tokyo-mikurajima` | 御蔵島 | 東京都 | 伊豆諸島 | 御蔵島村 | 323 | 20.51 | 3, demo-izu |
| `tokyo-hachijojima` | 八丈島 | 東京都 | 伊豆諸島 | 八丈町 | 7,042 | 69.11 | 3, 4, demo-izu |
| `tokyo-aogashima` | 青ヶ島 | 東京都 | 伊豆諸島 | 青ヶ島村 | 169 | 5.96 | 3, demo-izu |
| `niigata-sado` | 佐渡島 | 新潟県 | 佐渡島 | 佐渡市 | 51,492 | 854.81 | 4 |
| `hokkaido-rishiri` | 利尻島 | 北海道 | 利尻島 | 利尻町・利尻富士町 | 4,462（合計） | 182.08（合計） | 4 |
| `shimane-dogo` | 島後 | 島根県 | 隠岐島 | 隠岐の島町 | 13,433 | 241.53 | 4 |
| `nagasaki-tsushima` | 対馬島 | 長崎県 | 対馬島 | 対馬市 | 28,374 | 695.74 | 4 |

選定の基準と経緯は [A-05](./09-assumptions-and-open-questions.md#a-05) と [R-07](./09-assumptions-and-open-questions.md#r-07) にあります。

### 3.3 計画書から使う記述（`data/curated/` に抜き出すもの）

| ステージ | 記述（計画書の項目） | 使い道 | 出典区分 |
|---|---|---|---|
| 1 | 小豆島地域の `MARITIME_TRANSPORT`：「豊島~小豊島~土庄(8km・25分貸客船1便/日)」「高松~直島~豊島(22.6km・35分3~4便/日)」 | 航路と航行時間（§4.3） | `derived` |
| 1 | 小豆島地域の `NATURAL_CLIMATE_FEATURES`：「温暖、小雨の典型的な瀬戸内式気候」 | 嵐のない穏やかな海のチュートリアルの紹介文 | `fact` |
| 2 | 長崎県の `NATURAL_CLIMATE_FEATURES`：「台風等の自然災害の影響を受けやすい」 | 嵐のイベントの題材 | `fact` |
| 2 | 長崎県の `LOCAL_INDUSTRY_TOURISM`：「世界遺産登録された『長崎と天草地方の潜伏キリシタン関連遺産』」 | 島の紹介文 | `fact` |
| 3 | 伊豆諸島地域の `MARITIME_TRANSPORT`：「令和2(2020)年利島17%、御蔵島・青ヶ島41%」（定期貨客船の欠航） | 欠航のイベントの確率（§4.4） | `derived` |
| 3 | 伊豆諸島地域の `MARITIME_TRANSPORT`：「青ヶ島-八丈島-御蔵島-三宅島-大島-利島の各区間においては、ヘリコミューター … が運航」 | 島どうしのつながり（どの島とどの島に航路を引くか）（§4.5） | `derived` |
| 3 | 伊豆諸島地域の `PORTS`：「… あおがしま丸、橘丸、フェリーあぜりあ、… 新さるびあ丸、セブンアイランド結、… くろしお丸が就航」 | 船の名前（「計画書に記載された船名を使用しており、運航会社とは関係ありません」と注記する） | `fact` |
| 3 | 伊豆諸島地域の `DISASTER_LAND_CONSERVATION`：「平成25年台風26号による大島町の被害」 | 嵐のイベントの題材 | `fact` |

### 3.4 抜き出しの形式 `data/curated/*.yaml`

§3.3 の記述は、`data/curated/*.yaml` に出典（地域・項目・引用）付きで書きます（中身は T-206）。ETL は `data/curated/` の直下の `*.yaml` をファイル名の昇順で読み（サブディレクトリは見ない）、検証して `islands.json` の `excerpts` に出します。`*.yml` があればエラーにします（読み落としを防ぐため）。それ以外のファイル（`.gitkeep` など）は無視します。

```yaml
format: 1
excerpts:
  - id: setouchi-teshima-tonosho-minutes   # 全ファイルで一意。^[a-z0-9]+(-[a-z0-9]+)*$
    region: 小豆島地域                       # D2 の DESIGNATED_REGION_NAME
    field: MARITIME_TRANSPORT              # D2 の列名（_id 以外）
    quote: "豊島~小豊島~土庄(8km・25分"       # その列の文章にそのまま含まれる部分文字列
    provenance: derived                    # fact か derived（creative は書かない）
    value: 25                              # 任意。整数だけ（小数はエラー）
    unit: 分                               # value があるときは必須、ないときは書かない
    note: 小豊島に寄る便の全体の所要時間       # 任意
```

検証（どれかが違えば、ファイルと `id` を出してエラー）:

- 未知のキー・重複したキー・型の違いがない（[ADR-0022](./adr/0022-yaml-library.md)）
- `format` が 1
- `id` が形式に合い、全ファイルで一意
- `region` の行が D2 にちょうど 1 行ある
- `field` がその D2 の見出しにある列（`_id` を除く）
- `quote` が空でなく、その行のその列の文章に含まれる（正規化しないで比べる。`~` と `～`、全角と半角も区別する）
- `provenance` が `fact` か `derived`
- `value` があれば `unit` がある。`value` がなければ `unit` がない

`data/curated/` を変えたら、`islands.json` を再生成し、`--check` が通ることを確かめます（§2.5）。

---

## 4. データから値を決める式

### 4.1 人口から依頼の重みを決める

ステージ内の島 `i` の重みを、人口の 0.5 乗で決めます。

```text
w_i = round( 1000 × sqrt(pop_i) / Σ_j sqrt(pop_j) )   # 千分率の整数。合計が 1000 になるよう最大剰余法で調整
```

- 0.5 乗にするのは、人口の大きい島に依頼が集中しすぎて、小さい島が「何も起きない島」になるのを防ぐためです（指数はステージ定義の `populationExponent` で変えられます。既定 0.5）
- 浮動小数点の計算はステージの**読み込み時に 1 回だけ**行い、以降のシミュレーションでは整数の `w_i` だけを使います（CPU アーキテクチャによる浮動小数点の差で決定性が崩れるのを防ぐため。[DesignDoc §9.2](./03-design-doc.md)）

確定した島での計算結果は次のとおりです。

| ステージ | 重み（‰） |
|---|---|
| Stage 1 | 小豆島 659 / 直島 227 / 豊島 114 |
| Stage 2 | 福江島 511 / 中通島 363 / 奈留島 126 |
| Stage 3 | 大島 318 / 八丈島 317 / 三宅島 180 / 利島 68 / 御蔵島 68 / 青ヶ島 49 |
| Stage 4 | 佐渡島 253 / 福江島 199 / 対馬島 188 / 島後 129 / 八丈島 94 / 利尻島 75 / 直島 62 |
| demo-izu | Stage 3 と同じ |
| demo-goto | Stage 2 と同じ |

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

### 4.3 所要時間から航行時間を決める

計画書に所要時間が載っている航路は、次の式で航行時間を決めます（[A-06](./09-assumptions-and-open-questions.md#a-06)）。

```text
travelMs = 実際の所要時間[分] × 150     # 実際の 1 分 ＝ ゲーム内 0.15 秒
```

| ステージ | 航路 | 根拠の記述 | 実際の所要時間 | `travelMs` | 注意 |
|---|---|---|---|---|---|
| 1 | 小豆島–豊島 | 「豊島~小豊島~土庄(8km・25分貸客船1便/日)」 | 25 分 | 3,750 | 小豊島に寄る便。区間の時間は書かれていないので、全体の時間を使う |
| 1 | 豊島–直島 | 「高松~直島~豊島(22.6km・35分3~4便/日)」 | 35 分 | 5,250 | 高松発の便。直島–豊島の区間の時間は書かれていないので、全体の時間を上限として使う |
| 1 | 小豆島–直島 | 直接の航路の記述がない | — | （航路なし） | 豊島を経由する |

- 便数（「1便/日」など）は、将来、船の出航できる回数の制限に使えます（P2）
- 所要時間が載っていない航路（五島・伊豆・全国）は、`creative` の値をステージ定義に書きます

### 4.4 欠航率から欠航の確率を決める（Stage 3、P1）

伊豆諸島の計画書の欠航率（令和 2 年：利島 17%、御蔵島 41%、青ヶ島 41%）を、欠航のイベントの確率に使います。

```text
欠航の確率[‰] = 欠航率[%] × 10                   # 利島 170‰、御蔵島 410‰、青ヶ島 410‰
各区間（slotMs = 30 秒）ごとに、その島に接する航路を
rng("events").IntN(1000) < 欠航の確率 なら durationMs（10 秒）の間、出航できなくする
```

- 欠航率が載っていない島（大島・三宅島・八丈島）に接する航路は、その島の側からは欠航させません（台本の嵐だけ）
- 御蔵島と青ヶ島は欠航が非常に多く、難しくなりすぎる場合は `durationMs` を短くして調整します（確率はデータのまま変えない）
- デモ用のステージ（`demo-izu`）では、展開を台本どおりにするため欠航のイベントを止め、欠航率はステージ紹介の文で見せます

### 4.5 計画書の記述から島どうしのつながりを決める（Stage 3）

伊豆諸島の計画書にある「青ヶ島-八丈島-御蔵島-三宅島-大島-利島の各区間においては、ヘリコミューター（ヘリコプターによる乗客の定期輸送）が運航」という記述を、島どうしの航路の組み合わせに使います。

| 航路 | 根拠 | `travelMs`（ゲーム上の設定） |
|---|---|---|
| 利島–大島 | 「大島-利島」の区間 | 3,000 |
| 大島–三宅島 | 「三宅島-大島」の区間 | 10,000 |
| 三宅島–御蔵島 | 「御蔵島-三宅島」の区間 | 4,000 |
| 御蔵島–八丈島 | 「八丈島-御蔵島」の区間 | 9,000 |
| 八丈島–青ヶ島 | 「青ヶ島-八丈島」の区間 | 6,000 |

- つながりの出典区分は `derived` です。ヘリコプターの区間であり、船の航路そのものではないことを、ステージ紹介に書きます
- 航行時間は計画書に書かれていないので、実際の距離の比をおおまかに反映した `creative` の値です

## 5. ステージ定義 `data/stages/*.yaml`

### 5.1 一覧

| ファイル | ステージ | 島（最初の代表を先頭に） | 航路の時間 |
|---|---|---|---|
| `stage1.yaml` | はじめての郵便船 | 小豆島・豊島・直島 | `derived` |
| `stage2.yaml` | 島から返事が来ない！ | 福江島・奈留島・中通島 | `creative` |
| `stage3.yaml` | 嵐の離島共和国 | 大島・利島・三宅島・御蔵島・八丈島・青ヶ島 | つながりは `derived`、時間は `creative`（欠航の確率は `derived`） |
| `stage4.yaml` | 全国しまじま大作戦 | 佐渡島・利尻島・島後・八丈島・直島・対馬島・福江島 | `creative` |
| `demo-izu.yaml` | デモ（伊豆版・メイン） | Stage 3 と同じ 6 島 | Stage 3 の 0.6 倍 |
| `demo-goto.yaml` | デモ（五島版・サブ） | 福江島・奈留島・中通島 | `creative`（Stage 2 の 0.6 倍） |

### 5.2 例（Stage 1）

```yaml
format: 1
id: stage1
title: はじめての郵便船
region: 瀬戸内（香川県）
intro:
  text: 温暖で雨の少ない瀬戸内の海。小豆島・豊島・直島を結ぶ郵便船の、はじめての航海です。
  provenance: derived           # 計画書の記述（NATURAL_CLIMATE_FEATURES）をもとにした紹介文
  source: { dataset: mlit-ritou-2026, region: 小豆島地域, field: NATURAL_CLIMATE_FEATURES }
learning: [log_replication, commit, quorum]   # リザルト後に解放するヘルプのキー
durationMs: 180000
unlock: null

islands:                        # 並び順が Raft のノード ID（1, 2, 3…）になる
  - ref: kagawa-shodoshima
    layout: { x: 680, y: 260 }  # creative（マップ上の配置）
    color: "#E07A5F"
  - ref: kagawa-teshima
    layout: { x: 430, y: 300 }
    color: "#3D85C6"
  - ref: kagawa-naoshima
    layout: { x: 200, y: 330 }
    color: "#81B29A"

routes:
  - a: kagawa-shodoshima
    b: kagawa-teshima
    travelMs: 3750              # 25 分 × 150
    provenance: derived
    source: { dataset: mlit-ritou-2026, region: 小豆島地域, field: MARITIME_TRANSPORT, quote: "豊島~小豊島~土庄(8km・25分" }
  - a: kagawa-teshima
    b: kagawa-naoshima
    travelMs: 5250              # 35 分 × 150
    provenance: derived
    source: { dataset: mlit-ritou-2026, region: 小豆島地域, field: MARITIME_TRANSPORT, quote: "高松~直島~豊島(22.6km・35分" }

ferries:
  - { id: f1, name: せとうち丸, home: kagawa-shodoshima, capacity: 4, speed: 1.0 }

raft:
  initialLeader: kagawa-shodoshima
  tickMs: 500
  heartbeatTick: 2
  electionTick: 120             # 60〜120 秒。チュートリアルの終盤で立候補の相談を見せる（A-07）
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
    - { at: 5000,   island: kagawa-shodoshima, kind: stall.reserve,    resource: "stall:main" }
    - { at: 30000,  island: kagawa-naoshima,   kind: firework.reserve, resource: "firework:a" }
    - { at: 60000,  island: kagawa-teshima,    kind: stall.reserve,    resource: "stall:main" }
    - { at: 80000,  island: kagawa-shodoshima, kind: stall.reserve,    resource: "stall:main" }
    - { at: 105000, island: kagawa-naoshima,   kind: stall.reserve,    resource: "stall:main" }
    - { at: 130000, island: kagawa-teshima,    kind: stall.reserve,    resource: "stall:main" }
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

speed:                          # A-17
  allowed: [0.5, 1, 2, 4]

tutorial: stage1                # クライアントのチュートリアル台本のキー
```

### 5.3 イベントの書き方

```yaml
events:
  - kind: storm                 # Stage 2：福江島を孤立させる
    at: 60000                   # 固定時刻。幅を持たせる場合は window: [55000, 65000]（シードで決まる）
    durationMs: 40000
    routes: [[nagasaki-fukue, nagasaki-naru], [nagasaki-fukue, nagasaki-nakadori]]
    forecastMs: 10000
    flavor:
      text: 台風が近づき、福江島のまわりの海が荒れています
      provenance: derived
      source: { dataset: mlit-ritou-2026, region: 長崎県, field: NATURAL_CLIMATE_FEATURES, quote: "台風等の自然災害の影響を受けやすい" }
  - kind: cancellation          # Stage 3（P1）：欠航率から決める欠航。利島・御蔵島・青ヶ島の 3 つを書く
    island: tokyo-toshima
    probabilityPermille: 170    # 欠航率 17% × 10
    slotMs: 30000
    durationMs: 10000
    provenance: derived
    source: { dataset: mlit-ritou-2026, region: 伊豆諸島地域, field: MARITIME_TRANSPORT, quote: "令和2(2020)年利島17%、御蔵島・青ヶ島41%" }
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

デモ用のステージ（`demo-izu.yaml`・`demo-goto.yaml`）では、嵐を台本に書かず、発表者の操作（`demo.trigger`）で起こします。代わりに、発表者が起こす嵐の対象を `demo.storm.routes` に書きます。

```yaml
demo:
  endOnAllCriticalSecured: true # ★2 を達成した時点でラウンドを終え、花火大会へ
  storm:
    routes: [[tokyo-oshima, tokyo-miyakejima]]   # 北の 2 島（大島・利島）と南の 4 島に分かれる
    durationMs: 20000
    flavor:
      text: 台風が近づき、大島と三宅島の間の海が荒れています
      provenance: derived
      source: { dataset: mlit-ritou-2026, region: 伊豆諸島地域, field: DISASTER_LAND_CONSERVATION, quote: "平成25年台風26号による大島町の被害" }
```

### 5.4 読み込み時の検証

ステージの読み込み（`internal/stage`）は、次のどれかに当てはまればエラーにします。CI では全ステージを読み込むテストを行います。

- `islands` の `ref` が `islands.json` にない / 重複している
- 島が 3 つ未満、または 7 つより多い
- 航路グラフが連結でない
- `initialLeader`・`ferries[].home`・`requests.script[].island`・イベントの `island` / `routes` が `islands` にない
- `resources` にない資源を参照している / `firework` に `capacity` がある / `stall` に `capacity` がない
- `at` や `window` が `durationMs` を超えている
- `electionTick ≤ heartbeatTick`（`etcd-io/raft` の制約）
- `provenance: derived` の値に `source` がない
- 選挙タイムアウト（`electionTick × tickMs`）が、Leader と最も近い Follower の往復時間（`2 × travelMs + dockMs`）の 2 倍より短い → **警告**（エラーにはしない。自由実験モードではわざと短くするため）

---

### 5.5 船の名前（Stage 3・demo-izu）

計画書の伊豆諸島の `PORTS` の項目にある船名を使います（`fact`）。

```yaml
ferries:
  - { id: f1, name: 橘丸,           home: tokyo-oshima,      capacity: 4, speed: 1.0, provenance: fact }
  - { id: f2, name: 新さるびあ丸,   home: tokyo-oshima,      capacity: 3, speed: 1.2, provenance: fact }
  - { id: f3, name: くろしお丸,     home: tokyo-miyakejima,  capacity: 3, speed: 1.0, provenance: fact }
  - { id: f4, name: あおがしま丸,   home: tokyo-hachijojima, capacity: 3, speed: 1.0, provenance: fact }
```

- 積載量・速度・母港は `creative` です
- ステージ紹介とクレジットに「計画書に記載された船名を使用しており、運航会社とは関係ありません」と書きます

---

## 6. クレジット表示

クレジット画面と `NOTICE` に次を載せます。

```text
本ゲームは、国土交通省「Project LINKS アイディアソン公開データ（2026年度）離島に関するデータ」
（離島振興対策実施地域一覧・離島振興計画書）を加工して作成しています。
出典：国土交通省 Project LINKS（https://www.mlit.go.jp/links/）
利用規約：政府標準利用規約（第2.0版）
航行時間・積載量・イベントなどの多くはゲーム上の設定であり、実際の航路・運航とは異なります。
ゲーム内の船の名前の一部は、離島振興計画書に記載された船名を使用しており、実在の運航会社とは関係ありません。

etcd-io/raft — Copyright The etcd Authors — Apache License 2.0
（乱数源を注入する変更を加えています。詳細は server/third_party/etcd-raft/PATCHES.md）
```

リポジトリの `NOTICE` にも同じ内容を書きます（[ADR-0020](./adr/0020-public-repository-apache-license.md)）。

---

## 7. 素材（画像・音）

[判断の記録 R-09](./09-assumptions-and-open-questions.md#r-09) の方針です。

| 対象 | 作り方 | 出典区分 |
|---|---|---|
| 手紙・船・代表のマーク・嵐など、遊ぶうえで意味を持つもの | PixiJS の図形と絵文字。色と形の両方で区別する | `creative` |
| 島の郵便局長のキャラクター、タイトル画面、ステージ紹介の背景 | 画像生成 AI | `creative` |
| 効果音（出航・到着・確定・嵐・花火など） | 生成ツールなどで作る（MVP） | `creative` |
| BGM | MVP では使わない | — |

- 画像生成 AI などで作った素材は `web/public/assets/generated/` に置き、`web/public/assets/CREDITS.md` に、使ったサービス・作った日・利用規約の URL を書く
- 使うサービスの利用規約が、公開のリポジトリでの再配布（Apache-2.0）を認めていることを確かめてから使う
- 郵便局長の名前と口調は、計画書の観光などの記述（例：小豆島のオリーブ）にちなむ。実在のご当地キャラクターは使わない

### 7.1 PLATEAU の 3D 都市モデル（確認済み・今回は使わない）

- 「Project LINKS アイディアソン公開データ（2026年度）PLATEAU の成果報告書等のデータ」を確認した
  - 中身は、PLATEAU に取り組んだ自治体の報告書をデータにしたもの（CSV 19 本とデータ定義書）
  - 3D 都市モデル本体は含まれていない
- 整備状況の一覧（226 自治体）と照合した結果、今回の島の自治体で 3D 都市モデルがあるのは、Stage 4 の島後（隠岐の島町）だけだった
  - 建物・道路・土地利用：LOD1。範囲は都市計画区域の約 30 km²
  - 地形と災害リスク：町の全域（約 243 km²）
  - 細かい形の建物（LOD2）：ランドマークの 10 棟
- 2D の PixiJS のマップに 3D を入れるには別の描画ライブラリが必要になるため、今回は使わない
- 将来の案（P2、未採用）: Stage 4 の島後だけ、詳細の画面で 3D の地形と建物を表示する

