# T-007: Cloudflare スパイクの実行記録

- 対象: [Issue #15 (T-007)](https://github.com/K-Kizuku/raft-game/issues/15)
- 測定コードの準備: [Issue #134 (T-007a)](https://github.com/K-Kizuku/raft-game/issues/134)
- 記録用ブランチ: `agent/issue-15`
- 開始日: 2026-11-07 (JST)
- 状態: H-1 の準備中。実行時点の OAuth 認証成功を確認。Workers の契約・workers.dev・ローカルのビルド環境を確認し、測定コードの main へのマージを待つ。
- 実行: Kizuku が手元の端末でコマンドを実行し、AI が共有された出力の確認・次の手順の案内・記録を担当する。
- 最終成果: 実測結果と後片付けの確認を記録し、[ADR-0016](../adr/0016-infrastructure-cloudflare-with-cf-cli.md) に測定結果・判断を反映した PR を作る。

## 記録の扱い

ユーザーから共有された出力と、GitHub で確認した状態を区別する。未確認の項目を完了扱いにしない。認証トークン・シークレット・認証用 URL は記録しない。Docker の出力は関連する環境情報に絞り、ホスト名・ID・ローカルのパス・プロキシ設定は転載しない。

## 進捗

| 手順 | 状態 | 確認できたこと / 残り |
|---|---|---|
| H-1 準備 | 途中 | Node・pnpm・cf の版、Docker daemon の稼働、実行時点の OAuth 認証成功を確認。有料プラン・対象アカウントの最終確認・workers.dev サブドメインは未確認。#134 のマージ後に依存のインストールと check.sh を確認する |
| H-2 トークンの準備 | 未実施 | 測定コードが main に入ってから実施する |
| H-3 検証環境へのデプロイ / M-13 | 未実施 | 人が spike/ から実行する |
| H-4 測定 / M-1〜M-14 | 未実施 | lite・standard-1 の測定と、外部通信の対照実験 |
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
| Workers の有料プラン | `cf accounts subscriptions get` の結果で Workers の契約を確認。権限不足や不明な結果なら、ダッシュボードの Workers の契約表示で補足する | 未実施 |
| workers.dev のサブドメイン | 対象アカウントの Workers & Pages に表示される `<サブドメイン>.workers.dev` を読む | 未確認 |
| linux/amd64 のビルド環境 | `docker buildx inspect` の Platforms を確認。実際のイメージのビルド・実行は #134 の check.sh で確認する | 未実施 |
| ローカルの Go | `go version`。#134 の仕様の go.mod は `go 1.26`。測定用のローカル実行に使えるツールチェーンか確認する | 未実施 |
| トークン生成用の OpenSSL | `openssl version`。この段階ではシークレットを生成しない | 未実施 |
| ユーザーの手元のコミット | `git rev-parse HEAD`。GitHub で確認した main の SHA と区別して記録する | 未実施 |

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

## 測定前に確定・確認する事項

Issue #15 に残っている設計判断は、環境確認の成功だけで承認済みとは扱わない。

- 8 倍速の step p99 <=25ms、30 分の再計算 <=5 秒、起動 p95 <=5 秒は Issue の提案値。現行 NFR-03 の復元 <=3 秒とは異なるため、判定時に混同しない。
- workers.dev での一時公開、M-9 の固定宛先への外部通信の対照実験、スパイクのコードを削除する時期は、該当手順の前に確認する。
- H-7 の文書化は今回のユーザー指示により AI が支援し、このブランチに記録する。
- 一般公開の CONTAINER_INSTANCE、limits.cpuMs、起動待ちの上限、継承しない Durable Object の可否、cf dev の制約は、実測後に判断する。実測値はまだない。
