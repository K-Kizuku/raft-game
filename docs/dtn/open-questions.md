# 未確定のこと・測定や PoC が要ること

DTN-01（#158）で、この一覧の「DTN-01 で決める」ものに結論を書いてから Accepted にする。他は、担当の Issue の中で測定し、結果をここに書き足す。推測で「確認済み」と書かない。

| ID | 問い | 今の仮の決定 | 決める場所 |
|---|---|---|---|
| Q-PERF | 1 回の上限の往復（Coordinator → DtnSession → ActorHost → Container → 戻り）にかかる実時間。等倍（ループ 50 ms）・速度 2 倍・「次の出来事まで」で遊べるか | 50 ms のループのまま。遅ければループを 100〜200 ms にする・関係する島だけを進める | DTN-11 で概算、DTN-12 で実測 |
| Q-COLD | 13 個の Container を並行に起動したときの起動時間（demo-izu）。`start_timeout` をいくつにするか | 今の起動の上限を Actor ごとに使い、全体の上限を別に置く | DTN-12 |
| Q-ACCOUNT | 同じ Cloudflare のアカウントで、DTN の環境が Container の同時の数・メモリの合計などのアカウントの上限を使い切り、発表の prod・demo に影響しないか | 発表の前後は DTN の環境の受付を止める（管理用 API）。必要なら別のアカウントにする | DTN-12 の前にオーナーが決める |
| Q-IMAGE | 1 つの image（`dtnactor`）を、複数の Durable Object のクラス（Coordinator 用・ActorHost 用）で使えるか | 使えなければ、役割ごとに image を分ける | DTN-12 |
| Q-RPC | ActorHost どうしの Durable Object の呼び出しの、1 回の大きさの上限・遅さ | 1 つの transfer は積載の上限（最大 16 通 × 16 KiB、base64 で約 342 KiB）で、1 つの電文（1 MiB）に収まる | DTN-09 で概算、DTN-12 で実測 |
| Q-SQLITE | DO の SQLite の 1 行・1 トランザクション・合計の上限が、§6 の上限（8 MiB / Actor）に収まるか | 収まる前提 | DTN-12 |
| Q-COORD | Coordinator を Container にする（C1）のでなく、DO の中の WASM（C2）にできるか | C1 で進める。C2 は費用を減らす後続の候補 | DTN-12 の後 |
| Q-MOQ-1 | 利用時点の Cloudflare の MoQ の Relay が対応するドラフトと、Go のクライアント | 未調査 | DTN-14 |
| Q-MOQ-2 | `enableInternet: false` のまま MoQ を使えるか。使えないなら、どこを接続の主体にするか（Worker・別の Gateway） | Container からは直接つながない前提で比べる | DTN-14 |
| Q-MOQ-3 | Relay のトークンの権限の細かさ・期限・漏れる経路 | Namespace ごとの厳密な認可があるとは仮定しない。受け取り側で必ず Contact を確かめる | DTN-14 |
| Q-TRACE | 正規化トレースの `global` の比べ方（時刻ごとの集合）で、見逃すずれがないか | [simulation-time.md §5](./simulation-time.md#5-正規化トレース同等性の基準) のとおり | DTN-00 で参照モデルどうしを壊して確かめた（`server/internal/simtrace` の試験）。検出する: 1 つの島の列の中の順の入れ替え・手紙の時刻のずれ・`global` のイベントの時刻のずれ・欠け・重複・中身の違い・スコアの違い。見逃す（設計どおり）: 同じ時刻の中の `global` の順、同じ時刻の途中の `score.updated`、列をまたぐ同じ時刻の前後。同じ時刻の中の順の違いが最後の状態に効く依頼と島は、§5.3 の 4. の最終の状態で検出する（島の最後の `island.updated` を DTN-00 で足した） |
| Q-SCORE | 同じ時刻に複数の島で依頼の結果が出たときのスコアの途中経過が、参照モデルと一致するか | [simulation-time.md §3](./simulation-time.md#3-島の出力をまとめる順序) の順で一致する見込み | DTN-05 の試験 |
| Q-AGE | Bundle Age の更新のために `bundle.export` を挟むことで、積む操作の往復が 1 つ増える。許容できるか | 許容する（積む操作は人の操作の速さ） | DTN-08 |

## DTN-12 の実測（2026-11-08 の時点）

実環境（`raft-dtn.kizuku-hackathon.work`）はまだない（H3〜H5 はオーナーの作業、`server/cmd/dtnactor` の Coordinator は DTN-05）。下の測定は**未実施**。測り方は [environment.md §7](./environment.md#7-計測の入口管理者だけdtn-12) の計測の入口で、stage1 と demo-izu を 5 回ずつ。

| ID | 測るもの（計測の入口の値） | 結果 |
|---|---|---|
| Q-COLD | `startup.readyMs`・`startup.hosts`（demo-izu の 13 個の並行起動）。`DTN_START_TIMEOUT_MS` の既定は仮に 90 秒 | 未実施 |
| Q-IMAGE | 2 つの Container のアプリケーション（`raft-dtn-coordinator`・`raft-dtn-actor`）が、同じ `Dockerfile.dtnactor` から作った image を共有できるか（`cf deploy` の出力・ダッシュボード） | 未実施。ローカルの `cf build --mode dtn` は本物の image を作れず（Docker Hub の 429・`cmd/dtnactor` の Coordinator が未完成）、出力の形と `cf deploy --prebuilt --dry-run` だけを確かめた |
| Q-RPC | `handoff`（`transfer.prepare` の dispatch の実時間）と、16 通 × 16 KiB の transfer が通るか | 未実施 |
| Q-SQLITE | `storage.hosts[].status.storedBytes`・`journalRows` が §6 の上限（8 MiB・20,000 行）に収まるか | 未実施 |
| Q-PERF | `dispatch.p50Ms`・`p95Ms`（等倍・速度 2 倍） | 未実施 |
