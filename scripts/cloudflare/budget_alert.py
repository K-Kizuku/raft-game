#!/usr/bin/env python3
"""Cloudflare の予算アラート（ADR-0018 G6）を `cf billing spend-policies` で確かめ・作り・直す（T-607 / Issue #71）。

使い方:
    python3 scripts/cloudflare/budget_alert.py [--dry-run | --check | --apply [--yes]] [--email <アドレス>] [--threshold-usd <整数>]
      --dry-run        （既定）作るはずの 2 つのポリシーを、cf の --dry-run で送る内容として出す。認証なし
      --check          今のポリシーを読み、何を作る・直すかを出す。書き込まない
      --apply          --check の後、差があれば作る・直し、もう一度読んで差がないことを確かめる
      --yes            --apply の確認を省く
      --email          通知を受けるアドレス（既定は環境変数 BUDGET_ALERT_EMAIL。--check・--apply では必須）
      --threshold-usd  しきい値（USD の整数、1〜30。既定 5。ADR-0018 G6）

作るポリシーは、製品 `workers`・`durable_objects` のそれぞれに 1 つずつ（名前は
`raft-game-budget-workers`・`raft-game-budget-durable-objects`）。`action` は常に
`notification`（しきい値に達したらメールを送るだけで、利用は止めない）。
`usage_block`（利用を止める）は送らない。ほかのポリシー（Cloudflare が既定で作る
アラートを含む）は表に出すだけで触らない。ポリシーを消すモードはない（消すときは
手順書 docs/runbooks/cost-emergency.md のとおり、人が `cf billing spend-policies delete` を使う）。

- `cf` は環境変数 `CF` があれば `shlex.split` した値、なければ `<根>/web/node_modules/.bin/cf`
  を、リポジトリの根を作業ディレクトリにして呼ぶ（子の環境に `CF_SEND_TELEMETRY=false`）
- `--check`・`--apply` は環境変数 `CLOUDFLARE_ACCOUNT_ID` が必要
- `list` が `[1003] spend policy not found`（404）で失敗したときは、ポリシーが 0 件とみなす
  （Issue #223）。ほかの失敗は今までどおり失敗にする
- 終了コード: 成功 0、違反・cf の失敗・反映の後の差・確認の拒否 1、使い方の誤り 2
- アドレスはリポジトリに書かない（引数か環境変数で渡す）

標準ライブラリだけを使う（Python 3.9 以上）。
"""

import argparse
import json
import os
import re
import shlex
import subprocess
import sys
from pathlib import Path
from typing import Any, Dict, List, Optional, Sequence, Tuple

ROOT = Path(__file__).resolve().parent.parent.parent

PREFIX = "budget_alert: "
PRODUCTS = ("workers", "durable_objects")
NAME_PREFIX = "raft-game-budget-"
DEFAULT_THRESHOLD_USD = 5
# 月の上限 30 USD（ADR-0018 G1）。設定の誤りで大きな値を入れない。
MAX_THRESHOLD_USD = 30
MIN_THRESHOLD_USD = 1
ACTION = "notification"
DRY_RUN_EMAIL = "you@example.com"
EMAIL_ENV = "BUDGET_ALERT_EMAIL"
ACCOUNT_ENV = "CLOUDFLARE_ACCOUNT_ID"
EMAIL_RE = re.compile(r'^[^@\s<>"]+@[^@\s<>"]+\.[^@\s<>"]+$')
MAX_EMAIL_LEN = 254
THRESHOLD_RE = re.compile(r"^[0-9]+$")
# 自分のポリシーで比べるフィールド（name は探すのに使う）。
COMPARE_FIELDS = ("action", "enabled", "product_id", "threshold_cents", "notification_email")
SPEND_POLICIES = ("billing", "spend-policies")
# ポリシーが 0 件のとき、`list` が空の配列ではなく 404 のこの文を返すことがある（ベータ版の API の癖。
# T-607b / Issue #223）。`list` でこの文のときだけ 0 件とみなし、ほかの失敗（403 など）は失敗にする。
POLICY_NOT_FOUND_RE = re.compile(r"\[1003\]\s*spend policy not found", re.IGNORECASE)


class UsageError(Exception):
    """使い方の誤り（終了コード 2）。"""


class Failure(Exception):
    """違反・cf の失敗など（終了コード 1）。"""


# ---------------------------------------------------------------------------
# 純粋な部分（テストが importlib で読み込んで使う）
# ---------------------------------------------------------------------------


def policy_name(product: str) -> str:
    """製品のポリシーの名前（`_` は `-` にする）。"""
    return NAME_PREFIX + product.replace("_", "-")


