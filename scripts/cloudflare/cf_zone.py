#!/usr/bin/env python3
"""ゾーンに置くルール（AI クローラーの遮断と /api/・/ws/ のレート制限）を cf で扱う（T-606）。

ADR-0018（コストガード G4）・ADR-0016（構成は cf だけで管理し、ゾーンの設定は
2 つのホスト名だけに効かせる）に従う。宣言は同じディレクトリの zone_rules.json。

使い方（どれか 1 つ。指定がなければ --dry-run）:
  python3 scripts/cloudflare/cf_zone.py [--dry-run]   # 認証なしで送る内容を出す
  python3 scripts/cloudflare/cf_zone.py --check       # 今の状態との差とサーバー側の検査
  python3 scripts/cloudflare/cf_zone.py --apply [--yes]
  python3 scripts/cloudflare/cf_zone.py --remove [--yes]

ref が raft-game-zone- で始まるルールだけを扱い、ゾーンのほかのルールには触れない。
詳しくは scripts/cloudflare/README.md。
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shlex
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, NamedTuple, Optional

ZONE = "kizuku-hackathon.work"
HOSTNAMES = ("raft.kizuku-hackathon.work", "raft-demo.kizuku-hackathon.work")
HOST_GUARD = '(http.host in {"raft.kizuku-hackathon.work" "raft-demo.kizuku-hackathon.work"})'
REF_PREFIX = "raft-game-zone-"
PHASES = ("http_request_firewall_custom", "http_ratelimit")

NAME = "cf_zone"
PREFIX = NAME + ": "
REPO_ROOT = Path(__file__).resolve().parent.parent.parent
CONFIG_PATH = Path(__file__).resolve().parent / "zone_rules.json"

REF_RE = re.compile(r"^raft-game-zone-[a-z0-9-]+$")
TOP_KEYS = {"zone", "hostnames", "phases"}
RULE_KEYS = {"ref", "description", "expression", "action", "enabled"}
RATELIMIT_KEYS = {"characteristics", "period", "requests_per_period", "mitigation_timeout"}
RATELIMIT_CHARACTERISTICS = ["cf.colo.id", "ip.src"]
CUSTOM_ACTIONS = ("block", "managed_challenge")
COMPARE_KEYS = ("expression", "action", "description", "enabled")


# ---------------------------------------------------------------------------
# 手元の検査
# ---------------------------------------------------------------------------


def _is_int(v: Any) -> bool:
    return isinstance(v, int) and not isinstance(v, bool)


def check_expression(expr: Any) -> Optional[str]:
    """式がホスト名の条件の中に閉じているかを確かめる。違反の理由（なければ None）を返す。"""
    if not isinstance(expr, str):
        return "expression が文字列でない"
    head = HOST_GUARD + " and ("
    if not expr.startswith(head) or not expr.endswith(")") or len(expr) < len(head) + 1:
        return "expression がホスト名の条件（HOST_GUARD and (…)）の形でない"
    inner = expr[len(head) : -1]
    if not inner.strip():
        return "expression のホスト名の条件のあとの条件が空"
    depth = 0
    in_string = False
    escaped = False
    for ch in inner:
        if in_string:
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == '"':
                in_string = False
            continue
        if ch == '"':
            in_string = True
        elif ch == "(":
            depth += 1
        elif ch == ")":
            depth -= 1
            if depth < 0:
                return "expression の条件がホスト名の条件の外に出ている（かっこの対応）"
    if in_string:
        return "expression の文字列リテラルが閉じていない"
    if depth != 0:
        return "expression のかっこが閉じていない"
    return None


def validate_config(cfg: Any) -> list[str]:
    """zone_rules.json の内容を検査し、違反の文の一覧を返す（空なら合格）。"""
    errors: list[str] = []
    if not isinstance(cfg, dict):
        return ["最上位が JSON のオブジェクトでない"]
    keys = set(cfg)
    if keys != TOP_KEYS:
        errors.append(
            "最上位のキーは zone・hostnames・phases だけ（余分: %s、不足: %s）"
            % (sorted(keys - TOP_KEYS), sorted(TOP_KEYS - keys))
        )
    if "zone" in cfg and cfg["zone"] != ZONE:
        errors.append("zone が %s でない: %r" % (ZONE, cfg["zone"]))
    if "hostnames" in cfg:
        hosts = cfg["hostnames"]
        if not isinstance(hosts, list) or not all(isinstance(h, str) for h in hosts):
            errors.append("hostnames が文字列の配列でない")
        else:
            if any("*" in h for h in hosts):
                errors.append("hostnames に * を含む値がある")
            if len(hosts) != len(set(hosts)):
                errors.append("hostnames に重複がある")
            if len(hosts) != len(HOSTNAMES) or set(hosts) != set(HOSTNAMES):
                errors.append("hostnames が %s の 2 つでない: %r" % (list(HOSTNAMES), hosts))

    phases = cfg.get("phases")
    if "phases" in cfg and not isinstance(phases, dict):
        errors.append("phases が JSON のオブジェクトでない")
        phases = None
    seen_refs: set[str] = set()
    for phase, rules in (phases or {}).items():
        if phase not in PHASES:
            errors.append("phases に知らないフェーズがある: %r" % phase)
            continue
        if not isinstance(rules, list):
            errors.append("%s: ルールの配列でない" % phase)
            continue
        if phase == "http_ratelimit" and len(rules) > 1:
            errors.append("http_ratelimit: ルールは 1 つまで（%d 個ある）" % len(rules))
        for i, rule in enumerate(rules):
            errors.extend(_validate_rule(phase, i, rule, seen_refs))
    return errors


def _validate_rule(phase: str, index: int, rule: Any, seen_refs: set) -> list[str]:
    where = "%s[%d]" % (phase, index)
    if not isinstance(rule, dict):
        return ["%s: ルールが JSON のオブジェクトでない" % where]
    errors: list[str] = []
    ref = rule.get("ref")
    if isinstance(ref, str):
        where = "%s[%d]（%s）" % (phase, index, ref)
    allowed = RULE_KEYS | ({"ratelimit"} if phase == "http_ratelimit" else set())
    unknown = set(rule) - allowed
    if unknown:
        errors.append("%s: 知らないキーがある: %s" % (where, sorted(unknown)))
    missing = RULE_KEYS - set(rule)
    if phase == "http_ratelimit" and "ratelimit" not in rule:
        missing = missing | {"ratelimit"}
    if missing:
        errors.append("%s: キーが足りない: %s" % (where, sorted(missing)))

    if not isinstance(ref, str) or not REF_RE.match(ref):
        errors.append("%s: ref が ^raft-game-zone-[a-z0-9-]+$ に合わない: %r" % (where, ref))
    elif ref in seen_refs:
        errors.append("%s: ref が重複している" % where)
    else:
        seen_refs.add(ref)

    desc = rule.get("description")
    if not isinstance(desc, str) or not (1 <= len(desc) <= 500) or not desc.isascii():
        errors.append("%s: description は 1〜500 文字の ASCII にする" % where)
    if rule.get("enabled") is not True:
        errors.append("%s: enabled は true にする" % where)
    if "expression" in rule:
        reason = check_expression(rule.get("expression"))
        if reason:
            errors.append("%s: %s" % (where, reason))

    action = rule.get("action")
    if phase == "http_request_firewall_custom":
        if action not in CUSTOM_ACTIONS:
            errors.append("%s: action は block か managed_challenge にする: %r" % (where, action))
    elif phase == "http_ratelimit":
        if action != "block":
            errors.append("%s: action は block にする: %r" % (where, action))
        if "ratelimit" in rule:
            errors.extend(_validate_ratelimit(where, rule["ratelimit"]))
    return errors


def _validate_ratelimit(where: str, rl: Any) -> list[str]:
    if not isinstance(rl, dict):
        return ["%s: ratelimit が JSON のオブジェクトでない" % where]
    errors: list[str] = []
    if set(rl) != RATELIMIT_KEYS:
        errors.append(
            "%s: ratelimit のキーは %s だけ: %s" % (where, sorted(RATELIMIT_KEYS), sorted(rl))
        )
    if rl.get("characteristics") != RATELIMIT_CHARACTERISTICS:
        errors.append(
            "%s: ratelimit.characteristics は %s にする（Free プランの制約）"
            % (where, RATELIMIT_CHARACTERISTICS)
        )
    for key in ("period", "mitigation_timeout"):
        if not _is_int(rl.get(key)) or rl.get(key) != 10:
            errors.append("%s: ratelimit.%s は 10 にする（Free プランの制約）" % (where, key))
    rpp = rl.get("requests_per_period")
    if not _is_int(rpp) or not (1 <= rpp <= 10000):
        errors.append("%s: ratelimit.requests_per_period は 1〜10000 の整数にする" % where)
    return errors


# ---------------------------------------------------------------------------
# 差分の計算
# ---------------------------------------------------------------------------


class Update(NamedTuple):
    rule_id: str
    rule: dict
    diffs: list


@dataclass
class Plan:
    create: list = field(default_factory=list)  # 作る（desired のルール）
    update: list = field(default_factory=list)  # 直す（Update）
    delete: list = field(default_factory=list)  # 消す（今のルール）
    unchanged: list = field(default_factory=list)  # 変更なし（ref）
    others: int = 0  # ほかのルールの数（触らない）
    entrypoint_missing: bool = False

    def changes(self) -> int:
        return len(self.create) + len(self.update) + len(self.delete)


def _norm_ratelimit_value(key: str, value: Any) -> Any:
    if key == "characteristics" and isinstance(value, list):
        return sorted(value, key=str)
    return value


def rule_diffs(desired: dict, current: dict) -> list[str]:
    """desired と今のルールで違う項目の名前を返す（応答の余分なキーは無視する）。"""
    diffs: list[str] = []
    for key in COMPARE_KEYS:
        cur = current.get(key, True) if key == "enabled" else current.get(key)
        if desired.get(key) != cur:
            diffs.append(key)
    if "ratelimit" in desired:
        cur_rl = current.get("ratelimit")
        if not isinstance(cur_rl, dict):
            diffs.append("ratelimit")
        else:
            for key, value in desired["ratelimit"].items():
                if _norm_ratelimit_value(key, value) != _norm_ratelimit_value(key, cur_rl.get(key)):
                    diffs.append("ratelimit." + key)
    return diffs


def plan_phase(desired: list, existing_rules: Optional[list]) -> Plan:
    """1 つのフェーズについて、作る・直す・消す・変更なし・ほかのルールの数を決める。"""
    if existing_rules is None:
        return Plan(create=list(desired), entrypoint_missing=True)
    plan = Plan()
    by_ref: dict = {}
    ours: list = []
    for rule in existing_rules:
        ref = rule.get("ref") if isinstance(rule, dict) else None
        if not isinstance(ref, str) or not ref.startswith(REF_PREFIX):
            plan.others += 1
            continue
        ours.append(rule)
        by_ref.setdefault(ref, rule)
    desired_refs = set()
    for want in desired:
        ref = want["ref"]
        desired_refs.add(ref)
        cur = by_ref.get(ref)
        if cur is None:
            plan.create.append(want)
            continue
        diffs = rule_diffs(want, cur)
        if diffs:
            plan.update.append(Update(cur.get("id"), want, diffs))
        else:
            plan.unchanged.append(ref)
    for rule in ours:
        ref = rule["ref"]
        if ref not in desired_refs or by_ref.get(ref) is not rule:
            plan.delete.append(rule)
    return plan


# ---------------------------------------------------------------------------
# cf の呼び出し
# ---------------------------------------------------------------------------


class Failure(Exception):
    """利用者に文を出して終了コード 1 で終わる。"""


def info(msg: str) -> None:
    print(PREFIX + msg, flush=True)


def err(msg: str) -> None:
    print(PREFIX + msg, file=sys.stderr, flush=True)


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
        detail = proc.stderr.strip() or proc.stdout.strip()
        raise Failure("cf が失敗した（終了コード %d）: %s\n%s" % (proc.returncode, shown, detail))
    return proc


def cf_json(args: list[str]) -> Any:
    proc = run_cf(args)
    try:
        return json.loads(proc.stdout)
    except ValueError:
        raise Failure("cf の出力が JSON として読めない: cf " + shlex.join(args))


# ---------------------------------------------------------------------------
# 状態の読み込み
# ---------------------------------------------------------------------------


@dataclass
class PhaseState:
    phase: str
    ruleset_id: Optional[str]
    rules: Optional[list]
    plan: Plan


def find_zone() -> str:
    zones = cf_json(["zones", "list", "--name", ZONE])
    if not isinstance(zones, list):
        raise Failure("cf zones list の出力が配列でない")
    matches = [z for z in zones if isinstance(z, dict) and z.get("name") == ZONE]
    if not matches:
        raise Failure("ゾーンが見つからない（認証のアカウントを確かめる）: " + ZONE)
    if len(matches) > 1:
        raise Failure("同じ名前のゾーンが複数ある: " + ZONE)
    zone = matches[0]
    zone_id = zone.get("id")
    if not isinstance(zone_id, str) or not zone_id:
        raise Failure("ゾーンの id が読めない")
    plan = zone.get("plan") if isinstance(zone.get("plan"), dict) else {}
    plan_name = plan.get("legacy_id") or plan.get("name") or "（不明）"
    info("ゾーン %s（id %s・プラン %s）" % (ZONE, zone_id, plan_name))
    return zone_id


def read_phases(zone_id: str, cfg: dict) -> list[PhaseState]:
    rulesets = cf_json(["rulesets", "list", "--zone", zone_id])
    if not isinstance(rulesets, list):
        raise Failure("cf rulesets list の出力が配列でない")
    states: list[PhaseState] = []
    for phase in PHASES:
        found = [
            r
            for r in rulesets
            if isinstance(r, dict) and r.get("kind") == "zone" and r.get("phase") == phase
        ]
        if len(found) > 1:
            raise Failure("フェーズ %s のエントリーポイントのルールセットが複数ある" % phase)
        ruleset_id = None
        rules = None
        if found:
            ruleset_id = found[0].get("id")
            got = cf_json(["rulesets", "get", str(ruleset_id), "--zone", zone_id])
            if not isinstance(got, dict):
                raise Failure("cf rulesets get の出力がオブジェクトでない")
            rules = got.get("rules") or []
            if not isinstance(rules, list):
                raise Failure("cf rulesets get の rules が配列でない")
        desired = cfg["phases"].get(phase, [])
        states.append(PhaseState(phase, ruleset_id, rules, plan_phase(desired, rules)))
    return states


def print_plan(states: list[PhaseState]) -> int:
    total = 0
    for st in states:
        where = st.ruleset_id if st.ruleset_id else "まだない（エントリーポイントを作る）"
        info("フェーズ %s（ルールセット %s）" % (st.phase, where))
        p = st.plan
        for rule in p.create:
            print("  作る: %s" % rule["ref"])
        for up in p.update:
            print("  直す: %s（%s）" % (up.rule["ref"], "・".join(up.diffs)))
        for rule in p.delete:
            print("  消す: %s" % rule.get("ref"))
        for ref in p.unchanged:
            print("  変更なし: %s" % ref)
        print("  ほかのルール: %d 個（触らない）" % p.others)
        total += p.changes()
    return total


def server_validate(zone_id: str, states: list[PhaseState]) -> None:
    for st in states:
        p = st.plan
        if p.entrypoint_missing:
            if p.create:
                run_cf(
                    [
                        "rulesets", "entrypoint", "update", st.phase,
                        "--zone", zone_id, "--validate-only",
                        "--body", body_json({"rules": p.create}),
                    ]
                )
            continue
        for rule in p.create:
            run_cf(
                [
                    "rulesets", "rules", "create", str(st.ruleset_id),
                    "--zone", zone_id, "--validate-only", "--body", body_json(rule),
                ]
            )
        for up in p.update:
            run_cf(
                [
                    "rulesets", "rules", "update", str(up.rule_id),
                    "--ruleset-id", str(st.ruleset_id),
                    "--zone", zone_id, "--validate-only", "--body", body_json(up.rule),
                ]
            )
    info("サーバー側の検査（--validate-only）: 通った")


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


def mode_dry_run(cfg: dict) -> int:
    for phase in PHASES:
        for rule in cfg["phases"].get(phase, []):
            info("%s: ルールを足すときに送る内容（%s）" % (phase, rule["ref"]))
            proc = run_cf(
                [
                    "rulesets", "rules", "create", "RULESET_ID",
                    "--zone", ZONE, "--body", body_json(rule), "--dry-run",
                ]
            )
            sys.stdout.write(proc.stdout)
            sys.stdout.flush()
    for phase in PHASES:
        rules = cfg["phases"].get(phase, [])
        if not rules:
            continue
        info("%s: エントリーポイントがまだないときに送る内容" % phase)
        proc = run_cf(
            [
                "rulesets", "entrypoint", "update", phase,
                "--zone", ZONE, "--body", body_json({"rules": rules}), "--dry-run",
            ]
        )
        sys.stdout.write(proc.stdout)
        sys.stdout.flush()
    info("--dry-run: 何も送っていない。今の状態との差は --check で確かめる")
    return 0


def do_check(cfg: dict) -> tuple:
    zone_id = find_zone()
    states = read_phases(zone_id, cfg)
    total = print_plan(states)
    if total:
        server_validate(zone_id, states)
    return zone_id, states, total


def mode_check(cfg: dict) -> int:
    _, _, total = do_check(cfg)
    if total:
        info("変更あり（%d 件）。--apply で反映する" % total)
    else:
        info("変更なし")
    return 0


HUMAN_CHECKS = """\
人が確かめること:
  - curl -s -o /dev/null -w '%{http_code}\\n' https://raft.kizuku-hackathon.work/api/healthz が 200（最初のデプロイの後）
  - 同じゾーンのほかのサービスが今までどおり動く
  - もう一度 python3 scripts/cloudflare/cf_zone.py --check が「変更なし」"""


def mode_apply(cfg: dict, yes: bool) -> int:
    zone_id, states, total = do_check(cfg)
    if not total:
        info("変更なし")
        return 0
    confirm(yes, "ゾーン %s に %d 件の変更を反映する" % (ZONE, total))
    try:
        for st in states:
            apply_phase(zone_id, st)
    except Failure as e:
        err(str(e))
        err("反映の途中で止めた。それまでの反映は残っている。もう一度 --check で状態を見る")
        return 1
    after = read_phases(zone_id, cfg)
    remaining = print_plan(after)
    if remaining:
        err("反映の後の確認で差がある")
        return 1
    info("反映した。すべて変更なし")
    print(HUMAN_CHECKS)
    return 0


def apply_phase(zone_id: str, st: PhaseState) -> None:
    p = st.plan
    if p.entrypoint_missing:
        if p.create:
            run_cf(
                [
                    "rulesets", "entrypoint", "update", st.phase,
                    "--zone", zone_id, "--body", body_json({"rules": p.create}),
                ]
            )
            info(
                "%s: エントリーポイントを作った（%s）"
                % (st.phase, "・".join(r["ref"] for r in p.create))
            )
        return
    rs = str(st.ruleset_id)
    for rule in p.create:
        run_cf(["rulesets", "rules", "create", rs, "--zone", zone_id, "--body", body_json(rule)])
        info("%s: 作った: %s" % (st.phase, rule["ref"]))
    for up in p.update:
        run_cf(
            [
                "rulesets", "rules", "update", str(up.rule_id), "--ruleset-id", rs,
                "--zone", zone_id, "--body", body_json(up.rule),
            ]
        )
        info("%s: 直した: %s（%s）" % (st.phase, up.rule["ref"], "・".join(up.diffs)))
    for rule in p.delete:
        delete_rule(zone_id, rs, rule)
        info("%s: 消した: %s" % (st.phase, rule.get("ref")))


def delete_rule(zone_id: str, ruleset_id: str, rule: dict) -> None:
    ref = rule.get("ref")
    if not isinstance(ref, str) or not ref.startswith(REF_PREFIX):
        # plan_phase の約束を破らないための最後の確かめ。
        raise Failure("%s で始まらないルールは消さない: %r" % (REF_PREFIX, ref))
    run_cf(
        [
            "rulesets", "rules", "delete", str(rule.get("id")),
            "--ruleset-id", ruleset_id, "--zone", zone_id, "--force",
        ]
    )


def mode_remove(cfg: dict, yes: bool) -> int:
    zone_id = find_zone()
    states = read_phases(zone_id, cfg)
    targets = []
    for st in states:
        for rule in st.rules or []:
            ref = rule.get("ref") if isinstance(rule, dict) else None
            if isinstance(ref, str) and ref.startswith(REF_PREFIX):
                targets.append((st, rule))
    if not targets:
        info("消すものはない")
        return 0
    for st, rule in targets:
        print("  消す: %s（フェーズ %s・ルール %s）" % (rule["ref"], st.phase, rule.get("id")))
    confirm(yes, "%d 個のルールを消す" % len(targets))
    try:
        for st, rule in targets:
            delete_rule(zone_id, str(st.ruleset_id), rule)
            info("消した: %s" % rule["ref"])
    except Failure as e:
        err(str(e))
        err("途中で止めた。もう一度 --check で状態を見る")
        return 1
    info("消した（%d 個）。エントリーポイントのルールセット自体は残している" % len(targets))
    return 0


def load_config(path: Path) -> Any:
    try:
        with path.open(encoding="utf-8") as f:
            return json.load(f)
    except (OSError, ValueError) as e:
        raise Failure("%s が読めない: %s" % (path, e))


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="cf_zone.py",
        description="ゾーンのルール（AI クローラーの遮断・/api/ と /ws/ のレート制限）を cf で扱う（T-606）",
    )
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--dry-run", action="store_true", help="認証なしで送る内容を出す（既定）")
    group.add_argument("--check", action="store_true", help="今の状態との差を出す（書き込まない）")
    group.add_argument("--apply", action="store_true", help="差を反映する")
    group.add_argument("--remove", action="store_true", help="raft-game-zone- のルールだけを消す")
    parser.add_argument("--yes", action="store_true", help="--apply・--remove の確認を省く")
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    try:
        cfg = load_config(CONFIG_PATH)
        errors = validate_config(cfg)
        if errors:
            for e in errors:
                err("zone_rules.json: " + e)
            return 1
        if args.check:
            return mode_check(cfg)
        if args.apply:
            return mode_apply(cfg, args.yes)
        if args.remove:
            return mode_remove(cfg, args.yes)
        return mode_dry_run(cfg)
    except Failure as e:
        err(str(e))
        return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
