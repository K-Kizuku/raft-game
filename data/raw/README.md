# 提供データ（`data/raw/`）

国土交通省の「離島に関するデータ」の元のファイルを置く場所です。ゲームが使う島のデータ `data/islands.json` は、ここのファイルから ETL `server/cmd/islandsdata` で作ります（[データ仕様 §2](../../docs/07-data-spec.md#2-提供データ)）。

## 出典

| 項目 | 内容 |
|---|---|
| データセット | 国土交通省 Project LINKS アイディアソン公開データ（2026年度）「離島に関するデータ」 |
| 提供者・作成者 | 国土交通省 総合政策局 情報政策課 |
| 公開日 | 2026-10-31 |
| ライセンス | 政府標準利用規約（第 2.0 版） |
| 利用規約 | https://www.mlit.go.jp/links/terms-of-use.html |
| ホームページ | https://www.mlit.go.jp/links/ |
| 取得日 | 2026-11-07 |
| 取得元 | オーナーが共有した Google Drive 上の写し（Project LINKS の公開データ） |

利用規約は、出典を記載すれば複製・再配布を認めているので、元のファイルをそのままコミットします（[判断の記録 A-21](../../docs/09-assumptions-and-open-questions.md#a-21)）。

## ディレクトリとファイル

| # | データ | ディレクトリ | ファイル | ETL で使うか |
|---|---|---|---|---|
| D1 | 離島振興対策実施地域一覧 | `ritou-list/` | `01_ritoushinkoutaisakujisshichiikiichiran.csv`・`99_ritoushinkoutaisakujissichiikiichiran_dataspecificationdocument.xlsx` | 使う（CSV） |
| D2 | 離島振興計画書（全体情報） | `shinkou-keikaku/` | `01_zentaijouhou.csv`・`99_ritousinkoukeikakusho_dataspecificationdocument.xlsx` | 使う（CSV） |
| D2' | 離島振興計画書（所属市町村） | `shinkou-keikaku/` | `02_shozokushichouson.csv` | 使わない |
| D3 | 離島活性化交付金等事業計画書 | `kasseika-koufukin/` | `01_ritoukasseikakoufukintoujigyoukeikakusho.csv`・`99_ritoukasseikakouhukinntoujigyoukeikakusho_dataspecificationdocument.xlsx` | 使わない |
| D4 | 離島広域活性化事業 | `kouiki-kasseika/` | `01_zentaijouhou.csv` … `04_koufukinnoshikoujoukyou.csv`・`99_ritoukouikikasseikajigyou_dataspecificationdocument.xlsx` | 使わない |

- ETL が読むのは D1 と D2 の CSV だけです。D2'・D3・D4 は、今後の拡張（P2）のために置きます
- D2 と D4 は、どちらもファイル名が `01_zentaijouhou.csv` なので、データセットごとにディレクトリを分けます

## SHA-256

置いたファイルのバイト列のハッシュです。`data/raw/` で `sha256sum -c` に渡せる形にしてあります。

```text
d1f027c1e88f495c058dd969ab8748e33e47f6097452331b556a48d9638e7bca  ritou-list/01_ritoushinkoutaisakujisshichiikiichiran.csv
b7f8f75279b027c9bdc3086762a868c54912da32e53ed574024765e78a3fd483  ritou-list/99_ritoushinkoutaisakujissichiikiichiran_dataspecificationdocument.xlsx
bba4cb39f64a9ded171adfad3f31cd8e3f6eb0b4597d4eb674eebf4791aba912  shinkou-keikaku/01_zentaijouhou.csv
b010707e2ca5be405074ac664af938c33d1012cedda8b57a34f65d2f28350d16  shinkou-keikaku/02_shozokushichouson.csv
d796df22fe3f935d6804f1d41df47fbf33318c4a829e6b0d07bdac328e863530  shinkou-keikaku/99_ritousinkoukeikakusho_dataspecificationdocument.xlsx
826af6e4331249872d4bc0c3ac6c0b37fede71f4f453087b4cf4c8ed51f5ff16  kasseika-koufukin/01_ritoukasseikakoufukintoujigyoukeikakusho.csv
95c5ad7a1a07dfc1b90dc57cf003f3d44f45752d7b6f50aae8b43e099428e8ca  kasseika-koufukin/99_ritoukasseikakouhukinntoujigyoukeikakusho_dataspecificationdocument.xlsx
b8b34d33f5cf021e43140e8a530f595b6d9c4fe5c0e949c96891c47d0540e6de  kouiki-kasseika/01_zentaijouhou.csv
b51eebbffbec4e6a7a21505cc14ed42e788cc40ebd960becf249f9db57ab0a3c  kouiki-kasseika/02_zenkeikakunoseikamokuhyou.csv
01868b310beb413aca5fc5d2ab1fdc0d5359b422d60bf5af5a74bea07a02b44c  kouiki-kasseika/03_kikanjigyou.csv
c8aa1bd719683c66fed4620cfb411a95378b308d4de3e1773895032b7941f8cf  kouiki-kasseika/04_koufukinnoshikoujoukyou.csv
ee80531a28ccc6f25f1ef4b562dd31dd15083c06ff433b0f70c255c876cd3226  kouiki-kasseika/99_ritoukouikikasseikajigyou_dataspecificationdocument.xlsx
```

## 加工の有無

- 元のファイルは**加工せずに**置きます（文字コード・BOM・改行を変えない）
- 加工（島の特定・最新の基準日の選択・複数の自治体の合計・計画書の文章の取り込み）は ETL で行い、結果を `data/islands.json` に書きます。加工の内容は [データ仕様 §2.5](../../docs/07-data-spec.md#25-etlservercmdislandsdata) にあります
- 使う島と出典の情報は `data/islands-select.yaml`、計画書から人が抜き出した値は `data/curated/*.yaml` に書きます

## クレジット

クレジット画面と [`NOTICE`](../../NOTICE) に、次の文言を載せます（[データ仕様 §6](../../docs/07-data-spec.md#6-クレジット表示)）。

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

## 置くときの手順

1. 提供データの CSV とデータ定義書（xlsx）を、上の表のディレクトリにそのまま置く（ファイル名を変えない）
2. この README の「取得日」と「取得元」を書く
3. `data/islands-select.yaml` の `generatedAt` を、生成する日（`YYYY-MM-DD`）にする
4. リポジトリのルートで `data/islands.json` を生成する

   ```bash
   go -C server run ./cmd/islandsdata \
     --raw ../data/raw --curated ../data/curated \
     --select ../data/islands-select.yaml --out ../data/islands.json
   ```

5. テストを実行する。提供データがあると、実データの確認（`TestRepoRealData`）も動く

   ```bash
   go -C server test -race -count=1 ./cmd/islandsdata/...
   ```

6. 提供データ・`data/islands.json`・`data/islands-select.yaml` をいっしょにコミットする

## 再生成と確認

`data/raw/`・`data/curated/`・`data/islands-select.yaml` のどれかを変えたら、上の手順 4 で `data/islands.json` を作り直します。`--check` を付けると、ファイルを書かずに、`data/islands.json` が最新かどうかを確かめます（同じなら終了コード 0、違う・ないなら 1）。

```bash
go -C server run ./cmd/islandsdata \
  --raw ../data/raw --curated ../data/curated \
  --select ../data/islands-select.yaml --out ../data/islands.json --check
```
