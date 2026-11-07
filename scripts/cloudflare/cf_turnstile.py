#!/usr/bin/env python3
"""Turnstile のウィジェット（2 つのホスト名に 1 つ）を cf で確かめ・作る（直す）（T-606）。

ADR-0018（コストガード G4）・ADR-0016（構成は cf だけで管理）に従う。
シークレットキーは --secret-out のファイル（リポジトリの外・権限 0600）にだけ書き、
標準出力・標準エラーには出さない。

使い方（どれか 1 つ。指定がなければ --dry-run）:
  python3 scripts/cloudflare/cf_turnstile.py [--dry-run]   # 認証なしで送る内容を出す
  python3 scripts/cloudflare/cf_turnstile.py --check       # 今の状態を出す
  python3 scripts/cloudflare/cf_turnstile.py --apply --secret-out <パス> [--yes]

--check・--apply は環境変数 CLOUDFLARE_ACCOUNT_ID が要る。詳しくは scripts/cloudflare/README.md。
"""

from __future__ import annotations

import argparse
import json
import os
import shlex
import subprocess
import sys
from pathlib import Path
from typing import Any, NamedTuple, Optional

ZONE = "kizuku-hackathon.work"
HOSTNAMES = ("raft.kizuku-hackathon.work", "raft-demo.kizuku-hackathon.work")
WIDGET_NAME = "raft-game"
WIDGET_MODE = "managed"

NAME = "cf_turnstile"
PREFIX = NAME + ": "
REPO_ROOT = Path(__file__).resolve().parent.parent.parent

NEXT_STEPS = """\
次の手順（人が行う）:
  1. gh secret set TURNSTILE_SECRET < {path}
  2. gh variable set TURNSTILE_SITE_KEY --body {sitekey}
  3. 済んだら {path} を消す
  4. ゾーン: python3 scripts/cloudflare/cf_zone.py → --check → --apply"""


def widget_body() -> dict:
    return {"name": WIDGET_NAME, "domains": list(HOSTNAMES), "mode": WIDGET_MODE}


class Failure(Exception):
    """利用者に文を出して終了コード 1 で終わる。文にシークレットキーを含めない。"""


def info(msg: str) -> None:
    print(PREFIX + msg, flush=True)


def err(msg: str) -> None:
    print(PREFIX + msg, file=sys.stderr, flush=True)


# ---------------------------------------------------------------------------
# 純粋な部分
# ---------------------------------------------------------------------------


class WidgetPlan(NamedTuple):
    action: str  # "create"・"update"・"unchanged"
    sitekey: Optional[str]
    diffs: list


def public_view(widget: Any) -> dict:
    """ウィジェットから秘密でないキーだけを取り出す。"""
    if not isinstance(widget, dict):
        return {}
    return {k: widget[k] for k in ("sitekey", "name", "domains", "mode") if k in widget}


def widget_diffs(widget: dict) -> list[str]:
    want = widget_body()
    diffs: list[str] = []
    domains = widget.get("domains")
    if not isinstance(domains, list) or set(domains) != set(want["domains"]) or len(
        domains
    ) != len(set(domains)):
        diffs.append("domains: %s → %s" % (json.dumps(domains), json.dumps(want["domains"])))
    if widget.get("mode") != want["mode"]:
        diffs.append("mode: %s → %s" % (json.dumps(widget.get("mode")), json.dumps(want["mode"])))
    return diffs


def plan_widget(widgets: list) -> WidgetPlan:
    """名前がちょうど raft-game のウィジェットから、作る・直す・変更なしを決める。"""
    same = [w for w in widgets if isinstance(w, dict) and w.get("name") == WIDGET_NAME]
    if not same:
        return WidgetPlan("create", None, [])
    if len(same) > 1:
        raise Failure(
            "同じ名前のウィジェットが複数ある。cf turnstile widgets list で確かめ、要らないものを人が消す"
        )
    w = same[0]
    sitekey = w.get("sitekey")
    if not isinstance(sitekey, str) or not sitekey:
        raise Failure("ウィジェットの sitekey が読めない")
    diffs = widget_diffs(w)
    return WidgetPlan("update" if diffs else "unchanged", sitekey, diffs)


