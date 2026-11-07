# T-007: Cloudflare スパイクの実行記録

- 対象: [Issue #15 (T-007)](https://github.com/K-Kizuku/raft-game/issues/15)
- 測定コードの準備: [Issue #134 (T-007a)](https://github.com/K-Kizuku/raft-game/issues/134)
- 記録用ブランチ: `agent/issue-15`
- 開始日: 2026-11-07 (JST)
- 状態: H-4 を実施済み。H-5 では Worker の CPU p99/最大 3 ms、DO fetch の p99 112 ms / 最大 116 ms を取得し、WS と ping の内訳を確認。現在の対象デプロイは 50 ms。次は暫定候補 500 ms をスパイクにデプロイして s-25 で CPU/WS を再測定する。後片付け・最終 ADR/PR は未完了。
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
| H-4 測定 / M-1〜M-14 | 実施済み | lite (s-1〜s-9)、standard-1 (s-16〜s-24)、M-9 の対照 (s-10) が終了コード 0。JSON の抜粋を受領。inactivity と資源表示の制約は結果に記録 |
| H-5 limits.cpuMs の判断 | 途中 | 50 ms で Worker p99/最大 3 ms、DO fetch p99 112 ms/最大 116 ms。暫定候補 500 ms で CPU/WS を再測定する手順を案内。再デプロイと再測定の結果は未受領 |
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

## 2026-11-07: lite の初回クラウド測定

ユーザーが H-4 の lite / --runs 10 / --first 1 を実行し、コンソール出力・結果の表・終了コードを共有した。**終了コードは 0**。この時点では **lite.json 自体はまだ受け取っていない**。以下は表示された値 (丸められた数値を含む) の記録。

### 測定値

| ID | 項目 | lite の初回結果 |
|---|---|---|
| M-1 | 起動 | DO 内 p50 247 / p95 1112 / 最大 1112 ms (10/10 回)。初回 1112 ms。クライアント p50 610 ms。同時 3 つは全成功、最大 1706 ms |
| M-2 | 30 分相当の再計算 | wall 561 ms、53.4 分/秒、CPU 481 ms |
| M-3 | ループの step / 遅れ / CPU | ×1: step p99 0.56 ms、遅れ p99 202 ms、CPU 0.004。×4: 1.2 ms / 2.2 ms / 0.007。×8: 1.2 ms / 1.8 ms / 0.008 |
| M-4 | CPU テスト | Worker は 1e9 まで成功。DO は 3e8 まで成功、1e9 で 502。表の本文は [object Object] と表示されたため、元の JSON が必要 |
| M-5 | WS | 1 KiB 200 回、RTT p50 140 / p99 171 ms。16 KiB 20/20。超過は破棄。500 件は 500/500・順序 ok。無通信 120 秒 ok。ブラウザ側切断の中継は ok、コンテナ側切断では 1011 |
| M-6 | DO→コンテナの HTTP | 200/200 回、p50 3 / p99 9 ms |
| M-7 | SQLite | 100 行 193 ms、1,000 行 227 ms (クライアント)。コンテナ再起動後も 1100 行が残った |
| M-8 | 非継承 DO | PlainStore と ControlStore がともに ok。SpikeSession の SQLite・container・alarm が動作 |
| M-9 | enableInternet: false | DNS・TCP・HTTPS がすべて失敗。TCP は dial tcp 1.1.1.1:443: i/o timeout。internet=true の対照は未実施 |
| M-10 | 停止 | destroy 38 ms、running=false。60000 ms のアラームで停止。inactivityMs=30000 の実験では、60 秒後の確認時点でまだ動いていた |
| M-11 | 異常終了と再起動 | 検知 127 ms、再起動 1066 ms |
| M-12 | ローカル | 前節のローカル結果を使う |
| M-13 | デプロイ | 前節の 33.77 秒・ローカルのイメージ 10.5 MB を使う |
| M-14 | 資源 | CPU 1、GOMAXPROCS 1、cpu.max null、memory.max null、env MODE=single / PORT=8080、colo=SEA |

通常起動 10 回: 1112、329、420、205、209、447、247、191、390、202 ms。同時起動は 452、1557、1706 ms。起動の p95 は混在する通常 10 回の値であり、10 個の初回起動だけを測った結果ではない。

### 暫定評価

- 負荷模型の再計算 561 ms は、#15 の提案 5 秒と NFR-03 の 3 秒のどちらも下回る。手元の Apple M2 の 79.343 ms と比べると表示値の比は約 7.1 倍。
- 8 倍速の step p99 1.2 ms は、#15 の提案値 25 ms を下回る。ただし実際のゲームのエンジンの測定ではなく、最終的な一般公開のサイズの判断は保留する。
- ×1 の遅れ p99 202 ms は、step の CPU 処理時間とは別の問題。standard-1 との比較に残す。
- WS は必要な電文の往復・順序・120 秒の無通信を確認できた。RTT p99 171 ms と NFR-02 の「国内から p95 150 ms」を同じ指標として直接比較しない。
- 終了コード 0 は全設定の効果や全性能条件の合格を意味しない。inactivity は HTTP 200 なら ok、cpu は最初の 1e6 の成功を ok とする実装なので、内訳を読む必要がある。

### inactivity と monitor の仕様を確認

[Cloudflare の Durable Object Container API](https://developers.cloudflare.com/containers/api/durable-object-container/) で確認した。

- setInactivityTimeout は **DO が非アクティブになった後**にコンテナをどれだけ動かすかの設定。
- pending の monitor() は、コンテナの終了または最大 15 分まで DO の退避を抑える。
- スパイクの SessionCore.boot は起動ごとに watch を呼び、watch は c.monitor() の Promise を保持している。
- したがって、起動から 60 秒後に動いていたことは、この monitor と inactivity の組み合わせで説明できる可能性がある。**API の不具合や inactivity の全面的な無効とは断定しない**。
- 本番の設計では明示的な destroy とアラームによる期限を主要な停止手段にする。inactivity のみを「無通信から 30 秒で停止する仕組み」と扱わない。これは T-308・ADR-0016/0017 に引き継ぐべき制約。
- コンストラクターで稼働中のコンテナに inactivity timeout と monitor を再設定する必要性も、上記公式仕様に記載されている。現在の SpikeSession は起動時だけ設定するので、再起動・退避を含む本体設計時に確認する。

### colo の読み方をコードで確認

M-14 の colo=SEA は、router.ts が **入口の request.cf.colo** をヘッダーで DO に渡し、起動結果に載せた値。[Cloudflare の Request API](https://developers.cloudflare.com/workers/runtime-apis/request/#incomingrequestcfproperties) の定義も、リクエストが到達した拠点を指す。**DO や Go コンテナの実際の配置場所を測った値ではない**。

測定経路と RTT の関係は要確認。利用者の VPN やネットワーク設定が原因だと推測で断定しない。国内向けの応答性を満たしたとの判定もしない。

### CPU 上限の判断は保留

構成は limits.cpuMs=50 だったが、Worker は 1e9 まで成功し、DO は 1e9 で 502 を返した。502 の詳細は表だけでは失われている。

- lite.json の items.cpu の各要求の status・error・clientMs を確認する。
- 必要に応じてデプロイ済みの設定と Workers Logs の CPU 時間・outcome を調べる。
- 502 が 50 ms の CPU 制限によるものだとは、現時点では断定しない。
- [Durable Objects の CPU 制限の公式説明](https://developers.cloudflare.com/durable-objects/platform/limits/) と実際の反映状態を照合する。コンテナの CPU と Worker/DO の CPU を混同しない。

### 使用した番号と停止

計画と表示された起動から、s-1〜s-8 は起動 3 回分を消費したと読める。s-9 は inactivity の 1 回。実際の sessions・stopFailures と詳細は JSON で確認する。

measure.ts は各項目の最後に stop を呼ぶ実装で、終了コード 0、stop 失敗の表示はなかった。H-6 の全番号の停止状態の最終確認は別途行う。

ローカルで TCP が通った挙動とは異なり、クラウドでは enableInternet: false の DNS/TCP/HTTPS はすべて失敗した。対照を行うまでは、成功するべき通信が有効な環境だったかの確認を完了扱いにはしない。

### 次の手順: standard-1

同じターミナル・リポジトリのルートで実行する。今回使った s-1〜s-9 と重ならない --first 16 を使う。

```fish
node spike/scripts/measure.ts \
  --base https://raft-game-spike.kizuku.workers.dev \
  --instance standard-1 --runs 10 --first 16 \
  --out spike/.cloudflare/standard-1.json
printf 'measure exit status: %s\n' $status
```

通常の割り当ては s-16〜s-24。結果を受け取ってから、未使用の s-10 などで M-9 の対照に進む。lite.json と standard-1.json も共有してもらい、丸める前の値・502 の本文・inactivity の結果・停止 API の結果を読む。このコマンドはまだ実行結果を受け取っていない。

## 2026-11-07: standard-1 のクラウド測定

共有されたプロンプトの開始時刻は 16:39:07 JST。ユーザーが --instance standard-1 / --runs 10 / --first 16 の測定を実行した。

**終了コードは 0**。全項目の API の実行が終わったが、inactivity による停止や CPU 上限の原因確認を完了したとは扱わない。standard-1.json 自体はこの時点では未受領。

### 測定値

| ID | 項目 | standard-1 の初回結果 (コンソールの表示値) |
|---|---|---|
| M-1 | 起動 | DO 内 p50 230 / p95 396 / 最大 396 ms (10/10 回)。初回 359 ms。クライアント p50 686 ms。同時 3 つは全成功、最大 303 ms |
| M-2 | 30 分相当の再計算 | wall 330 ms、90.9 分/秒、CPU 282 ms |
| M-3 | ループの step / 遅れ / CPU | ×1: step p99 0.114 ms、遅れ p99 1.1 ms、CPU 0.002。×4: 0.556 ms / 1.1 ms / 0.003。×8: 0.275 ms / 1.1 ms / 0.004 |
| M-4 | CPU テスト | Worker・DO とも 3e8 まで成功。1e9 では Worker 503 / DO 502。表ではどちらも [object Object] なので JSON の本文が必要 |
| M-5 | WS | 1 KiB 200 回、RTT p50 130 / p99 335 ms。16 KiB 20/20。超過は破棄。500 件は 500/500・順序 ok。無通信 120 秒 ok。ブラウザ側切断の中継は ok、コンテナ側切断では 1011 |
| M-6 | DO→コンテナの HTTP | 200/200 回、p50 2 / p99 5 ms |
| M-7 | SQLite | 100 行 204 ms、1,000 行 219 ms (クライアント)。コンテナ再起動後も 1100 行が残った |
| M-8 | 非継承 DO | PlainStore と ControlStore がともに ok。SpikeSession の SQLite・container・alarm が動作 |
| M-9 | enableInternet: false | DNS・TCP・HTTPS がすべて失敗。TCP は i/o timeout。internet=true の対照はまだ未実施 |
| M-10 | 停止 | destroy 48 ms、running=false。60000 ms のアラームで停止。inactivityMs=30000 の実験では、60 秒後の確認時点でまだ動いていた |
| M-11 | 異常終了と再起動 | 検知 124 ms、再起動 216 ms |
| M-12 | ローカル | 既に取得したローカルの結果を使う |
| M-13 | デプロイ | lite と同じ Worker・同じイメージ。サイズは start の引数で指定しており、別途のデプロイはしていない |
| M-14 | 資源 | CPU 1、GOMAXPROCS 1、cpu.max null、memory.max null、env MODE=single / PORT=8080、入口の Worker の colo=SJC |

通常起動 10 回: 359、305、296、224、213、339、219、213、396、230 ms。同時起動は 264、206、303 ms。

### lite と比較した暫定評価

| 指標 | lite | standard-1 |
|---|---|---|
| 起動 p95 (DO の中) | 1112 ms | 396 ms |
| 再計算 wall | 561 ms | 330 ms |
| 8 倍速 step p99 | 1.2 ms | 0.275 ms |
| 8 倍速 遅れ p99 | 1.8 ms | 1.1 ms |
| 1 倍速 遅れ p99 | 202 ms | 1.1 ms |
| WS RTT p50 / p99 | 140 / 171 ms | 130 / 335 ms |

- 負荷模型の性能については両方とも、復元 3 秒 (現行 NFR) / 5 秒 (#15 の提案) と、8 倍速 step p99 25 ms (#15 の提案) を下回る。
- 一般公開 lite / デモ standard-1 という既存の構成は、今回の予備測定では維持する候補。本体のエンジンとゲームの負荷を確認した後の判断は残る。
- 起動は実行順・キャッシュ・通信経路などの条件がそろっていない。今回の差だけから、サイズによる起動時間の改善量を断定しない。
- WS の p99 は standard-1 の方が大きい。通信経路や測定時間の違いを切り分けず、サイズの効果や国内向けの応答性を結論しない。
- M-14 の CPU 数 1 や cgroup の null は、lite と standard-1 の割り当て資源が等しい証拠にはならない。アプリから見える値とサービスの割り当て仕様を区別する。
- Worker/DO の CPU テストは Go コンテナ内では実行していない。Worker の 1e9 反復が前回 200 / 今回 503 だったことを、コンテナのサイズ変更による効果とは扱わない。
- inactivity は両方で60秒後に動いていた。前節の monitor との相互作用の候補は残る。明示的な destroy とアラームは両方で動作した。

### 使用した番号

計画と起動の出力では s-16〜s-23 は各 3 回分を使用し、s-24 は inactivity の 1 回。stop 失敗の表示はなく、終了コードは 0。raw JSON と H-6 で最終停止状態を確認する。

現時点のクラウドで使用済みの範囲は s-1〜s-9 と s-16〜s-24。未使用の --first 10 を M-9 の対照に使う。

### 次の手順: M-9 の対照と JSON の詳細取得

同じターミナル、リポジトリのルートで実行する。--only egress --internet-control は、外部通信を禁止した場合と許可した場合を比較する。宛先はコードで固定された example.com の DNS、1.1.1.1:443 の TCP、https://example.com/ の HTTPS のみ。

```fish
node spike/scripts/measure.ts \
  --base https://raft-game-spike.kizuku.workers.dev \
  --instance lite --only egress --internet-control \
  --runs 1 --first 10 \
  --out spike/.cloudflare/egress-control.json
printf 'measure exit status: %s\n' $status
```

その後、lite・standard-1・egress-control の JSON から必要な詳細だけを表示する。認証トークンはこの抽出対象には含めない。

```fish
node -e '
const fs = require("node:fs");
for (const name of ["lite", "standard-1", "egress-control"]) {
  const r = JSON.parse(fs.readFileSync("spike/.cloudflare/" + name + ".json", "utf8"));
  console.log(JSON.stringify({
    target: name, startedAt: r.startedAt, finishedAt: r.finishedAt,
    sessions: r.sessions, stopFailures: r.stopFailures,
    cpu: r.items.cpu, inactivity: r.items.inactivity, egress: r.items.egress,
    startInfo: r.items.start?.data?.info,
    inactivityTimeout: r.items.start?.data?.runs?.[0]?.inactivityTimeout
  }, null, 2));
}
'
```

取得する内容は測定の開始・終了日時、使用番号、stopFailures、CPU のエラー本文と clientMs、inactivity の stopped、egress の両条件、コンテナの info、setInactivityTimeout の設定結果。これらは次に案内したコマンドで、まだ実行結果は受け取っていない。

## 2026-11-07: M-9 の対照と JSON の抜粋を確認

添付されたコマンド出力を確認した。対照実験のプロンプト表示は 16:47:20、詳細の抽出のプロンプト表示は 16:53:09。実際の実行時刻には、以下の JSON の startedAt / finishedAt を使う。フル JSON の全内容ではなく、案内済みの項目の抜粋を受け取った。

### 実行日時と使用番号

| 測定 | 実際の開始 (JST) | 実際の終了 (JST) | 所要 | sessions | stopFailures |
|---|---|---|---|---|---|
| lite | 16:12:51.730 | 16:19:30.646 | 398.916 秒 | 1〜9 | [] |
| standard-1 | 16:40:43.645 | 16:47:20.396 | 396.751 秒 | 16〜24 | [] |
| egress-control | 16:52:57.390 | 16:53:09.020 | 11.630 秒 | 10 | [] |

3 回とも停止 API の失敗の記録は空。最終的な全番号の running=false の確認は H-6 で行う。現時点で使ったクラウドの番号は s-1〜s-10 と s-16〜s-24 の計 19 個。s-10 は今回 2 回起動した。

### M-9: 外部通信の対照

--instance lite / --only egress --internet-control / --runs 1 / --first 10 を実行。起動は 307 ms と 212 ms、終了コードは 0。

| 試験 | enableInternet: false | enableInternet: true |
|---|---|---|
| example.com の DNS | resolver [2606:4700:4700::1111]:53 への UDP が i/o timeout | ok |
| 1.1.1.1:443 の TCP | i/o timeout | ok |
| https://example.com/ | context deadline exceeded | ok |

internet0AllFailed は true。今回の 3 試験では、通信できる対照の条件で成功し、禁止した条件では失敗することを確認した。ローカルで false でも TCP が通った結果とは区別する。

### CPU の HTTP エラーの詳細

DO は両方の測定とも、1e9 反復で次の JSON を返した。

```json
{
  "error": "do_error",
  "message": "Durable Object exceeded its CPU time limit and was reset."
}
```

この DO の 502 は、CPU 超過で DO がリセットされた結果だと確認できた。

Worker は standard-1 の測定の 1e9 で HTTP 503。HTML の title は `Worker exceeded resource limits`。Worker の resource limit 超過は確認できたが、ここに表示された HTML の先頭 500 文字には具体的な CPU 時間や Error 1102 の番号は含まれない。実際の CPU 時間・outcome は Logs で確認する。

| 反復 | lite Worker: status / clientMs | lite DO: status / clientMs | standard-1 Worker: status / clientMs | standard-1 DO: status / clientMs |
|---|---|---|---|---|
| 1e6 | 200 / 138.097 | 200 / 215.780 | 200 / 156.169 | 200 / 202.014 |
| 1e7 | 200 / 169.320 | 200 / 203.751 | 200 / 238.467 | 200 / 248.755 |
| 3e7 | 200 / 179.220 | 200 / 237.715 | 200 / 406.538 | 200 / 218.265 |
| 1e8 | 200 / 308.003 | 200 / 399.034 | 200 / 397.533 | 200 / 481.190 |
| 3e8 | 200 / 695.560 | 200 / 953.258 | 200 / 919.928 | 200 / 1102.796 |
| 1e9 | 200 / 2082.436 | 502 / 2503.731 | 503 / 1710.909 | 502 / 2803.127 |

clientMs はクライアントから見た往復の経過時間であり、Worker/DO の CPU 時間そのものではない。

[Cloudflare Workers の CPU 制限の説明](https://developers.cloudflare.com/workers/platform/limits/) には、isolate が時々設定値を超える場合を許容する仕組みがあると記載されている。1 回成功したことだけで上限が無効とは判定しない。ただし、その仕組みが今回の成功/失敗の差の原因だと断定せず、デプロイされた値と Logs の CPU 時間を確認する。

### inactivity とコンテナの情報

- lite の s-9、standard-1 の s-24 とも inactivityMs=30000 / stopped=false。
- 起動結果の inactivityTimeout は両方とも ok。設定 API が例外になった事例ではない。
- 前節で確認した monitor と DO の退避の関係を踏まえ、単に 60 秒後に停止するという期待で測定を合格扱いにしない。
- Go の実測値は両方とも go1.26.4 / linux / amd64、numCPU=1 / GOMAXPROCS=1、cpuMax=null / memoryMax=null、MODE=single / PORT=8080。
- コンテナの hostname は公開記録に転載しない。

### H-5: 次に CPU 設定と通常要求の CPU 時間を取得する

cf@1.0.0-beta.28 の公式ソースで、次を確認した。

- [workers versions get](https://github.com/cloudflare/cf/blob/b0703f68cf9ec67f515055d42d7ad128241649ed/packages/cli/src/commands/_generated/workers/versions/get.ts) は、Worker 名と version UUID を指定してメタデータを取得できる。modules を include しない。
- [o11y telemetry query](https://github.com/cloudflare/cf/blob/b0703f68cf9ec67f515055d42d7ad128241649ed/packages/cli/src/commands/_generated/o11y/telemetry/query.ts) は、[Workers Observability API](https://developers.cloudflare.com/api/resources/workers/subresources/observability/subresources/telemetry/methods/query/) を呼ぶ。--preview は結果を保存しない dry に対応する。
- [Query Builder の説明](https://developers.cloudflare.com/workers/observability/query-builder/) の $workers.cpuTimeMs と request.path、P99/Max/Count の集計を使う。

次のブロックはユーザーがリポジトリのルートで実行する。最初は CPU の設定を含む箇所だけを表示し、次に通常要求の CPU 時間を経路ごとに集計する。負荷試験の /cpu は除外する。

```fish
pnpm -C spike exec cf workers versions get b402a25d-9b2d-418d-b710-f577294a7eb9 --worker raft-game-spike | node -e '
let s = "";
process.stdin.on("data", d => s += d).on("end", () => {
  const x = JSON.parse(s), out = {};
  function scan(v, p = "") {
    if (!v || typeof v !== "object") return;
    for (const [k, value] of Object.entries(v)) {
      const path = p ? p + "." + k : k;
      if (["limits", "cpu_ms", "cpuMs"].includes(k)) out[path] = value;
      else scan(value, path);
    }
  }
  scan(x);
  console.log(JSON.stringify({cpuSettings: out, versionKeys: Object.keys(x)}, null, 2));
});
'

pnpm -C spike exec cf o11y telemetry query \
  --query-id t007-normal-cpu --preview \
  --view calculations --chart-type aggregate --ignore-series \
  --timeframe-from 1794035511730 --timeframe-to 1794038049020 \
  --parameters-filters '[{"key":"$metadata.service","operation":"eq","type":"string","value":"raft-game-spike"},{"key":"$workers.cpuTimeMs","operation":"exists","type":"number"},{"key":"$workers.event.request.path","operation":"not_includes","type":"string","value":"/cpu"}]' \
  --parameters-calculations '[{"operator":"count","alias":"n"},{"operator":"p99","key":"$workers.cpuTimeMs","keyType":"number","alias":"cpu_p99_ms"},{"operator":"max","key":"$workers.cpuTimeMs","keyType":"number","alias":"cpu_max_ms"}]' \
  --parameters-group-bys '[{"type":"string","value":"$workers.event.request.path"}]' \
  --limit 500 --parameters-limit 500
```

集計の時刻範囲は 2026-11-07 16:11:51.730〜16:54:09.020 JST (3 回の測定の前後に 1 分を加えた範囲)。リソースの変更・再デプロイ・コンテナの起動はしない。

CPU 設定を抽出する Node 部分は node --check で構文を確認した。API の実行・スキーマ上のフィールドが実際のログで利用できるかは、ユーザーの実行結果を待つ。集計が空の場合は成功した測定とせず、フィールドとログの記録設定を調べる。

この出力を受けて limits.cpuMs の値を判断する。H-6 の資源削除は、設定の取得と必要なログの確認を終えてから案内する。

## 2026-11-07: H-5 の設定取得失敗と空の集計結果

添付されたコマンド出力を確認した。設定の取得にも、通常要求の CPU 時間の測定にもまだ成功していない。

### デプロイ済み設定の取得

案内した `cf workers versions get <version-id> --worker raft-game-spike` は、手元の CLI で次のエラーになった。

```text
Missing required argument: worker-id
```

その後の Node の `Unexpected end of JSON input` は、失敗した cf コマンドの空の標準出力を JSON として解析した結果。設定が存在しないという意味ではない。

前節で参照したのは Cloudflare 公式リポジトリの特定コミットであり、手元にインストールされた配布物の引数と一致することまでは確認できていなかった。パッケージの表示バージョンだけを根拠に、公開版の仕様確認済みとは扱わない。次は実際の CLI のヘルプを確認し、修正後の設定取得では cf の成功と JSON の解析を別々に確認する。

### 通常要求の CPU 集計

Observability API はリクエストを受け付け、2026-11-07 17:05:24.015 JST に status=COMPLETED / dry=true を返した。しかし n / cpu_p99_ms / cpu_max_ms はすべて aggregates=[]、series の各 data も空だった。

- API の呼び出しの成功と、測定値の取得を区別する。通常要求の件数・CPU p99・最大値は未取得。
- 空の結果を CPU 時間 0 ms と解釈しない。statistics.rows_read は内部の走査行数であり、通常要求の一致件数ではない。
- フィルターのフィールド名・値、CPU 項目の記録有無、ログ設定などはまだ切り分けられていない。
- この結果だけでは、デプロイ済みの上限が 50 ms であることも、50 ms が適切であることも確認できない。
- lite / standard-1 / 外部通信の既存の測定結果は引き続き利用する。再デプロイや測定の再実行はまだ案内しない。

### 次の手順: インストール済み CLI のヘルプ

リポジトリのルートで次の 3 コマンドを実行し、出力を確認する。いずれもヘルプの表示で、Cloudflare の設定変更・コンテナの起動は行わない。

```fish
pnpm -C spike exec cf workers versions get --help
pnpm -C spike exec cf workers get --help
pnpm -C spike exec cf o11y telemetry keys --help
```

その出力で、バージョンの取得に必要な worker-id の指定、設定取得用コマンド、実際に記録されているログ項目の調べ方を確定する。H-5 は途中のままとし、資源の削除と最終 ADR/PR は未完了として維持する。

## 2026-11-07: インストール済み CLI のヘルプを確認

cf@1.0.0-beta.28 の 3 コマンドのヘルプを受領した。

| コマンド | 実際の引数 |
|---|---|
| workers versions get | version-id が位置引数、--worker-id が必須。Worker の ID または名前を指定できる |
| workers get | worker-id が必須の位置引数。ID または名前を指定できる |
| o11y telemetry keys | --from、--to、--limit、--filters、--body を提供。今回はネストした検索条件も指定するため --body を使う |

測定コミット d862bb78cbfea3fe0e476605b3b6d4996153f818 の spike/cloudflare.config.ts では、Worker の observability.enabled=true、logs.enabled=true、headSamplingRate=1 を指定している。設定ファイル上の指定であり、デプロイ後の実効設定はまだ未取得。

[Cloudflare の keys API](https://developers.cloudflare.com/api/resources/workers/subresources/observability/subresources/telemetry/methods/keys/) は、ログ項目の key・type・lastSeenAt を返す。needle で Worker 名を含むイベントを探し、keyNeedle で CPU や要求を分類する項目名に絞る。前回の CPU フィルターと経路の除外条件を流用せず、利用できる項目から確かめる。

### 次に案内するコマンド

デプロイ済みのバージョンと Worker のメタデータを、既存の gitignore 対象である .cloudflare 内に保存する。後続の解析は cf が成功した場合だけ行い、公開する出力は CPU 上限・observability とトップレベルの項目名に限定する。シークレットファイルや環境変数の値は表示しない。

```fish
pnpm -C spike exec cf workers versions get b402a25d-9b2d-418d-b710-f577294a7eb9 \
  --worker-id raft-game-spike > spike/.cloudflare/deployed-version.json
and pnpm -C spike exec cf workers get raft-game-spike \
  > spike/.cloudflare/deployed-worker.json
and node -e '
const fs = require("node:fs");
for (const name of ["version", "worker"]) {
  const x = JSON.parse(fs.readFileSync("spike/.cloudflare/deployed-" + name + ".json", "utf8"));
  const settings = {};
  function scan(v, p = "") {
    if (!v || typeof v !== "object") return;
    for (const [k, value] of Object.entries(v)) {
      const path = p ? p + "." + k : k;
      if (["limits", "cpu_ms", "cpuMs", "observability"].includes(k)) settings[path] = value;
      else scan(value, path);
    }
  }
  scan(x);
  console.log(JSON.stringify({target: name, settings, topLevelKeys: Object.keys(x)}, null, 2));
}
'
```

次に、lite・standard-1・M-9 対照を含む測定時間帯のログ項目を取得する。

```fish
pnpm -C spike exec cf o11y telemetry keys \
  --body '{"from":1794035511730,"to":1794038049020,"limit":1000,"needle":{"value":"raft-game-spike","matchCase":false},"keyNeedle":{"value":"cpu|wall|service|script|class|request|trigger|origin|outcome|execution|worker","isRegex":true,"matchCase":false}}'
```

どちらも読み取り。Node 部分の JavaScript と keys の JSON は構文を確認した。実際の API 結果はまだ受領しておらず、上限と CPU p99/最大値は未確定のまま。

## 2026-11-07: デプロイ済み CPU 上限とログ項目の取得に成功

添付の出力を確認した。workers versions get と workers get が成功し、保存した JSON から指定した項目を抽出できた。ログ項目の取得のプロンプト表示は 17:17:40。

### 取得した実際の設定

| 対象 | 項目 | 値 |
|---|---|---|
| バージョン b402a25d-9b2d-418d-b710-f577294a7eb9 | limits.cpu_ms | 50 |
| Worker | observability.enabled | true |
| Worker | observability.head_sampling_rate | 1 |
| Worker | observability.logs.enabled | true |
| Worker | observability.logs.head_sampling_rate | 1 |
| Worker | observability.logs.invocation_logs | true |
| Worker | observability.logs.persist | true |
| Worker | observability.traces.enabled | false |

CPU 上限は対象デプロイのメタデータで確認した値。ふだんの処理に十分な余裕があるかの判断は、CPU 時間の集計を待つ。空の集計の原因がログ無効化だったとは現時点では言えない。

### 利用可能なログ項目

keys の出力には次が存在した。ここでは型が確認できた項目名だけを記録し、イベント本体やヘッダーの値は取得・転載しない。

| 項目 | 型 | 用途 |
|---|---|---|
| $workers.cpuTimeMs | number | CPU 時間 |
| $workers.wallTimeMs | number | 経過時間。CPU 時間とは区別する |
| $workers.scriptName | string | Worker の指定 |
| $workers.executionModel | string | Worker / DO の実行モデルの分類 |
| $workers.entrypoint | string | エントリーポイントの分類 |
| $workers.eventType | string | イベントの種類の分類 |
| $workers.outcome | string | 実行結果 |
| $workers.event.request.path | string | 要求のパス |
| $workers.event.path | string | 別のイベント形式のパス |
| $workers.event.request.search.iters | number | 反復負荷試験の判別 |
| $workers.event.search.iters | number | 別のイベント形式の反復負荷試験の判別 |

前回の集計が空だった原因の断定はしない。次の集計では scriptName を指定し、request.path への除外条件とグループ化を外す。測定コードの itemCpu は /spike/cpu と /spike/s/<番号>/cpu に iters を指定するため、実際に存在する 2 種類の iters 項目がどちらもないイベントを通常処理の対象にする。

### 次に CPU 時間を集計する

[Observability API](https://developers.cloudflare.com/api/resources/workers/subresources/observability/subresources/telemetry/methods/query/) の DOES_NOT_EXIST、P99、Max、Count、グループ化を使用する。実行モデル・エントリーポイント・イベントの種類ごとに分け、alarm などと fetch を混同しない。CPU 時間が存在するログだけを対象とするため、件数は測定スクリプト全体の要求件数と同一とは限らない。

```fish
pnpm -C spike exec cf o11y telemetry query \
  --query-id t007-normal-cpu-v2 --preview --view calculations \
  --timeframe-from 1794035511730 --timeframe-to 1794038049020 \
  --parameters-filters '[{"key":"$workers.scriptName","operation":"eq","type":"string","value":"raft-game-spike"},{"key":"$workers.cpuTimeMs","operation":"exists","type":"number"},{"key":"$workers.event.request.search.iters","operation":"DOES_NOT_EXIST","type":"number"},{"key":"$workers.event.search.iters","operation":"DOES_NOT_EXIST","type":"number"}]' \
  --parameters-calculations '[{"operator":"count","alias":"n"},{"operator":"p99","key":"$workers.cpuTimeMs","keyType":"number","alias":"cpu_p99_ms"},{"operator":"max","key":"$workers.cpuTimeMs","keyType":"number","alias":"cpu_max_ms"}]' \
  --parameters-group-bys '[{"type":"string","value":"$workers.executionModel"},{"type":"string","value":"$workers.entrypoint"},{"type":"string","value":"$workers.eventType"}]' \
  --parameters-limit 50 \
  > spike/.cloudflare/normal-cpu-query.json
and node -e '
const fs = require("node:fs");
const r = JSON.parse(fs.readFileSync("spike/.cloudflare/normal-cpu-query.json", "utf8"));
console.log(JSON.stringify({
  status: r.run?.status,
  filters: r.run?.query?.parameters?.filters,
  groupBys: r.run?.query?.parameters?.groupBys,
  calculations: r.calculations?.map(c => ({
    alias: c.alias, calculation: c.calculation, aggregates: c.aggregates
  })),
  statistics: r.statistics
}, null, 2));
'
```

CLI に通す JSON と抽出用の JavaScript の構文は確認した。--preview による読み取りのみで、再デプロイや追加の負荷試験はしない。集計結果はまだ受領していない。

## 2026-11-07: 通常処理の CPU 集計 v2 を受領

プロンプト表示は 17:17:52。t007-normal-cpu-v2 は status=COMPLETED。scriptName=raft-game-spike、cpuTimeMs が存在し、request.search.iters と event.search.iters がどちらも存在しない、という条件で取得した。API が返した filters / groupBys は指定した条件と一致した。

### 実際に表示された集計

| executionModel | entrypoint | eventType | 件数 n | CPU p99 (ms) | CPU 最大 (ms) |
|---|---|---|---|---|---|
| durableObject | SpikeSession | fetch | 113 | 112 | 116 |
| durableObject | SpikeSession | alarm | 10 | 1 | 1 |
| durableObject | PlainStore | fetch | 2 | 1 | 1 |

集計ごとの count も上記の件数と一致し、interval / sampleInterval はいずれも 1。statistics の elapsed は 0.116443379 秒、abr_level=1。rows_read は 4,123,602 だったが、一致する通常要求の件数としては使わない。

### 判断と未確認の点

- 取得できた SpikeSession fetch の p99 / 最大値は、対象バージョンの cpu_ms=50 を上回った。50 ms が通常処理に十分な余裕を持つとは、まだ結論できない。
- 今回の結果には Worker と ControlStore が現れていない。CPU 0 ms や未実行とは判断しない。entrypoint を含めたグループ化で項目のないログが外れた可能性を含め、条件を変えて確認する。
- 経路別にはまだ分かれていない。スパイクには ping の 200 回の HTTP 往復、sql の 100 / 1,000 行の同期 INSERT、WS の長時間の中継などが含まれる。112 ms がどの処理に対応するか、CPU 上限を変更する前に調べる。
- PlainStore の 2 件の p99 は参考値。少数の測定から本番の高い百分位数の安定性は推定しない。
- 前回集計が空だった原因はまだ断定しない。今回はフィルターとグループ化を同時に変えたため、原因の切り分け実験にはなっていない。
- [最新の Issue #15](https://github.com/K-Kizuku/raft-game/issues/15) を再読した。H-5 は通常要求の p99 の数倍を目安に、必要なら cpuMs を変更して cpu / ws を再測定する手順のまま。現時点では再デプロイを案内しない。

### 次は同じ時間帯の内訳を取得する

既存の normal-cpu-query.json の filters と calculations を再利用し、3 つの読み取りを行う。

1. overview: entrypoint をグループ化から外し、実行モデルとイベントの種類だけで集計する。
2. request-path: request.path があるイベントを経路別に集計する。
3. do-path: executionModel=durableObject を指定し、entrypoint・eventType・event.path で集計する。

経路の項目がないログは経路別の結果に含まれない可能性があるため、overview と件数を照合する。各結果の CPU 最大値が大きい順で先頭 20 グループを表示し、全体のグループ数も残す。生のイベント、ヘッダーやクエリの値は表示しない。

```fish
node -e '
const fs = require("node:fs");
const {execFileSync} = require("node:child_process");
const p = JSON.parse(fs.readFileSync("spike/.cloudflare/normal-cpu-query.json", "utf8")).run.query.parameters;
const cases = [
  ["overview", ["$workers.executionModel", "$workers.eventType"], []],
  ["request-path", ["$workers.executionModel", "$workers.eventType", "$workers.event.request.path"], []],
  ["do-path", ["$workers.entrypoint", "$workers.eventType", "$workers.event.path"],
    [{key: "$workers.executionModel", operation: "eq", type: "string", value: "durableObject"}]]
];
for (const [name, keys, extra] of cases) {
  const args = ["-C", "spike", "exec", "cf", "o11y", "telemetry", "query",
    "--query-id", "t007-cpu-" + name, "--preview", "--view", "calculations",
    "--timeframe-from", "1794035511730", "--timeframe-to", "1794038049020",
    "--parameters-filters", JSON.stringify([...p.filters, ...extra]),
    "--parameters-calculations", JSON.stringify(p.calculations),
    "--parameters-group-bys", JSON.stringify(keys.map(value => ({type: "string", value}))),
    "--parameters-limit", "500"];
  const raw = execFileSync("pnpm", args, {encoding: "utf8", stdio: ["ignore", "pipe", "inherit"], maxBuffer: 32 * 1024 * 1024});
  fs.writeFileSync("spike/.cloudflare/normal-cpu-" + name + ".json", raw);
  const r = JSON.parse(raw), rows = new Map();
  for (const c of r.calculations ?? []) for (const a of c.aggregates ?? []) {
    const key = JSON.stringify(a.groups);
    if (!rows.has(key)) rows.set(key, {group: a.groups.map(g => g.value).join(" / ")});
    rows.get(key)[c.alias ?? c.calculation] = a.value;
  }
  const sorted = [...rows.values()].sort((a, b) => (b.cpu_max_ms ?? 0) - (a.cpu_max_ms ?? 0));
  console.log(JSON.stringify({query: name, status: r.run?.status, totalGroups: rows.size, top20: sorted.slice(0, 20)}, null, 2));
}
'
```

JavaScript の構文を確認した。pnpm の引数は配列で渡し、シェルへの文字列の再解釈を行わない。API の結果は未受領。H-5 の値の判断と H-6 以降は継続中。

## 2026-11-07: CPU の全体集計と経路別の内訳を受領

プロンプト表示は 17:21:59。overview / request-path / do-path の 3 クエリがすべて status=COMPLETED。フィルターと計算は、前回保存した normal-cpu-query.json から再利用した。

### overview: Worker の CPU 時間も取得

| executionModel | eventType | 件数 n | CPU p99 (ms) | CPU 最大 (ms) |
|---|---|---|---|---|
| stateless | fetch | 73 | 3 | 3 |
| durableObject | fetch | 115 | 112 | 116 |
| durableObject | alarm | 10 | 1 | 1 |

entrypoint をグループ化から外すと stateless の fetch が現れた。前回の結果に Worker がなかったことを、Worker の CPU 0 ms とは解釈しない。今回も cpuTimeMs が存在するログの条件付き集計であり、すべての実行を数えたものとは限らない。

### do-path: 高い値は WS と HTTP の反復

| entrypoint | eventType | event.path | 件数 n | CPU p99 (ms) | CPU 最大 (ms) |
|---|---|---|---|---|---|
| SpikeSession | fetch | /ws | 2 | 116 | 116 |
| SpikeSession | fetch | /ping | 2 | 64 | 64 |
| SpikeSession | fetch | /sql | 4 | 25 | 25 |
| SpikeSession | fetch | /start | 49 | 10 | 10 |
| SpikeSession | fetch | /crash | 2 | 5 | 5 |
| SpikeSession | fetch | /status | 12 | 3 | 3 |
| SpikeSession | fetch | /stop | 39 | 2 | 2 |
| SpikeSession | fetch | /egress | 1 | 1 | 1 |
| PlainStore | fetch | /plain | 2 | 1 | 1 |
| SpikeSession | fetch | /bench/raft | 2 | 1 | 1 |

10 グループの n の合計は 115 で、overview の durableObject / fetch と一致する。出力は先頭 20 グループの制限に達していない。

request-path は 0 グループで、CPU の値は取れなかった。キー一覧で request.path が存在しても、cpuTimeMs を持つログに同じ項目があるとは限らない。今回は overview と DO の event.path の結果を使用する。

### CPU 上限の次の検証候補: 500 ms

- Worker は p99/最大 3 ms。DO fetch の p99 は 112 ms、最大 116 ms。
- /ws の数値は 2 件の fetch ログの集計であり、WS 1 電文ごとの CPU p99 ではない。[DO の CPU 制限](https://developers.cloudflare.com/durable-objects/platform/limits/) は、受信する HTTP 要求や WS メッセージで CPU の予算が更新されると説明している。116 ms と設定 50 ms の比較だけで、電文ごとの制限違反や、500 ms が必要な最小値だとは結論しない。
- /ping は 1 要求内で HTTP 往復を 200 回行う測定。/sql も 100 行/1,000 行をまとめて追加する測定であり、本番の 1 コマンドと同じ負荷ではない。
- H-5 の「通常要求の p99 の数倍」を満たす保守的な候補として、共有の limits.cpuMs を 500 ms で検証する。取得した DO fetch p99 の約 4.46 倍、最大値の約 4.31 倍。
- これは本番向けの暫定候補。スパイクの WS と反復 CPU 試験で再確認し、本体の負荷に対する最終調整は T-605 で行う。
- 現在のデプロイ済みの値は引き続き 50 ms。以下の再デプロイはまだ実行結果を受け取っていない。

### 次の手順: スパイクだけを再デプロイして CPU / WS を再測定

#15 の H-5 の範囲で、ユーザーが実行する。2 回目のデプロイなので --containers-rollout immediate を指定する。最新バージョンのメタデータから cpu_ms=500 を確認し、一致した場合だけ測定に進む。

```fish
env SPIKE_CPU_MS=500 pnpm -C spike exec cf build
and time pnpm -C spike exec cf deploy --prebuilt \
  --secrets-file spike.secrets.json --containers-rollout immediate
and curl -fsS --max-time 30 https://raft-game-spike.kizuku.workers.dev/spike/healthz
and pnpm -C spike exec cf workers versions get latest --worker-id raft-game-spike \
  > spike/.cloudflare/cpu500-version.json
and node -e '
const x = JSON.parse(require("node:fs").readFileSync("spike/.cloudflare/cpu500-version.json", "utf8"));
console.log(JSON.stringify({id: x.id, limits: x.limits}, null, 2));
if (x.limits?.cpu_ms !== 500) process.exit(1);
'
and set -gx SPIKE_TOKEN (node -p 'JSON.parse(require("node:fs").readFileSync("spike/spike.secrets.json","utf8")).SPIKE_TOKEN')
and node spike/scripts/measure.ts \
  --base https://raft-game-spike.kizuku.workers.dev \
  --instance lite --only cpu,ws --runs 1 --first 25 \
  --out spike/.cloudflare/cpu500-ws-lite.json
printf 'H-5 exit status: %s\n' $status
```

- measure.ts の SessionPool と planSessions を確認した。--only cpu,ws は規定順で ws→cpu を実行し、未使用の s-25 だけを使う。WS で起動は 1 回、CPU はコンテナを起動しない。SPIKE_MAX_SESSIONS=30 の範囲内。
- WS は前と同じ 1 KiB・16 KiB・超過の破棄・500 件の順序・120 秒無通信・両方向の切断を確認する。
- CPU の 1e9 などが意図的に上限で止まる場合がある。HTTP の結果を残し、WS の正常な中継が維持されることと分けて判断する。
- 対象は raft-game-spike のみ。新しい秘密値は生成せず、既存のシークレットファイルを使う。表示するメタデータは version id と limits だけ。
- Node のメタデータ検査の構文を確認した。デプロイ・API への問い合わせ・測定そのものは AI から実行していない。

再測定後に使った全番号の停止、クラウド資源の削除、ローカルの後片付け、翌日の費用確認へ進む。H-7 / H-8 / 最終 PR は継続中。

## 測定前に確定・確認する事項

Issue #15 に残っている設計判断は、環境確認の成功だけで承認済みとは扱わない。

- 8 倍速の step p99 <=25ms、30 分の再計算 <=5 秒、起動 p95 <=5 秒は Issue の提案値。現行 NFR-03 の復元 <=3 秒とは異なるため、判定時に混同しない。
- workers.dev での一時公開、M-9 の固定宛先への外部通信の対照実験、スパイクのコードを削除する時期は、該当手順の前に確認する。
- H-7 の文書化は今回のユーザー指示により AI が支援し、このブランチに記録する。
- 一般公開の CONTAINER_INSTANCE、limits.cpuMs、起動待ちの上限、継承しない Durable Object の可否、cf dev の制約は、実測後に判断する。測定結果に基づく最終判断はまだ確定していない。