def parse_threshold_usd(value: str) -> int:
    """しきい値（USD の整数、1〜30）を読む。誤りなら UsageError。"""
    text = value.strip()
    if not THRESHOLD_RE.match(text):
        raise UsageError(f"--threshold-usd は整数で指定する（{value!r}）")
    usd = int(text)
    if usd < MIN_THRESHOLD_USD or usd > MAX_THRESHOLD_USD:
        raise UsageError(
            f"--threshold-usd は {MIN_THRESHOLD_USD}〜{MAX_THRESHOLD_USD} で指定する（{value!r}）"
        )
    return usd


def validate_email(value: str) -> str:
    """アドレスの前後の空白を除いて検査し、返す。誤りなら UsageError。"""
    text = value.strip()
    if not text:
        raise UsageError("通知のアドレスが空")
    if len(text) > MAX_EMAIL_LEN or not EMAIL_RE.match(text):
        raise UsageError(f"通知のアドレスの形が違う（user@example.com の形だけ）: {value!r}")
    return text


def build_body(product: str, threshold_usd: int, email: str) -> Dict[str, Any]:
    """製品ごとのポリシーの本文。"""
    if product not in PRODUCTS:
        raise ValueError(f"知らない製品: {product}")
    return {
        "name": policy_name(product),
        "action": ACTION,
        "enabled": True,
        "product_id": product,
        "threshold_cents": threshold_usd * 100,
        "notification_email": email,
    }


def build_bodies(threshold_usd: int, email: str) -> List[Dict[str, Any]]:
    return [build_body(p, threshold_usd, email) for p in PRODUCTS]


def ensure_notification(body: Dict[str, Any]) -> None:
    """送る前の検査。`notification` 以外（`usage_block` など）を送る経路を作らない。"""
    if body.get("action") != ACTION:
        raise Failure(f"action が {ACTION} でない本文は送らない（{body.get('action')!r}）")


def _same(a: Any, b: Any) -> bool:
    # True と 1 を同じとみなさない。
    return type(a) is type(b) and a == b


def diff_policy(current: Dict[str, Any], desired: Dict[str, Any]) -> List[str]:
    """違うフィールドの名前の一覧（COMPARE_FIELDS の順）。"""
    return [f for f in COMPARE_FIELDS if not _same(current.get(f), desired[f])]


def compute_plan(policies: Sequence[Dict[str, Any]], bodies: Sequence[Dict[str, Any]]) -> List[Dict[str, Any]]:
    """製品ごとに、作る（create）・直す（update）・変更なし（none）を決める。

    同じ名前のポリシーが 2 つ以上あれば Failure。
    """
    plan = []
    for body in bodies:
        same = [p for p in policies if isinstance(p, dict) and p.get("name") == body["name"]]
        if len(same) > 1:
            ids = "・".join(str(p.get("id")) for p in same)
            raise Failure(f"同じ名前のポリシーが複数ある（{body['name']}: {ids}）。どれを残すか人が決める")
        if not same:
            plan.append({"op": "create", "body": body, "policy": None, "diffs": []})
            continue
        diffs = diff_policy(same[0], body)
        plan.append({"op": "update" if diffs else "none", "body": body, "policy": same[0], "diffs": diffs})
    return plan


def own_names() -> Tuple[str, ...]:
    return tuple(policy_name(p) for p in PRODUCTS)


def other_policies(policies: Sequence[Dict[str, Any]]) -> List[Dict[str, Any]]:
    names = own_names()
    return [p for p in policies if isinstance(p, dict) and p.get("name") not in names]


def usage_block_policies(policies: Sequence[Dict[str, Any]]) -> List[Dict[str, Any]]:
    """ほかのポリシーのうち、`usage_block` で有効なもの。"""
    return [p for p in other_policies(policies) if p.get("action") == "usage_block" and p.get("enabled") is True]


def format_table(policies: Sequence[Dict[str, Any]]) -> List[str]:
    cols = ("name", "action", "product_id", "threshold_cents", "enabled")
    rows = [[_cell(p.get(c)) for c in cols] for p in policies]
    widths = [max([len(c)] + [len(r[i]) for r in rows]) for i, c in enumerate(cols)]
    lines = ["  " + "  ".join(c.ljust(widths[i]) for i, c in enumerate(cols)).rstrip()]
    for r in rows:
        lines.append("  " + "  ".join(v.ljust(widths[i]) for i, v in enumerate(r)).rstrip())
    return lines


def _cell(v: Any) -> str:
    if v is None:
        return "-"
    if isinstance(v, bool):
        return "true" if v else "false"
    return str(v)