def check_secret_out(path_str: str) -> Path:
    """--secret-out のパスを確かめる。違反なら Failure。"""
    path = Path(path_str).expanduser()
    if os.path.lexists(path):
        raise Failure("--secret-out が既にある（上書きしない）: %s" % path)
    parent = path.parent
    if not parent.is_dir():
        raise Failure("--secret-out の親のディレクトリがない: %s" % parent)
    resolved = parent.resolve() / path.name
    root = REPO_ROOT.resolve()
    if resolved == root or root in resolved.parents:
        raise Failure("--secret-out がリポジトリの中にある（公開リポジトリに残さない）: %s" % path)
    return path


def write_secret(path: Path, secret: str) -> None:
    fd = os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        os.fchmod(fd, 0o600)
        data = secret.encode("utf-8")
        view = memoryview(data)
        while view:
            n = os.write(fd, view)
            view = view[n:]
    finally:
        os.close(fd)


# ---------------------------------------------------------------------------
# cf の呼び出し
# ---------------------------------------------------------------------------


def cf_base() -> list[str]:
    env_cf = os.environ.get("CF")
    if env_cf:
        return shlex.split(env_cf)
    path = REPO_ROOT / "web" / "node_modules" / ".bin" / "cf"
    if not path.exists():
        raise Failure("cf がない（pnpm install を先に実行する）")
    return [str(path)]


def body_json(obj: Any) -> str:
    return json.dumps(obj, ensure_ascii=False, separators=(",", ":"))


