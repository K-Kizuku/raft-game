# ADR-0021: AI エージェントの開発は、ローカル PC の Codex CLI と Claude Code で行い、Rulesets・必須のチェック・hooks で強制する

- 状態: Accepted
- 日付: 2026-11-06
- 決めた人: プロダクトオーナー（選択肢の選定）、Claude（具体化）
- 関連: [ADR-0019](./0019-parallel-local-development.md)（ローカル環境の分離）、[ADR-0016](./0016-infrastructure-cloudflare-with-cf-cli.md)、[docs/agents/](../agents/README.md)、#1（Epic）・#2〜#6

## 背景

- 開発は、人間 1 人（オーナー）が AI エージェントを並列に動かして行う（ADR-0019）
- オーナーは、ふだんの実装・レビュー・マージを AI に任せ、例外のときだけ判断したい（human-out-of-the-loop）
- そのためには、次の 3 つが要る
  - 並列に動くエージェントが互いの作業を壊さないこと
  - 品質の条件を満たさない PR がマージされないこと
  - AI が自分で条件を緩められないこと
- レビューには Codex を使いたい。オーナーは ChatGPT の契約を持っているが、OpenAI の API キーの従量課金は増やしたくない
- リポジトリは公開している（ADR-0020）。シークレットを増やすほど、漏れたときの影響が大きい

## 検討した選択肢

### 案 A: ローカル PC で Codex CLI と Claude Code を動かし、Rulesets・必須のチェック・hooks で強制する

- 内容: Orchestrator・Worker・Reviewer をすべてオーナーの PC で動かす。Codex CLI は `codex login`（ChatGPT）で使う。マージの条件は GitHub の Rulesets（必須のステータス `ci-ok`・`merge-gate`）と Merge Gate（`agentctl gate`）で、役割ごとの禁止事項は Claude Code の権限と hooks で強制する
- 長所: API キーが要らず、追加の費用がない。シークレットを GitHub に置かない。Actions は CI と判定だけなので、PR から AI を動かされる危険がない
- 短所: PC が動いている間しか進まない。AI とオーナーが同じ GitHub の認証情報を使うので、マージの実行者を GitHub の権限では分けられない（hooks による疑似的な分離）

### 案 B: GitHub Actions で、API キーの Codex を動かす

- 長所: PC が止まっていても動く。実行者（Actions のトークン）を GitHub の権限で分けられる
- 短所: API キーの従量課金が増える。公開リポジトリにシークレット（API キー）を置く。PR やコメントから AI の実行を引き起こせるので、プロンプトインジェクションと費用の上限の管理が要る

### 案 C: Codex の GitHub 連携（クラウドの Codex のレビュー）を使う

- 長所: 設定が少ない。PR に直接レビューが付く
- 短所: レビューの書式を Merge Gate が読める形（agent-review/v1）に固定できない。レビュー役・フォールバックの記録を残せない。動きを手元で再現・検証できない

### 案 D: マージ専用の GitHub App を作り、マージだけをその App に許す

- 長所: マージの実行者を GitHub の権限で分けられる（AI の認証情報ではマージできない）
- 短所: App の秘密鍵の管理と、App を動かす場所（サーバーか Actions）が要る。ハッカソンの規模に対して準備が重い

## 決定

案 A。オーナーは、費用とシークレットを増やさないことを優先し、案 B・C・D を選ばなかった。

| 選ばなかった案 | オーナーが選ばなかった理由 |
|---|---|
| B: Actions で API キーの Codex | API キーの従量課金と、公開リポジトリにシークレットを置くことを避けたい |
| C: Codex の GitHub 連携 | レビューの書式と記録を Merge Gate で検証できない |
| D: マージ専用の GitHub App | 秘密鍵の管理と動かす場所の準備が、規模に見合わない |