def describe(item: Dict[str, Any]) -> str:
    body = item["body"]
    product = body["product_id"]
    if item["op"] == "create":
        return f"{product}: 作る（{body['name']}、{body['threshold_cents']} セント、{body['action']}）"
    if item["op"] == "none":
        return f"{product}: 変更なし（{body['name']}）"
    cur = item["policy"]
    parts = [f"{f}: {_cell(cur.get(f))} → {_cell(body[f])}" for f in item["diffs"]]
    return f"{product}: 直す（{body['name']}、id {cur.get('id')}。" + "、".join(parts) + "）"


def body_json(body: Dict[str, Any]) -> str:
    return json.dumps(body, ensure_ascii=False, separators=(",", ":"))


def is_policy_not_found(text: str) -> bool:
    """cf の失敗の出力が `[1003] spend policy not found` か。"""
    return bool(POLICY_NOT_FOUND_RE.search(text))


# ---------------------------------------------------------------------------
# cf の呼び出し
# ---------------------------------------------------------------------------


def cf_command() -> List[str]:
    value = os.environ.get("CF", "")
    if value.strip():
        return shlex.split(value)
    return [str(ROOT / "web" / "node_modules" / ".bin" / "cf")]


def run_cf(args: Sequence[str]) -> str:
    """cf を呼び、標準出力を返す。失敗したら cf の文を標準エラーに出して Failure。"""
    out = call_cf(args, not_found_ok=False)
    assert out is not None
    return out


def call_cf(args: Sequence[str], not_found_ok: bool) -> Optional[str]:
    """run_cf の本体。not_found_ok が真で、失敗の出力が `[1003] spend policy not found` のときは None を返す。"""
    cmd = cf_command() + list(SPEND_POLICIES) + list(args)
    env = dict(os.environ)
    env["CF_SEND_TELEMETRY"] = "false"
    try:
        proc = subprocess.run(cmd, cwd=str(ROOT), env=env, capture_output=True, text=True)
    except OSError as e:
        raise Failure(f"cf を実行できない（{cmd[0]}）: {e}。pnpm install の後に実行するか、環境変数 CF を設定する")
    label = " ".join(["cf"] + list(SPEND_POLICIES) + list(args[:1]))
    if proc.returncode != 0 and not_found_ok and is_policy_not_found((proc.stderr or "") + "\n" + (proc.stdout or "")):
        warn(f"{label} が [1003] spend policy not found を返した。ポリシーは 0 件とみなす")
        return None
    if proc.stderr:
        sys.stderr.write(proc.stderr)
    if proc.returncode != 0:
        if proc.stdout:
            sys.stderr.write(proc.stdout)
        raise Failure(f"{label} が失敗した（終了コード {proc.returncode}）")
    return proc.stdout


def parse_json(text: str, what: str) -> Any:
    try:
        return json.loads(text)
    except ValueError:
        raise Failure(f"{what} の出力が JSON でない: {text.strip()[:200]!r}")


def list_policies() -> List[Dict[str, Any]]:
    out = call_cf(["list"], not_found_ok=True)
    if out is None:
        return []
    data = parse_json(out, "cf billing spend-policies list")
    if not isinstance(data, list):
        raise Failure("cf billing spend-policies list の出力が配列でない")
    return data


def policy_version(policy: Dict[str, Any]) -> str:
    version = policy.get("version")
    if version is None:
        pid = str(policy.get("id"))
        got = parse_json(run_cf(["get", pid]), "cf billing spend-policies get")
        if isinstance(got, dict):
            version = got.get("version")
    if version is None:
        raise Failure(f"ポリシー {policy.get('id')} の version がわからない（--if-match を送れない）")
    return str(version)


# ---------------------------------------------------------------------------
# モード
# ---------------------------------------------------------------------------


def say(msg: str) -> None:
    print(PREFIX + msg, flush=True)


def warn(msg: str) -> None:
    print(PREFIX + msg, file=sys.stderr, flush=True)


def do_dry_run(bodies: Sequence[Dict[str, Any]]) -> int:
    for body in bodies:
        ensure_notification(body)
        out = run_cf(["create", "--body", body_json(body), "--dry-run"])
        sys.stdout.write(out)
        if out and not out.endswith("\n"):
            sys.stdout.write("\n")
        sys.stdout.flush()
    say("--dry-run: 何も送っていない。今の状態との差は --check で確かめる")
    return 0