def run_cf(args: list[str]) -> subprocess.CompletedProcess:
    """cf を呼ぶ。失敗したときは標準エラーだけを出す（標準出力は secret を含み得る）。"""
    cmd = cf_base() + list(args)
    env = dict(os.environ)
    env["CF_SEND_TELEMETRY"] = "false"
    shown = "cf " + shlex.join(args)
    try:
        proc = subprocess.run(
            cmd,
            cwd=str(REPO_ROOT),
            env=env,
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
    except OSError as e:
        raise Failure("cf を実行できない（%s）: %s" % (e.strerror or e, shown))
    if proc.returncode != 0:
        raise Failure(
            "cf が失敗した（終了コード %d）: %s\n%s" % (proc.returncode, shown, proc.stderr.strip())
        )
    return proc


def cf_json(args: list[str]) -> Any:
    proc = run_cf(args)
    try:
        return json.loads(proc.stdout)
    except ValueError:
        # 標準出力は secret を含み得るので、中身は出さない。
        raise Failure("cf の出力が JSON として読めない: cf " + shlex.join(args))


def require_account() -> None:
    if not os.environ.get("CLOUDFLARE_ACCOUNT_ID"):
        raise Failure("CLOUDFLARE_ACCOUNT_ID を設定する")


def read_plan() -> WidgetPlan:
    widgets = cf_json(["turnstile", "widgets", "list", "--filter", "name:" + WIDGET_NAME])
    if not isinstance(widgets, list):
        raise Failure("cf turnstile widgets list の出力が配列でない")
    return plan_widget(widgets)


def print_plan(plan: WidgetPlan) -> None:
    if plan.action == "create":
        info("ウィジェット %s: 作る（%s）" % (WIDGET_NAME, body_json(widget_body())))
    elif plan.action == "update":
        info("ウィジェット %s: 直す（%s）" % (WIDGET_NAME, "・".join(plan.diffs)))
        info("サイトキー: %s" % plan.sitekey)
    else:
        info("ウィジェット %s: 変更なし" % WIDGET_NAME)
        info("サイトキー: %s" % plan.sitekey)


def confirm(yes: bool, what: str) -> None:
    if yes:
        return
    if not sys.stdin.isatty():
        raise Failure("標準入力が端末でないので確認できない。何も書いていない（--yes を付ける）")
    try:
        answer = input("%s%s。続けるなら yes と入力する: " % (PREFIX, what))
    except EOFError:
        answer = ""
    if answer.strip() != "yes":
        raise Failure("確認が得られなかったので、何も書いていない")


# ---------------------------------------------------------------------------
# モード
# ---------------------------------------------------------------------------


def mode_dry_run() -> int:
    info("ウィジェットを作るときに送る内容")
    proc = run_cf(["turnstile", "widgets", "create", "--body", body_json(widget_body()), "--dry-run"])
    sys.stdout.write(proc.stdout)
    sys.stdout.flush()
    info("--dry-run: 何も送っていない。今の状態は --check で確かめる")
    return 0


def mode_check() -> int:
    require_account()
    plan = read_plan()
    print_plan(plan)
    return 0


def mode_apply(secret_out: str, yes: bool) -> int:
    require_account()
    path = check_secret_out(secret_out)
    plan = read_plan()
    print_plan(plan)
    body = body_json(widget_body())
    sitekey = plan.sitekey
    secret = None
    if plan.action != "unchanged":
        confirm(yes, "Turnstile のウィジェット %s を%s" % (WIDGET_NAME, "作る" if plan.action == "create" else "直す"))
        if plan.action == "create":
            got = cf_json(["turnstile", "widgets", "create", "--body", body])
        else:
            got = cf_json(["turnstile", "widgets", "update", str(sitekey), "--body", body])
        if not isinstance(got, dict):
            raise Failure("cf の応答がオブジェクトでない")
        view = public_view(got)
        sitekey = view.get("sitekey") or sitekey
        if not isinstance(sitekey, str) or not sitekey:
            raise Failure("応答に sitekey がない")
        info(
            "%s: %s"
            % ("作った" if plan.action == "create" else "直した", body_json(view))
        )
        s = got.get("secret")
        if isinstance(s, str) and s:
            secret = s
    if secret is None:
        got = cf_json(["turnstile", "widgets", "get", str(sitekey)])
        s = got.get("secret") if isinstance(got, dict) else None
        if not isinstance(s, str) or not s:
            raise Failure("cf turnstile widgets get の応答にシークレットキーがない")
        secret = s
    try:
        write_secret(path, secret)
    except OSError as e:
        raise Failure("シークレットキーを書けない（%s）: %s" % (e.strerror or type(e).__name__, path))
    info("サイトキー: %s" % sitekey)
    info("シークレットキーを %s に書いた（権限 0600。GitHub の Secrets に登録したら消す）" % path)

    if plan.action != "unchanged":
        after = read_plan()
        if after.action != "unchanged" or after.sitekey != sitekey:
            err("反映の後の確認で差がある")
            print_plan(after)
            return 1
        info("反映の後の確認: 変更なし")
    print(NEXT_STEPS.format(path=shlex.quote(str(path)), sitekey=shlex.quote(sitekey)))
    return 0


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="cf_turnstile.py",
        description="Turnstile のウィジェット（2 つのホスト名）を cf で確かめ・作る（T-606）",
    )
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--dry-run", action="store_true", help="認証なしで送る内容を出す（既定）")
    group.add_argument("--check", action="store_true", help="今の状態を出す（書き込まない）")
    group.add_argument("--apply", action="store_true", help="作る（直す）。--secret-out が要る")
    parser.add_argument("--secret-out", metavar="PATH", help="シークレットキーを書くファイル（リポジトリの外。まだないこと）")
    parser.add_argument("--yes", action="store_true", help="--apply の確認を省く")
    args = parser.parse_args(argv)
    if args.apply and not args.secret_out:
        parser.error("--apply には --secret-out <パス> が要る")
    return args


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    try:
        if args.check:
            return mode_check()
        if args.apply:
            return mode_apply(args.secret_out, args.yes)
        return mode_dry_run()
    except Failure as e:
        err(str(e))
        return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
