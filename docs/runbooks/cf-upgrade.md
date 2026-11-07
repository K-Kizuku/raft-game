# `cf` の版を上げる

`web/` と `spike/` の devDependency の `cf`（Cloudflare CLI、ベータ版）の版を上げる手順。T-604（Issue #68）。

- 関連: [ADR-0016](../adr/0016-infrastructure-cloudflare-with-cf-cli.md)（`cf` だけで管理する・版を固定する）、[CLAUDE.md の「Cloudflare（`cf`）の注意点」](../../CLAUDE.md)（上げるときは専用の PR。`spike/package.json` も同じ版）
- 版を上げる PR は AI エージェントに任せてよい。マージの後のデプロイの見守りと、うまくいかなかったときの戻しは人が行う（[README.md](./README.md)）

## 1. いつ上げるか

- 必要な修正・機能が新しい版にあるとき
- ADR-0016 の「見直すきっかけ」に当たったとき（`cf` の正式版が出たなど）
- **デモの 2 日前からデモの後までは上げない**（凍結）
- `cf` はベータ版なので、版を上げるとコマンド・設定の型（`cf/config`）・ビルドの出力の形が変わり得る（ADR-0016 の「ベータ版の制約と対応」）。急がないなら上げない

## 2. 変えるもの

専用の PR にする。ほかの依存の更新を混ぜない。

- `web/package.json` と `spike/package.json` の `cf` を、**同じ版に完全に固定**する（`^`・`~` を付けない）
- `pnpm-lock.yaml`
- `@cloudflare/vite-plugin` は、新しい `cf` が求めるときだけ同じ PR で上げる（`web/`・`spike/` の両方を同じ版に。`^`・`~` を付けない）
- 版を書いているドキュメント・コメント（`git grep -n "<古い版>"` で探す）。`docs/adr/` と `docs/verification/` の記録は書き換えず、その時点の版の記録として残す

## 3. 手順

1. 比べるために、古い版のコマンドの一覧を写しておく（`mktemp` の場所。リポジトリに置かない）

   ```bash
   old_meta=$(mktemp -d)
   cp web/node_modules/cf/dist/_meta/commands.json web/node_modules/cf/dist/_meta/hand-written-commands.json "$old_meta"/
   ```

2. 版を上げる

   ```bash
   pnpm -C web add -D -E cf@<新しい版>
   pnpm -C spike add -D -E cf@<新しい版>
   pnpm -C web exec cf --version && pnpm -C spike exec cf --version   # どちらも新しい版
   ```

3. リリースノート（npm の `cf` のページか、Cloudflare の公開している変更履歴）を読み、壊れる変更を PR の本文に書く
4. コマンドの一覧の差を見る。古い版と新しい版の `node_modules/cf/dist/_meta/commands.json`・`hand-written-commands.json` の `command` とフラグ（`options[].name`）を比べる

   ```bash
   python3 - "$old_meta" web/node_modules/cf/dist/_meta <<'EOF'
   import json, sys
   from pathlib import Path

   def load(d):
       out = {}
       for name in ("commands.json", "hand-written-commands.json"):
           for c in json.loads((Path(d) / name).read_text(encoding="utf-8"))["commands"]:
               out.setdefault(c["command"], set()).update(o["name"] for o in c.get("options", []))
       return out

   old, new = load(sys.argv[1]), load(sys.argv[2])
   print("消えたコマンド:", sorted(set(old) - set(new)))
   print("増えたコマンドの数:", len(set(new) - set(old)))
   for k in sorted(set(old) & set(new)):
       if old[k] - new[k]:
           print("消えたフラグ:", k, sorted(old[k] - new[k]))
   EOF
   ```

   - この repo が使うコマンド（`cf build`・`cf deploy`・`cf dev`・`cf workers versions`・`cf workers deployments`・`cf workers types`・`scripts/cloudflare/` のスクリプトが呼ぶもの・`docs/runbooks/` の手順のもの）が消えていないか、フラグが変わっていないかを見る

5. 手順の中の `cf` のコマンドとフラグが新しい版にあるかを確かめる（`web/node_modules` が要る。skip が出ないこと）

   ```bash
   python3 -m unittest discover -s scripts/tests -p 'test_runbooks.py'
   ```

6. `scripts/cloudflare/` のスクリプトを、それぞれ `--dry-run`（既定）で実行する。認証なしで送る内容を出せること

   ```bash
   python3 scripts/cloudflare/cf_zone.py --dry-run
   python3 scripts/cloudflare/cf_turnstile.py --dry-run
   python3 scripts/cloudflare/budget_alert.py --dry-run
   ```

7. テスト・lint・型の検査。`web/cloudflare.config.ts` の型は `cf/config` から来るので、`pnpm -C web typecheck` が新しい型で通ること

   ```bash
   make test
   make lint
   pnpm -C spike typecheck
   ```

8. `DEV_SLOT` を割り当てて、すべてのモードの `cf build`・出力の検査・`cf deploy --prebuilt --dry-run` を確かめる（T-601。Docker が要る）。ビルドの出力の形が変わり `web/scripts/fixtures/` の写しと合わなくなったら、同じ PR で写しを作り直し、差を PR の本文に書く

   ```bash
   scripts/dev-slot.sh acquire
   DEV_SLOT=<n> bash web/scripts/check-cloudflare.sh
   ```

9. Docker があれば、スパイクの検査も流す（CI では動かさない）

   ```bash
   bash spike/scripts/check.sh
   ```

10. PR の CI（`ci-ok`）と、ワークフロー `deploy` の `dry-run` のジョブ（`production`・`demo`）が通ること
11. PR の本文に書く: 古い版 → 新しい版、リリースノートの要点（壊れる変更）、上の 4〜9 の結果

## 4. マージの後

- `pnpm-lock.yaml`・`web/**` の変更なので、マージで `production` に自動でデプロイされる（`deploy.yml` の `paths`。T-603）
- ワークフロー `deploy` の実行を見守り、`/api/healthz` の `build` がマージの commit になったことを確かめる
- 続けて、ワークフロー `deploy` の手動の実行で `demo` をデプロイし、同じく確かめる
- T-605 のスモークテストがあれば流す

```bash
curl -s https://raft.kizuku-hackathon.work/api/healthz          # build が git:<マージの 7 桁>
curl -s https://raft-demo.kizuku-hackathon.work/api/healthz     # demo のデプロイの後
```

## 5. うまくいかなかったとき

- デプロイが失敗した・動きがおかしい → [deploy-rollback.md](./deploy-rollback.md)。Worker の版は方法 A ですぐ戻せる。`cf` の版は手元・CI のビルドの道具なので、恒久には方法 C で版を戻す PR（revert）を出す
- ADR-0016 の「ベータ版の制約と対応」の表の内容が変わったら（例: シークレットを 1 つずつ登録できるようになった）、ADR-0016 に日付つきで追記するか、新しい ADR を書く