def check(bodies: Sequence[Dict[str, Any]]) -> List[Dict[str, Any]]:
    """一覧を読み、計画と表を出す。計画を返す。"""
    policies = list_policies()
    plan = compute_plan(policies, bodies)
    for item in plan:
        say(describe(item))
    others = other_policies(policies)
    if others:
        say("ほかのポリシー（触らない）:")
        for line in format_table(others):
            print(line)
    else:
        say("ほかのポリシー: なし")
    for p in usage_block_policies(policies):
        say(
            f"注意: 利用を止めるポリシーがある（{p.get('name')}）。"
            f"しきい値に達すると、その製品（{p.get('product_id')}）が請求期間の終わりまで止まる"
        )
    changes = [i for i in plan if i["op"] != "none"]
    if changes:
        say(f"変更あり（{len(changes)} 件）。--apply で反映する")
    else:
        say("変更なし")
    return plan


def confirm(n: int) -> bool:
    if not sys.stdin.isatty():
        warn("確認できない（標準入力が端末でない）。確かめたうえで --yes を付ける")
        return False
    try:
        answer = input(f"{PREFIX}{n} 件を反映する。よければ yes と入力: ")
    except EOFError:
        return False
    return answer.strip() == "yes"


def do_apply(bodies: Sequence[Dict[str, Any]], yes: bool, threshold_usd: int, email: str) -> int:
    plan = check(bodies)
    changes = [i for i in plan if i["op"] != "none"]
    if not changes:
        return 0
    if not yes and not confirm(len(changes)):
        warn("反映をやめた")
        return 1
    for item in changes:
        body = item["body"]
        ensure_notification(body)
        if item["op"] == "create":
            run_cf(["create", "--body", body_json(body)])
            say(f"作った: {body['name']}")
        else:
            policy = item["policy"]
            pid = policy.get("id")
            if pid is None:
                raise Failure(f"ポリシー {body['name']} に id がない")
            version = policy_version(policy)
            run_cf(["update", str(pid), "--if-match", version, "--body", body_json(body)])
            say(f"直した: {body['name']}")
    after = compute_plan(list_policies(), bodies)
    if any(i["op"] != "none" for i in after):
        for i in after:
            if i["op"] != "none":
                warn(describe(i))
        warn("反映の後の確認で差がある")
        return 1
    say(
        f"予算アラート（{threshold_usd} USD）: workers・durable_objects。"
        f"通知は {email}。届くのは前日の分が翌日（ADR-0018）"
    )
    return 0


class _Parser(argparse.ArgumentParser):
    def error(self, message: str) -> "None":  # type: ignore[override]
        self.print_usage(sys.stderr)
        warn(message)
        sys.exit(2)


def make_parser() -> argparse.ArgumentParser:
    parser = _Parser(
        prog="budget_alert.py",
        description="Cloudflare の予算アラート（ADR-0018 G6）を cf billing spend-policies で確かめ・作り・直す",
    )
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--dry-run", action="store_true", help="（既定）送る内容を cf の --dry-run で出す。認証なし")
    mode.add_argument("--check", action="store_true", help="今のポリシーを読み、何を作る・直すかを出す")
    mode.add_argument("--apply", action="store_true", help="--check の後、差があれば作る・直す")
    parser.add_argument("--yes", action="store_true", help="--apply の確認を省く")
    parser.add_argument("--email", help=f"通知を受けるアドレス（既定は環境変数 {EMAIL_ENV}）")
    parser.add_argument(
        "--threshold-usd",
        default=str(DEFAULT_THRESHOLD_USD),
        help=f"しきい値（USD の整数、{MIN_THRESHOLD_USD}〜{MAX_THRESHOLD_USD}。既定 {DEFAULT_THRESHOLD_USD}）",
    )
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = make_parser()
    args = parser.parse_args(argv)
    writes = args.check or args.apply

    try:
        threshold_usd = parse_threshold_usd(args.threshold_usd)
        raw_email = args.email if args.email is not None else os.environ.get(EMAIL_ENV, "")
        if raw_email.strip():
            email = validate_email(raw_email)
        elif writes:
            raise UsageError(f"--check・--apply では通知のアドレスが要る（--email か環境変数 {EMAIL_ENV}）")
        else:
            email = DRY_RUN_EMAIL
            say(f"通知のアドレスがない（--email・{EMAIL_ENV}）ので、{DRY_RUN_EMAIL} で出す")
    except UsageError as e:
        parser.error(str(e))
        return 2

    bodies = build_bodies(threshold_usd, email)
    try:
        if not writes:
            return do_dry_run(bodies)
        if not os.environ.get(ACCOUNT_ENV, "").strip():
            raise Failure(f"環境変数 {ACCOUNT_ENV} がない（アカウントの選択を cf に対話で求めさせない）")
        if args.check:
            check(bodies)
            return 0
        return do_apply(bodies, args.yes, threshold_usd, email)
    except Failure as e:
        warn(str(e))
        return 1


if __name__ == "__main__":
    sys.exit(main())