強制は次の層で行う（詳しくは [docs/agents/README.md](../agents/README.md#強制の仕組みの層)）。

| 層 | 内容 |
|---|---|
| Rulesets | `main` への直接の push・force push・削除を禁止。PR・squash だけ。必須のステータス `ci-ok`・`merge-gate` |
| ワークフローの権限 | AI の認証に `workflow` 権限を付けない。ワークフローの正本は `.github/workflow-sources/` で、人が取り込む |
| Merge Gate | `agentctl gate` が、Issue・AC・CI・レビュー・Conflict・変更の範囲・保護されたパスの 12 の条件で判定する。ワークフローは main のコードで判定する |
| hooks と権限 | Claude Code の `settings.json` と `guard-*.sh` が、役割ごとの禁止事項（マージ・`main` への push・保護されたパスの書き込みなど）を拒否する |
| 指示 | Issue・エージェントの定義・`/orchestrate`・`CLAUDE.md` |

レビューは Codex CLI を先に使い、使えないとき（`not_installed`・`auth_error`・`rate_limited`・`service_unavailable`）だけ、別セッションの Claude Code に切り替える。切り替えたことと理由はレビュー記録に残し、Merge Gate の C8 で検証する。

## 結果

- 良いこと:
  - 追加の費用とシークレットがない
  - マージの条件が GitHub の状態だけで決まり、エージェントの自己申告に頼らない
  - 状態が GitHub にあるので、PC やセッションが止まっても `agentctl recover` で復旧できる
- 悪いこと・受け入れるリスク:
  - **マージの実行者の制限は疑似的。** AI とオーナーが同じ GitHub の認証情報を使うため、Rulesets の条件を満たした PR は、hooks を迂回すれば AI でもマージできる。条件そのもの（必須のチェック）は迂回できない
  - hooks の判定は字句によるもので、スクリプトのファイルの中での書き込み、`cd` してからの相対パス、変数で組み立てたパスは見分けられない。保護されたパスの変更は、Merge Gate の C12 とレビューで止める
  - Merge Gate が読む PR の本文とレビュー記録は AI が書く。記録が正直であることは、信頼する作者とレビューに頼る
  - PC が動いている間しか開発が進まない
  - Codex が使えない環境では、レビューが Claude Code だけになり、実装と同じ系統のモデルでのレビューになる

## 追記（2026-11-08）: AI の役割の GitHub の操作を、AI 用の GitHub App のトークンにする（#176・#282）

オーナーと AI が 1 つのトークン（1 時間に 5,000 回）を共有していたため、API の上限に毎時間達するようになった。オーナーは #176 で、AI 用の GitHub App（App ID `5659452`）を作って `K-Kizuku/raft-game` にインストールし、秘密鍵をリポジトリの外（`~/.config/raft-game/agent-app.pem`、`chmod 600`）に置いた。案 D と違い、App はマージの実行者を分けるためではなく、**API の上限を分けるため**に使う。動かす場所はローカル PC のままで、App のサーバーは要らない。

オーナーの判断（2026-11-08 4:10）: **すべての AI の役割（Orchestrator・Worker・Reviewer）で同じ GitHub App（`kizuku-claude`）を使う。** リポジトリへの push は引き続きオーナーの鍵の SSH。

- AI の役割（`worker`・`orchestrator`・`reviewer`）の `gh`・`agentctl` は、App のインストールのトークン（上限はオーナーと別の 1 時間に 5,000 回）を使う。トークンは `scripts/agents/lib.sh` が秘密鍵から JWT（RS256、`openssl`）を作って得て、`~/.config/raft-game/` にキャッシュし、作ってから 50 分で作り直す
- Worker は 1 時間を超えて動くことがあるので、`spawn-worker.sh` が `gh` のラッパー（`scripts/agents/gh-app.sh`）を Worker の PATH の先頭に置き、`gh` を呼ぶたびに有効なトークンを入れる。lib.sh の `agentctl` と `ghx` も、呼ぶたびにトークンを子にだけ渡す。Orchestrator の役割のスクリプト（`merge-train.sh`・`autospawn.sh`・`spawn-worker.sh` など）が直接呼ぶ `gh` も、lib.sh の `gh` 関数で `ghx` を通る
- `merge.sh` も App のトークンでマージする。App は Rulesets の bypass に入れないので、必須のチェック（`ci-ok`・`merge-gate`）はそのまま効く
- `review.sh` も App のトークンで記録を書く。Merge Gate の C5 が数えるように、`trusted_authors` に `kizuku-claude[bot]` を足した（オーナーの `K-Kizuku` は残す）
- 鍵がない・権限が 600 より広い・トークンを作れない（App が未インストール・時計のずれ）ときは、理由を標準エラーに 1 行出して、オーナーのトークンに戻る
- トークンと鍵の中身は、標準出力・ログ・PR に出さない。秘密鍵はコミットしない

受け入れるリスク: AI とオーナーは同じ PC で動くので、AI はオーナーの認証情報（`gh` のログイン・SSH の鍵）にも手が届く。マージの実行者の制限が疑似的であることは変わらない（上の「結果」）。また、`kizuku-claude[bot]` を信頼する作者にしたので、App のトークンを持つ Worker も、C5 が数える名前でコメントを書ける。Merge Gate は記録の書式（HEAD の SHA・レビュー役・フォールバックの理由）を検証するが、記録が正直であることは、今までどおりレビューの運用に頼る（上の「結果」）。Worker の push を HTTPS と App のトークンにそろえるか、役割ごとに App を分けるかは、まだ決めていない。

## 差し替えるときの影響範囲

[docs/agents/](../agents/README.md)、[.agents/config.json](../../.agents/config.json)、[.claude/settings.json](../../.claude/settings.json)・`.claude/hooks/`・`.claude/agents/`、`scripts/agents/`、`scripts/github/`、`.github/rulesets/`・`.github/workflow-sources/`、`tools/agentctl/`、[CLAUDE.md](../../CLAUDE.md)。

## 見直すきっかけ

- PC を止めている時間の作業を進めたくなったとき（案 B・D）
- hooks の迂回によるマージ、または保護されたパスの意図しない変更が起きたとき（案 D）
- 共同開発者が増え、認証情報を分ける必要が出たとき
- Codex の GitHub 連携が、記録の書式を固定できるようになったとき（案 C）
