# T-007: Cloudflare スパイクの実行記録

- 対象: [Issue #15 (T-007)](https://github.com/K-Kizuku/raft-game/issues/15)
- 測定コードの準備: [Issue #134 (T-007a)](https://github.com/K-Kizuku/raft-game/issues/134)
- 記録用ブランチ: `agent/issue-15`
- 開始日: 2026-11-07 (JST)
- 状態: H-2・H-3 完了。検証用 Worker・DO・コンテナの構成をデプロイし、healthz が応答。次は H-4 の lite の測定。クラウドのコンテナ起動・性能はまだ未測定。
- 実行: Kizuku が手元の端末でコマンドを実行し、AI が共有された出力の確認・次の手順の案内・記録を担当する。
- 最終成果: 実測結果と後片付けの確認を記録し、[ADR-0016](../adr/0016-infrastructure-cloudflare-with-cf-cli.md) に測定結果・判断を反映した PR を作る。

## 記録の扱い

ユーザーから共有された出力と、GitHub で確認した状態を区別する。未確認の項目を完了扱いにしない。認証トークン・シークレット・認証用 URL は記録しない。Docker の出力は関連する環境情報に絞り、ホスト名・ID・ローカルのパス・プロキシ設定は転載しない。

## 進捗

| 手順 | 状態 | 確認できたこと / 残り |
|---|---|---|
| H-1 準備 | 完了 | main d862bb78cbfea3fe0e476605b3b6d4996153f818、pnpm install --frozen-lockfile と check.sh が成功。shellcheck は未導入のため省略 |
| M-12 ローカル動作確認 | 成功 | 必須 8 項目すべて ok、measure.ts 終了コード 0。cf dev の準備完了 6 秒。TCP の外部接続が通る点はクラウドで再確認 |
| M-2・M-3 手元の基準値 | 取得済み | Apple M2 / Go 1.26.9。30 分の再計算 79.343 ms、8 倍速 step p99 0.289 ms。クラウドの性能とは区別する |
| H-2 トークンの準備 | 完了 | gitignore を確認し、64 桁 hex を mode 600 / 新規作成専用で保存。デプロイ時は hidden と表示 |
| H-3 検証環境へのデプロイ / M-13 | 完了 | 所要 33.77 秒。raft-game-spike.kizuku.workers.dev/spike/healthz が ok: true |
| H-4 測定 / M-1〜M-14 | 未実施 | 次に lite / --first 1 / --runs 10。standard-1 と外部通信の対照はその後に行う |
| H-5 limits.cpuMs の判断 | 未実施 | Worker・Durable Object の CPU 時間と WS 中継を確認する |
| H-6 後片付け・費用確認 | 未実施 | 停止・資源の削除・翌日の請求確認まで記録する |
| H-7 ADR の更新 | 未実施 | 実測値・制約・判断を記載する |
| H-8 後続への引き継ぎ | 未実施 | 対象 Issue と ADR に測定結果を伝える |

## 2026-11-07: 初回の環境確認

### ユーザーが実行したコマンド

raft-game の main ブランチで実行。共有されたプロンプトの開始時刻は 13:50:47。

```fish
node --version
pnpm --version
docker info
pnpm -C web exec cf --version
pnpm -C web exec cf auth whoami
```

### 共有された結果

| 項目 | 結果 | 判定 |
|---|---|---|
| Node.js | v24.22.0 | web/package.json の node >=22.23.0 を満たす。ADR-0016 に記載された cf の ^24.22.0 にも合致する |
| pnpm | 9.15.0 | ルート package.json の packageManager と一致 |
| cf | 1.0.0-beta.28 | web/package.json の固定バージョンと一致 |
| Docker Client / Server | 24.0.6 / 24.0.6 | daemon への接続成功 |
| Docker の環境 | Docker Desktop、desktop-linux、Linux aarch64、8 CPUs、12.66 GiB | 起動を確認。linux/amd64 イメージのビルド・実行可否は未確認 |
| Cloudflare 認証 | authenticated: false / Not logged in | 未認証。H-1 は未完了 |

認証コマンドの出力:

```json
{
  "authenticated": false,
  "error": "Not logged in"
}
```

Docker は `WARNING: daemon is not using the default seccomp profile` を表示した。daemon は応答しているため、現時点ではこの警告だけを理由に停止せず、check.sh・ローカル動作確認の結果で影響を確認する。

### GitHub で確認した状態

確認時の main は `dd17df2090c49ed9b7bd0ad2ab804367424f895c`。

- Issue #15 は open / status:backlog / needs-human。手順は H-1〜H-8 に更新され、測定コード #134 のマージが前提になっている。
- Issue #134 は open / status:in-progress。確認時の main に `spike/` は存在しない。
- したがって、`spike/scripts/check.sh` の実行・検証環境へのデプロイ・測定にはまだ進まない。
- ユーザーの手元の main のコミット SHA は未確認。上記 SHA と同一だったとは扱わない。

### 初回確認時に案内した次の手順

リポジトリのルートで OAuth のログインを行い、正しいアカウントに認証できたか確認する。

```fish
pnpm -C web exec cf auth login
pnpm -C web exec cf auth whoami
```

その後、Workers の有料プランと workers.dev サブドメインの存在を確認する。#134 が main にマージされたことを確認してから、手元を更新し、`pnpm install` と `bash spike/scripts/check.sh` に進む。

## 2026-11-07: 認証の結果と、#134 を待つ間の準備

### ユーザーから共有された結果

共有されたプロンプトの開始時刻は 14:39:53。リポジトリの main ブランチで、前節の `cf auth login` と `cf auth whoami` を実行した。

| 項目 | 共有された結果 | 判定 |
|---|---|---|
| cf auth login | You are already logged in. | 実行時にはログイン済みだった |
| authenticated | true | 認証済み |
| tokenValid | true | 実行時点で有効 |
| 認証方法 | OAuth token | OAuth 認証を確認。保存先のローカルパスは転載しない |
| accounts | 1 件、type: standard | アカウントの列挙に成功。standard は Workers の有料プランの証拠にはしない |
| 関連する scopes | workers:read / workers:write / containers:write / cloudchamber:write など | 出力に含まれることを確認。実デプロイの成功や契約状態は未確認 |
| 表示されたトークン期限 | 2026-11-07 15:39:51 JST | 実行時点の表示。以後の認証状態を保証するものではない |

メールアドレス・アカウント名・アカウント ID・全スコープの一覧は公開記録に転載しない。トークンそのものは共有されていない。

### ユーザーと確認した進め方

- #134 は別の AI エージェントが実装中。こちらは環境の準備・測定の支援・記録を担当する。
- スパイクは `spike/` 内の独立した構成で、Worker は `raft-game-spike`、Durable Object は 3 クラス、Go コンテナと測定スクリプトを用意する。アカウントの workers.dev を使い、ゾーンの設定には触れない。
- 8 倍速・復元の測定はスパイクの負荷模型による予備検証であり、本番のゲームのコードで性能を満たしたことにはしない。
- 実際のアプリの検証は T-306・T-308・T-601・T-603 がそろった後の [T-605 / #69](https://github.com/K-Kizuku/raft-game/issues/69) で行う。
- #134 のコードが必要になる直前まで、下記の準備を進める。

### #134 のマージ前に確認する項目

| 項目 | 方法 | 状態 |
|---|---|---|
| Workers の有料プラン | `cf accounts subscriptions get` の結果で Workers の契約を確認。権限不足や不明な結果なら、ダッシュボードの Workers の契約表示で補足する | 初回の一覧にはなかったが、その後ユーザーが Workers Paid を有効化したと報告 |
| workers.dev のサブドメイン | 対象アカウントの Workers & Pages に表示される `<サブドメイン>.workers.dev` を読む | kizuku.workers.dev (ユーザー報告) |
| linux/amd64 のビルド環境 | `docker buildx inspect` の Platforms を確認。実際のスパイクのイメージのビルド・実行は #134 の check.sh で確認する | alpine の実行に続き、check.sh でスパイクのイメージのビルドと linux/amd64 を確認 |
| ローカルの Go | `go version`。#134 の仕様の go.mod は `go 1.26`。測定用のローカル実行に使えるツールチェーンか確認する | 既定は go1.25.5。コマンド単位で指定した go1.26.9 の取得・起動に成功 |
| トークン生成用の OpenSSL | `openssl version`。この段階ではシークレットを生成しない | OpenSSL 3.4.0 の起動を確認 |
| ユーザーの手元のコミット | `git rev-parse HEAD`。GitHub で確認した main の SHA と区別して記録する | 31d8b909d620c35c252e2108aa753eab0fa5d8a1 |

次にユーザーがリポジトリのルートで実行するコマンド:

```fish
pnpm -C web exec cf accounts subscriptions get
docker buildx inspect
go version
openssl version
git rev-parse HEAD
```

`cf accounts subscriptions get` は [Cloudflare の cf 1.0.0-beta.28 の実装](https://github.com/cloudflare/cf/blob/b0703f68cf9ec67f515055d42d7ad128241649ed/packages/cli/src/commands/_generated/accounts/subscriptions/get.ts) で、アカウントの契約を読む GET コマンドであることを確認した。ドメインの Free / Pro プランとは区別する。403 などで読めない場合も、有料プラン未契約とは判定しない。

workers.dev の表示場所は [Cloudflare の公式手順](https://developers.cloudflare.com/workers/configuration/routing/workers-dev/) の Workers & Pages / Your subdomain を参照する。表示を確認するだけで、変更は行わない。

### #134 が必要になる境界

上記の前提条件が確認できたら、#134 のマージを待つ。マージ後に手元の測定用の作業ツリーを最新化し、`spike/README.md` の実装済みの手順を確認してから、`pnpm install` と `bash spike/scripts/check.sh` に進む。

その後のローカル動作確認、シークレットの生成と gitignore の確認、spike/ からのビルド・デプロイは、実装された README の手順に合わせて順に案内する。ここまでの出力で H-1 全体を完了扱いにはしない。

## 2026-11-07: 契約・Docker・Go・OpenSSL の確認

共有されたプロンプトの開始時刻は 14:57:50。前節の 5 コマンドの出力をユーザーから受け取った。

### 共有された結果

| 項目 | 結果 | 判定 |
|---|---|---|
| アカウントの契約一覧 | Teams Free Base (teams_free) と R2 Paid (r2_paid) の 2 件。全 1 ページ、total_count: 2 | 一覧に Workers Paid はない。Workers の契約をダッシュボードで確認する |
| Docker builder | desktop-linux、driver: docker、status: running、BuildKit v0.11.6+616c3f613b54 | builder は稼働中 |
| builder の Platforms | linux/arm64, linux/riscv64, linux/ppc64le, linux/s390x, linux/386, linux/mips64, linux/arm/v7, linux/arm/v6 | linux/amd64 が含まれない。実行可能かはまだ試していない |
| Go | go1.25.5 darwin/arm64 | #134 の仕様の go 1.26 より古い。ただし自動ツールチェーン選択の設定・1.26 以上のダウンロード可否は未確認 |
| OpenSSL | OpenSSL 3.4.0 22 Oct 2024 / Library: OpenSSL 3.4.0 22 Oct 2024 | コマンドの起動を確認。シークレットはまだ生成していない |
| 手元の HEAD | 31d8b909d620c35c252e2108aa753eab0fa5d8a1 | ユーザーの端末の出力として記録。最新の main と同一かは未確認 |
| workers.dev | 今回の共有には含まれない | 引き続き未確認 |

契約の state が Paid でも、Teams Free Base や R2 Paid を Workers Paid と同一視しない。契約 ID・外部課金 ID・支払い関連フィールドは公開記録には転載しない。

### 次の手順

1. 対象アカウントのダッシュボードで Workers の契約を確認し、Workers Paid が未契約なら有効にする。Containers には Workers Paid が必要で、基本料金は月 5 USD、利用状況に応じて追加の従量料金が発生する。あわせて workers.dev のサブドメインを確認する。
2. Go 1.26 系のツールチェーンをコマンド単位で指定して起動する。確認した [Go の配布一覧](https://go.dev/dl/) の 1.26.9 を使い、既存の Go のグローバル設定は変えない。[GOTOOLCHAIN の公式説明](https://go.dev/doc/toolchain) に従い、未取得なら Go コマンドがダウンロードする。
3. linux/amd64 の軽量なコンテナを 1 回実行して、エミュレーションが機能するかを確認する。builder の表示だけでは故障箇所を断定しない。Docker の設定変更や再起動は、実行結果を見てから判断する。

```fish
env GOTOOLCHAIN=go1.26.9 go version
docker run --rm --platform linux/amd64 alpine:3.22 uname -m
```

期待する出力はそれぞれ `go version go1.26.9 darwin/arm64` と `x86_64`。これは次に案内したコマンドであり、まだユーザーの実行結果は受け取っていない。Go の取得やイメージの pull のエラーが出た場合も、ツールチェーンやエミュレーションの問題と区別して記録する。

Docker の確認用コンテナは `--rm` で実行終了時に消える。取得した `alpine:3.22` のイメージは残り得るので、測定用資源の後片付けとは区別する。スパイクのイメージのビルド・実行確認と性能測定は未実施。

参考: [Containers の提供プラン](https://developers.cloudflare.com/containers/)、[Workers の料金](https://developers.cloudflare.com/workers/platform/pricing/)、[Docker のエミュレーション](https://docs.docker.com/build/building/multi-platform/)。

## 2026-11-07: Go 1.26.9 と linux/amd64 の実行確認に成功

共有されたプロンプトの開始時刻は 14:57:59。ユーザーが以下を実行した。

```fish
env GOTOOLCHAIN=go1.26.9 go version
docker run --rm --platform linux/amd64 alpine:3.22 uname -m
```

### 実行結果

```text
go: downloading go1.26.9 (darwin/arm64)
go version go1.26.9 darwin/arm64
Unable to find image 'alpine:3.22' locally
3.22: Pulling from library/alpine
710f3da1112f: Pull complete
Digest: sha256:bfe7ceb9f09ec0566c48fb93356e45a47a00be5a1dd14fa5b909e7709c66b034
Status: Downloaded newer image for alpine:3.22
x86_64
```

| 確認事項 | 判定 |
|---|---|
| Go 1.26 系の取得と起動 | go1.26.9 darwin/arm64 のダウンロードと起動に成功 |
| linux/amd64 コンテナの取得と実行 | alpine:3.22 の pull に成功し、uname -m は x86_64 を返した |
| Docker の設定変更 | 今回の実行確認のための変更は不要。builder の Platforms 表示だけで実行不可とは判定しない |
| スパイクのイメージのビルド・実行 | 未実施。#134 のマージ後に check.sh とローカル動作確認で調べる |
| Go の既定バージョン | env によるコマンド単位の指定なので、既定を 1.26.9 に変更したとは扱わない |

後続のローカルの Go コマンドは、必要に応じて同じ `env GOTOOLCHAIN=go1.26.9` を付ける。スクリプトが Go を呼ぶ場合はスクリプトにもこの環境変数を渡す。実装された go.mod・README に別の指定がある場合は、実行前に整合を確認する。

確認用の alpine:3.22 は今回初めてローカルに取得された。コンテナは `--rm` により終了時に削除されるが、イメージの削除はまだ実行していない。上記 digest は pull の出力として記録し、amd64 専用のイメージ manifest の digest とまでは断定しない。

### 次に必要な確認

- 対象アカウントの Workers Paid の契約状態。前回の契約一覧には Workers Paid がなく、有効化したとの報告もまだない。
- 対象アカウントの workers.dev のサブドメイン。

この 2 点が確認できれば、#134 のコードが必要になる前の環境確認は一通りそろう。H-1 全体の完了には、マージ済みのスパイクでの依存のインストールと check.sh の成功が必要。

## 2026-11-07: Workers Paid の有効化と workers.dev の確認

ユーザーから「Workers Paid を有効にしました」「kizuku.workers.dev」と報告を受けた。API の再取得結果ではなく、ユーザーが行った操作・確認として記録する。

| 項目 | 状態 |
|---|---|
| Workers Paid | 有効化済み (ユーザー報告) |
| アカウントの workers.dev サブドメイン | kizuku.workers.dev (ユーザー報告) |
| 検証用 Worker の予定 URL | https://raft-game-spike.kizuku.workers.dev |
| スパイクの実デプロイ・URL の応答確認 | 未実施。予定 URL を稼働済みとは扱わない |

### #134 の進捗を GitHub で確認

- Issue #134 は open / status:in-progress。
- 実装の [PR #138](https://github.com/K-Kizuku/raft-game/pull/138) が作成されている。
- 確認時は open / draft: false / merged: false。
- head は `agent/issue-134`、SHA は `4dd737453f9156ad47e9beef5fc170689be644e1`。
- こちらではこの PR のマージ・デプロイ・測定は実行しない。

### 現在の到達点と次の手順

#134 が必要になる前の環境準備はそろった。Node・pnpm・cf、Docker daemon、OAuth、Go 1.26.9 の起動、linux/amd64 コンテナの実行、OpenSSL、Workers Paid、workers.dev の確認が済んでいる。

次は PR #138 のマージ待ち。マージ後に `spike/README.md` と実装されたスクリプトを確認し、ユーザーの測定用作業ツリーを最新化してから、`pnpm install` と `check.sh` を案内する。Go を呼ぶ手順では、必要に応じて `env GOTOOLCHAIN=go1.26.9` を付ける。

H-1 全体はまだ途中であり、依存のインストール・スパイクのビルド・構成の検査・dry-run は未実施。シークレットの準備 (H-2) と実デプロイ (H-3) にもまだ進んでいない。

## 2026-11-07: #134 のマージ後、最新の #15 とスクリプトを確認

### 確認したリビジョンと状態

- [PR #138](https://github.com/K-Kizuku/raft-game/pull/138) は merged。マージ時刻は 2026-11-07 15:29:53 JST、マージコミットは `4073ed97f8253f072312273cff76fe119898aa86`。
- 確認時の main は `d862bb78cbfea3fe0e476605b3b6d4996153f818`。この SHA の README・構成・スクリプトを読んだ。
- 最新の Issue #15 を再取得。open / status:backlog / needs-human、本文の更新日時は 2026-11-07 14:27:49 JST (`2026-11-07T05:27:49Z`)、コメントは 0 件。手順は H-1〜H-8、チェックは未完了のまま。
- `spike/README.md`・`spike/scripts/check.sh`・`spike/scripts/local-smoke.sh`・`spike/scripts/measure.ts`・`spike/scripts/lib.ts`・`spike/cloudflare.config.ts`・`spike/package.json`・`spike/container/go.mod`・`spike/container/Dockerfile` を確認した。ここでコードを読んだことは、ユーザーの環境での実行成功とは区別する。

### 解決までの順序

| 段階 | 実施すること |
|---|---|
| H-1 と M-12 | 手元の main を更新し、固定した依存をインストール。check.sh に続き、DEV_SLOT を確保して local-smoke.sh と手元の Go の基準値を確認する |
| H-2〜H-3 | 検証用トークンを作り、gitignore を確認。spike/ からビルド・実デプロイし、時間・イメージの大きさ・URL を記録する |
| H-4〜H-5 | lite と standard-1 の測定、M-9 の対照、Workers Logs の CPU 時間を確認する。limits.cpuMs・起動待ち・インスタンスの判断を整理する |
| H-6 | 使った番号の停止を確認し、検証用のクラウド資源とローカルの不要なものを削除する。翌日の費用も確認する |
| H-7〜H-8 | ADR-0016 と必要な関連文書を更新して PR を作成し、後続タスクへ実測結果を引き継ぐ |

### 次にユーザーが実行するコマンド (H-1)

リポジトリのルートで fish から実行する。各段階を `and` でつなぎ、失敗時に後続へ進まないようにする。

```fish
set -gx AGENT_ROLE human
git switch main
and git pull --ff-only origin main
and git rev-parse HEAD
and pnpm install --frozen-lockfile
and env GOTOOLCHAIN=go1.26.9 SPIKE_CPU_MS=50 bash spike/scripts/check.sh
```

`check.sh` は型検査・TS テスト・Go の整形検査/vet/race テスト・Docker の確認・cf build・構成の検査・linux/amd64 の検査・`cf deploy --prebuilt --dry-run` を行う。shellcheck は手元にある場合に行う。実デプロイは含まない。

Go は手元で取得済みの 1.26.9 を環境変数で渡す。Dockerfile の Go は実装で固定された 1.26.4 なので、手元の基準値とコンテナの値の比較では版の違いも記録する。

これらは次に案内したコマンドで、まだユーザーから実行結果は受け取っていない。check.sh の最後の `check: すべて成功` とバインディング・コンテナの表、または失敗した箇所を受け取って判断する。

### 実装を読んで補う注意点

- **M-9 の番号**: 最新の #15 の H-4 は `--only egress --internet-control` に `--first` を書いていない。README の注意に従い、初回の通常測定で使っていない `--first 10` を補う。初回の標準的な割り当ては lite が s-1〜s-9、standard-1 が s-16〜s-24。結果の sessions を確認し、再試行で使った番号があれば割り当てを見直す。
- **再実行**: 同じ Durable Object は起動 3 回まで。measure.ts の SessionPool はその実行の中の回数だけを数えるため、別の実行で消費した回数は把握しない。失敗した測定を同じ番号でそのまま繰り返さない。
- **local-smoke.sh**: DEV_SLOT が必要。DOCKER_HOST が未設定なら Docker context のソケットを自動で渡すので、最初から手で固定する必要はない。実行後は spike/.cloudflare/local-smoke.json と終了時の Docker の残存一覧を受け取る。
- **CPU の判定**: ローカルで CPU の反復が成功したことだけで本番の limits.cpuMs を決めない。Cloudflare 上の測定と Worker/DO の Logs で CPU 時間を確認する。
- **測定の成功フラグ**: egress は応答を取得できれば ok: true を返すため、result.ok だけで外部通信の遮断を判断しない。DNS・TCP・HTTPS の各結果と internet0AllFailed を読む。同様に各項目の実測値を読んで設計判断する。
- **未確定の基準**: #15 の提案 (復元 5 秒) と現行 NFR-03 (復元 3 秒) の差は残っている。両方との比較を記録し、提案値を承認済みとして扱わない。
- **測定データ**: .cloudflare/ は無視される場所なので、結果の JSON と表を共有してもらい、必要な実測値をこの記録と最終 ADR に残す。

## 2026-11-07: check.sh に成功、H-1 完了

ユーザーが前節で案内したコマンドを実行した。共有されたプロンプトの開始時刻は 15:47:47 JST、Vitest の開始は 15:48:25。

### 確認できた結果

| 項目 | 結果 |
|---|---|
| AGENT_ROLE | ユーザーが human を設定 |
| git switch / pull | main のまま。origin/main と同期済み |
| 手元の HEAD | d862bb78cbfea3fe0e476605b3b6d4996153f818 |
| pnpm install --frozen-lockfile | 全 3 workspace projects。lockfile の更新なし、成功 |
| 型検査 | tsc -b、cf workers types、tsc -p tsconfig.workerd.json が成功 |
| TypeScript テスト | 6 ファイル、137 件すべて成功 |
| Go | 整形検査・vet・test -race が成功。container と loadmodel の両パッケージが ok |
| Docker | daemon の確認が成功 |
| cf build | 成功。Docker のビルド表示は 5.7 秒、主要レイヤーはキャッシュ済み |
| ビルド結果の構成検査 | check-config が ok: true |
| イメージのプラットフォーム | linux/amd64 |
| cf deploy --prebuilt --dry-run | 成功。実デプロイは行っていない |
| shellcheck | 未導入のためスクリプトの仕様どおり省略。実行済みとは扱わない |
| check.sh の最終行 | check: すべて成功 |

Vitest の内訳: check-config 17、lib 33、relay 8、stores 1、router 61、session 17。

生成されたイメージは `cloudflare-build/b8366d8bfb35/raft-game-spike-server-server:c537d571e688`。ビルド表示のイメージ ID は `sha256:03d889b3fe26b5bd2ebaa766ab30caf01eaf5bfb76c49fefb17edf3d8fd107a7`。大きさはこの出力にはないため未記録。

cf build は workspace の作業ディレクトリ直下に lock file がない旨の警告を出したが、ルートでの frozen-lockfile インストール・ビルド・構成検査・dry-run は成功している。この警告だけを理由に設定を変更しない。

### dry-run の記録

```text
Total Upload: 27.32 KiB / gzip: 8.83 KiB
env.SESSIONS (SpikeSession, defined in raft-game-spike)    Durable Object
env.PLAIN (PlainStore, defined in raft-game-spike)         Durable Object
env.CONTROL (ControlStore, defined in raft-game-spike)     Durable Object
env.SPIKE_MAX_SESSIONS ("30")                             Environment Variable

The following containers are available:
- SpikeSession (durable_object)

--dry-run: exiting now.
```

H-1 の完了として記録する。シークレットの作成・実デプロイ・Cloudflare 上での動作確認は、まだ行っていない。

### 次の手順: M-12 のローカル動作確認

ユーザーの実行した SHA の `scripts/dev-slot.sh` と `scripts/dev-env.sh` を読み、acquire がこの作業ツリー用の番号を返し、既存の枠があればそれを再利用することを確認した。親の fish の PID を記録して枠を確保する。

リポジトリのルートで実行する:

```fish
set -gx DEV_SLOT (bash scripts/dev-slot.sh acquire --pid $fish_pid)
and env GOTOOLCHAIN=go1.26.9 SPIKE_CPU_MS=50 bash spike/scripts/local-smoke.sh
```

この段階では cf dev での起動・WS の往復と超過の破棄・SQLite・PlainStore/ControlStore・停止を確認する。egress・crash・cpu はスクリプト上は記録用の項目なので、その失敗と必須項目の失敗を分けて読む。

結果は `spike/.cloudflare/local-smoke.json`。まず終了までのコンソール出力を受け取り、必要に応じて JSON の内容も確認する。終了時の cf dev の停止と Docker の残存一覧も記録する。DEV_SLOT の番号とスパイクが使うポートは実行出力から確認する。

このコマンドは次に案内したもので、まだ実行結果は受け取っていない。枠の解放や、残ったコンテナ・イメージの削除は使用状況を確認してから行う。

## 2026-11-07: local-smoke.sh 成功 (M-12)

共有されたプロンプトの開始時刻は 15:48:53 JST。ユーザーが DEV_SLOT を確保し、案内済みの local-smoke.sh を実行した。

- cf dev の準備完了は **6 秒**。
- 使用したポートは **15184**。スクリプトの式 (15173 + DEV_SLOT × 10 + 1) から DEV_SLOT は **1** と読み取れる。番号そのものを echo した出力はない。
- DOCKER_HOST は Docker context の Unix ソケットから自動設定された。ユーザーのローカルパスは転載しない。
- ローカルで使った番号は **s-1〜s-5**、PlainStore/ControlStore はスクリプトの p-1。
- measure.ts の終了コードは **0**。ローカルの必須 8 項目はすべて ok。
- これはローカルの結果であり、Cloudflare 上での測定とは区別する。表示が instance=lite でも、コンテナから見た資源に CPU・メモリの上限はない。

### 共有されたローカルの測定値

| ID | 項目 | ローカルの結果 / 確認できる範囲 |
|---|---|---|
| M-1 | 起動 | DO 内の p50 166 / p95 593 / 最大 593 ms (3/3 回)。初回 593 ms。クライアント p50 186 ms。同時 3 つは全成功、最大 853 ms |
| M-2 | 復元相当の負荷模型 | このスモークでは未実施。次に手元の Go で基準値を測る |
| M-3 | 8 倍速 | このスモークでは未実施。次に手元の Go で基準値を測る |
| M-4 | CPU 上限 | Worker・DO とも反復 1e6、1e7、3e7、1e8、3e8、1e9 がすべて HTTP 200。CPU 制限の効果はこの結果では確認できない。Cloudflare 上で再測定する |
| M-5 | WS | 1 KiB 200 回、p50 0.6 / p99 1.7 ms。16 KiB 20/20。超過は破棄。500 件は 500/500、順序 ok。ブラウザ側切断の中継は ok、コンテナ側切断では 1011。無通信 120 秒は --idle 0 のため未実施 |
| M-6 | HTTP 往復 | 200/200 回、p50 0 / p99 1 ms。workerd の Date.now() の分解能を含む値であり、実際の遅延がゼロとは扱わない |
| M-7 | SQLite | 100 行 12.9 ms、1,000 行 38.4 ms (クライアント計測)。コンテナ再起動後も計 1100 行が残った |
| M-8 | 継承しない DO | PlainStore と継承する ControlStore がともに ok。非継承の SpikeSession の SQLite・container も使用できた。アラームの実際の発火はこのスモークでは未測定 |
| M-9 | 外部通信 | enableInternet: false で DNS 失敗、TCP 1.1.1.1:443 は ok、HTTPS は名前解決で失敗。internet=1 の対照は未実施 |
| M-10 | 停止 | destroy 30 ms、running=false。アラーム・inactivity の停止はこのスモークでは未実施 |
| M-11 | 異常終了と再起動 | 検知 123 ms、再起動 297 ms |
| M-12 | cf dev | start / ws / sql / plain / stop の必須確認が成功。egress / crash / cpu も応答を記録できた |
| M-13 | 実デプロイ | 未実施 |
| M-14 | 資源 | CPU 8、GOMAXPROCS 8、cpu.max = max 100000、memory.max = max、env MODE=single / PORT=8080、colo=NRT。ローカルの colo 表示を実配置とは扱わない |

個々の通常起動 (start の項目) は 593、166、163 ms。同時起動は 723、815、853 ms。初回以外はイメージ・実行環境のキャッシュの影響もあり得るため、本番の起動時間の判断にそのまま使わない。

外部通信のエラー本文:

```text
dns=lookup example.com on 1.1.1.1:53: no such host
tcp=ok
https=Get "https://example.com/": dial tcp: lookup example.com on 1.1.1.1:53: no such host
```

egress: ok は測定 API の応答を取得できたことを表す。**外部通信がすべて遮断されたという意味ではない**。TCP が通った事実を残し、Cloudflare 上の M-9 と対照の結果を比較する。

### 終了処理と残った Docker 資源

cf dev の停止処理が行われた。以下の中継用コンテナ 5 個は動いたまま見つかり、local-smoke.sh が docker stop した。その後、すべて停止状態の一覧に出た。

| ID | 名前の規則 | イメージ |
|---|---|---|
| ad41c5d45807 | workerd-raft-game-spike-SpikeSession-<ハッシュ>-proxy | cloudflare/proxy-everything:3cb1195 |
| b2ba10714c83 | 同上 | 同上 |
| 6dd1c6654b0c | 同上 | 同上 |
| 273d7b10be25 | 同上 | 同上 |
| e10610f6b999 | 同上 | 同上 |

停止済みコンテナの削除はまだ実行していない。削除する際は、上記 ID の対象を通常の docker rm で消す (force や prune は使わない)。

終了時に表示されたイメージ:

| イメージ | ローカルの大きさ |
|---|---|
| cloudflare-build/b8366d8bfb35/raft-game-spike-server-server:c537d571e688 | 10.5 MB |
| cloudflare-build/d708d772e6cb/raft-game-spike-server-server:248e6be09658 | 10.5 MB |
| cloudflare-dev/spikesession-server-5c07eb541ff8:6e2d46e2 | 10.5 MB |
| cloudflare-dev/spikesession-server-5c07eb541ff8:766f7f43 | 10.5 MB |
| cloudflare/proxy-everything:<none> | 21.9 MB |

これは終了時に見えた一覧であり、すべてが今回新しく作られたとは断定しない。共有の proxy-everything のイメージは、ほかの作業にも使うため消さなくてよい。DEV_SLOT の解放も未実施。

### 次の手順: 手元の基準値 (M-2・M-3)

Cloudflare の lite / standard-1 と比較するため、macOS 上のネイティブ Go の負荷模型を測る。リポジトリのルートで実行する。

```fish
sysctl -n machdep.cpu.brand_string
env GOTOOLCHAIN=go1.26.9 go -C spike/container run . bench raft --nodes 7 --minutes 30
env GOTOOLCHAIN=go1.26.9 go -C spike/container run . bench loop --nodes 7 --speed 8 --seconds 20
```

1 つ目で CPU の種類、残りの 2 つで基準値の JSON を受け取る。bench loop は約 20 秒実行する。このコマンドは次に案内したもので、まだ結果は受け取っていない。

基準値の取得後に H-2 のトークン作成と H-3 の検証環境へのデプロイに進む。ここまででは、クラウドの資源は作成していない。

## 2026-11-07: 手元の性能基準値を取得 (M-2・M-3)

共有されたプロンプトの開始時刻は 15:52:23 JST。

- CPU: **Apple M2**。
- Go: **go1.26.9 darwin/arm64** を環境変数で指定。
- 対象のリビジョンは直前の確認済み main `d862bb78cbfea3fe0e476605b3b6d4996153f818`。この測定時の SHA の再表示はないが、変更したとの報告もない。
- Docker を使わず、macOS 上の Go の負荷模型として実行。これは本番のゲームのエンジンや、クラウドの lite の性能確認ではない。

### M-2: 7 ノード・30 分相当の再計算

```fish
env GOTOOLCHAIN=go1.26.9 go -C spike/container run . bench raft --nodes 7 --minutes 30
```

```json
{
  "nodes": 7,
  "minutes": 30,
  "wallMs": 79.343,
  "cpuUserMs": 103.05,
  "cpuSysMs": 7.809,
  "virtualMinutesPerSecond": 378.107,
  "committed": 3558,
  "leaderChanges": 1
}
```

### M-3: 7 ノード・8 倍速・20 秒

```fish
env GOTOOLCHAIN=go1.26.9 go -C spike/container run . bench loop --nodes 7 --speed 8 --seconds 20
```

```json
{
  "speed": "8",
  "seconds": 20,
  "steps": 399,
  "stepMs": {
    "p50": 0.057,
    "p99": 0.289,
    "max": 6.728
  },
  "lateMs": {
    "p99": 3.639,
    "max": 32.731
  },
  "cpuUtil": 0.002,
  "gc": {
    "num": 2,
    "pauseMaxMs": 0.034
  }
}
```

手元の基準値は取得できた。クラウドの測定では wallMs の比、stepMs.p99 と lateMs、CPU やメモリの実際の上限を比較する。Dockerfile の Go は 1.26.4 なので、ローカルの 1.26.9 との差も記録する。

### 次の手順: H-2・H-3

リビジョン `d862bb78cbfea3fe0e476605b3b6d4996153f818` のルート .gitignore に `*.secrets*` があることを確認した。ユーザーの端末でも git check-ignore が成功してからシークレットを作る。

トークンは openssl の 32 バイトの乱数 (64 桁 hex)。ファイルの新規作成は Node の writeFileSync で mode 600 / flag wx を指定し、既存のファイルがあれば上書きせず停止する。トークンの値は画面や記録に出さない。SPIKE_TOKEN はこの fish セッションの測定用の環境変数に保持する。

次のブロックはユーザーがリポジトリのルートで実行する。最後から 2 行目で検証用の Worker・DO・コンテナを Cloudflare に実デプロイする。AI はこのブロックを実行しない。

```fish
git check-ignore spike/spike.secrets.json
and set -gx SPIKE_TOKEN (openssl rand -hex 32)
and node -e 'const fs=require("node:fs"),t=process.env.SPIKE_TOKEN;if(!/^[0-9a-f]{64}$/.test(t??""))throw new Error("Invalid SPIKE_TOKEN");fs.writeFileSync("spike/spike.secrets.json",JSON.stringify({SPIKE_TOKEN:t}),{mode:0o600,flag:"wx"});console.log("spike.secrets.json created (mode 600)");'
and env SPIKE_CPU_MS=50 pnpm -C spike exec cf build
and time pnpm -C spike exec cf deploy --prebuilt --secrets-file spike.secrets.json
and curl -fsS --max-time 30 https://raft-game-spike.kizuku.workers.dev/spike/healthz
```

pnpm -C spike によって cf は spike/ で実行される。secrets-file はその作業ディレクトリ内の spike.secrets.json。最初のデプロイなので --containers-rollout immediate は付けない。Worker の公開先の予定は https://raft-game-spike.kizuku.workers.dev。

このコマンドは次に案内したもので、まだ H-2・H-3 の実行結果は受け取っていない。時間・出力 URL・イメージの push・デプロイの成功と /spike/healthz の応答を記録する。healthz は DO を起動しないため、コンテナの本番での起動・WS・性能を確認したことにはしない。

## 2026-11-07: 初回の実デプロイに成功 (H-2・H-3 / M-13)

共有されたプロンプトの開始時刻は 15:58:56 JST。ユーザーが前節で案内したブロックを実行した。

### シークレットの準備

- git check-ignore は `spike/spike.secrets.json` を出力し、無視されることを確認した。
- 64 桁の hex トークンを作り、mode 600 / flag wx でファイルを新規作成した。
- 作成結果は `spike.secrets.json created (mode 600)`。
- cf deploy のバインディング表示では `SPIKE_TOKEN ("(hidden)")`。
- トークンの値は共有されていない。公開記録にも書かない。
- H-2 完了。

### ビルド・イメージ登録・デプロイ

| 項目 | 実行結果 |
|---|---|
| 構成 | SPIKE_CPU_MS=50、cf@1.0.0-beta.28、spike/ の構成 |
| cf build | 成功。Vite 19 ms、Docker の表示 2.2 秒 (主要レイヤーはキャッシュ済み) |
| ローカルのイメージ ID | sha256:03d889b3fe26b5bd2ebaa766ab30caf01eaf5bfb76c49fefb17edf3d8fd107a7 |
| 今回のビルドのタグ | cloudflare-build/b8366d8bfb35/raft-game-spike-server-server:c4818d2dac5b |
| イメージ登録 | Image does not exist remotely のため push。Login Succeeded、2 レイヤーが Pushed |
| レジストリの manifest digest | sha256:a5a5726ea808fe8bee6bbb4507065a514341f2bef67646cdd7f086ea68cf67d5 |
| manifest の表示サイズ | 738 bytes。イメージ全体の大きさとは区別する |
| イメージのローカルの大きさ | 前段で 10.5 MB と表示されたものと同じイメージ ID。今回の push ログにイメージ全体の大きさの再表示はない |
| DO の exports reconciliation | ControlStore / PlainStore / SpikeSession を Created |
| Worker Startup Time | 2 ms。Go コンテナの準備完了時間ではない |
| Worker の upload | 27.32 KiB / gzip 8.83 KiB、Uploaded raft-game-spike (26.76 sec) |
| triggers の deploy | Deployed raft-game-spike triggers (0.60 sec) |
| 公開 URL | https://raft-game-spike.kizuku.workers.dev |
| Version ID | b402a25d-9b2d-418d-b710-f577294a7eb9 |
| fish の time | wall 33.77 秒、usr 1.30 秒、sys 0.27 秒 |
| healthz | GET /spike/healthz が {"ok":true} |
| 判定 | H-3 完了。実デプロイと入口の応答を確認 |

レジストリのアカウント ID は転載しない。イメージのパスは `registry.cloudflare.com/<account-id>/cloudflare-build/b8366d8bfb35/raft-game-spike-server-server`。push と準備完了の表示は同じ digest を指していた。

cf build と cf deploy は、ローカルのビルド用タグ c537d571e688 と c4818d2dac5b をそれぞれ Untagged と表示した。ローカルでの削除は、後片付け時に実際に残っているタグ・ID を確認して行う。

healthz は DO を呼ばないため、コンテナの起動・WS・SQLite・負荷の測定が成功したとはまだ扱わない。

### 次の手順: H-4 の lite の初回測定

クラウドでの測定は初回。ローカルの s-1〜s-5 はクラウドとは別の状態なので、クラウドでは --first 1 から始める。

リポジトリのルートで、デプロイに使ったファイルからトークンを環境変数に読み込んで実行する。トークンは作り直さず、画面にも表示しない。

```fish
set -gx SPIKE_TOKEN (node -p 'JSON.parse(require("node:fs").readFileSync("spike/spike.secrets.json","utf8")).SPIKE_TOKEN')
and node spike/scripts/measure.ts \
  --base https://raft-game-spike.kizuku.workers.dev \
  --instance lite --runs 10 --first 1 \
  --out spike/.cloudflare/lite.json
printf 'measure exit status: %s\n' $status
```

- 標準的な割り当てではクラウドの s-1〜s-9 を使う。実際の使用番号は結果の sessions を正とする。
- 測定は README の目安で約 10 分。WS 無通信 120 秒、alarm 75 秒、inactivity 60 秒、loop 各速度 20 秒などの待ち時間を含む。
- この実行に --internet-control は付けない。enableInternet: true の対照は未使用の番号を選んで別に実行する。
- 結果は spike/.cloudflare/lite.json。終了までのコンソール出力・終了コードと JSON を受け取って確認する。
- 再実行は、使用済み番号と起動回数を確認してから案内する。同じ --first 1 のまま測り直しても起動回数のカウンターはリセットされない。
- stop の失敗があれば、性能の比較より先に対象番号の停止を確認する。

このコマンドは次に案内したもので、クラウド測定の実行結果はまだ受け取っていない。standard-1 と M-9 の対照は lite の結果を確認してから進める。

## 測定前に確定・確認する事項

Issue #15 に残っている設計判断は、環境確認の成功だけで承認済みとは扱わない。

- 8 倍速の step p99 <=25ms、30 分の再計算 <=5 秒、起動 p95 <=5 秒は Issue の提案値。現行 NFR-03 の復元 <=3 秒とは異なるため、判定時に混同しない。
- workers.dev での一時公開、M-9 の固定宛先への外部通信の対照実験、スパイクのコードを削除する時期は、該当手順の前に確認する。
- H-7 の文書化は今回のユーザー指示により AI が支援し、このブランチに記録する。
- 一般公開の CONTAINER_INSTANCE、limits.cpuMs、起動待ちの上限、継承しない Durable Object の可否、cf dev の制約は、実測後に判断する。実測値はまだない。
