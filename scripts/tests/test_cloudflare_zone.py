"""scripts/cloudflare/cf_zone.py と cf_turnstile.py のテスト（T-606 / Issue #70 / ADR-0018 G4）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_cloudflare_zone.py'

ネットワーク・本物の cf・認証の情報を使わない。偽の cf（このテストが一時ディレクトリに
書く Python のスクリプト）を環境変数 CF で渡す。偽の cf は、呼ばれた引数を JSON の
1 行ずつで記録し、状態（ゾーン・ルールセット・ウィジェット）を JSON のファイルに持つ。
"""

from __future__ import annotations

import copy
import importlib.util
import json
import os
import shlex
import stat
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
CF_DIR = REPO_ROOT / "scripts" / "cloudflare"
ZONE_SCRIPT = CF_DIR / "cf_zone.py"
TURNSTILE_SCRIPT = CF_DIR / "cf_turnstile.py"
CONFIG = CF_DIR / "zone_rules.json"

ZONE = "kizuku-hackathon.work"
HOSTS = ["raft.kizuku-hackathon.work", "raft-demo.kizuku-hackathon.work"]
FAKE_SECRET = "0x4AAAAAAA-fake-secret-for-test"
FAKE_SECRET_GET = "0x4AAAAAAA-fake-secret-from-get"


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[name] = mod  # dataclass が sys.modules からモジュールを引く
    spec.loader.exec_module(mod)
    return mod


cf_zone = load_module("cf_zone_under_test", ZONE_SCRIPT)
cf_turnstile = load_module("cf_turnstile_under_test", TURNSTILE_SCRIPT)


FAKE_CF = textwrap.dedent(
    r'''
    import json, os, sys

    KNOWN = [
        ["zones", "list"],
        ["rulesets", "list"],
        ["rulesets", "get"],
        ["rulesets", "rules", "create"],
        ["rulesets", "rules", "update"],
        ["rulesets", "rules", "delete"],
        ["rulesets", "entrypoint", "update"],
        ["turnstile", "widgets", "list"],
        ["turnstile", "widgets", "get"],
        ["turnstile", "widgets", "create"],
        ["turnstile", "widgets", "update"],
    ]
    VALUE_OPTS = {"--zone", "-z", "--body", "--ruleset-id", "--filter", "--name"}
    BOOL_OPTS = {"--dry-run", "--validate-only", "--force"}

    argv = sys.argv[1:]
    with open(os.environ["FAKE_CF_LOG"], "a", encoding="utf-8") as f:
        f.write(json.dumps({
            "args": argv,
            "telemetry": os.environ.get("CF_SEND_TELEMETRY"),
            "cwd": os.getcwd(),
        }) + "\n")

    def die(msg, code=1):
        sys.stderr.write("fake-cf: " + msg + "\n")
        sys.exit(code)

    key = None
    for k in KNOWN:
        if argv[: len(k)] == k:
            key = k
            break
    if key is None:
        die("unknown command: " + " ".join(argv))
    rest = argv[len(key):]
    pos, opts, flags = [], {}, set()
    i = 0
    while i < len(rest):
        a = rest[i]
        if a in VALUE_OPTS:
            opts[a.lstrip("-")] = rest[i + 1]
            i += 2
        elif a in BOOL_OPTS:
            flags.add(a.lstrip("-"))
            i += 1
        elif a.startswith("-"):
            die("unknown option: " + a)
        else:
            pos.append(a)
            i += 1
    name = " ".join(key)

    for entry in json.loads(os.environ.get("FAKE_CF_FAIL", "[]")):
        # "<コマンド> [--flag] [!--flag]": --flag はあるとき、!--flag はないときだけ失敗させる。
        words = entry.split()
        need = [w.lstrip("-") for w in words if w.startswith("--")]
        absent = [w[1:].lstrip("-") for w in words if w.startswith("!--")]
        cmd = " ".join(w for w in words if not w.startswith(("--", "!--")))
        if cmd == name and all(n in flags for n in need) and not any(a in flags for a in absent):
            die("forced failure: " + entry + " (Authentication error / not allowed on this plan)")

    body = json.loads(opts["body"]) if "body" in opts else None
    if "dry-run" in flags:
        print(json.dumps({"fake": True, "command": "cf " + name, "positional": pos,
                          "zone": opts.get("zone"), "body": body}, indent=2))
        sys.exit(0)

    state_path = os.environ["FAKE_CF_STATE"]
    with open(state_path, encoding="utf-8") as f:
        state = json.load(f)

    def save():
        with open(state_path, "w", encoding="utf-8") as f:
            json.dump(state, f)

    def new_id(prefix):
        state["next_id"] = state.get("next_id", 1) + 1
        return "%s-new-%d" % (prefix, state["next_id"])

    def out(obj):
        print(json.dumps(obj))

    def zone_ok():
        z = opts.get("zone")
        if z not in [x["id"] for x in state.get("zones", [])]:
            die("zone not found: %r" % z)

    def ruleset(rid):
        for rs in state.get("rulesets", []):
            if rs["id"] == rid:
                return rs
        die("ruleset not found: " + str(rid))

    validate = "validate-only" in flags

    if name == "zones list":
        out([z for z in state.get("zones", []) if z["name"] == opts.get("name")])
    elif name == "rulesets list":
        zone_ok()
        out([{k: v for k, v in rs.items() if k != "rules"} for rs in state.get("rulesets", [])])
    elif name == "rulesets get":
        zone_ok()
        out(ruleset(pos[0]))
    elif name == "rulesets rules create":
        zone_ok()
        rs = ruleset(pos[0])
        if validate:
            out(None)
        else:
            rule = dict(body)
            rule["id"] = new_id("rule")
            rule["version"] = "1"
            rs["rules"].append(rule)
            save()
            out(rs)
    elif name == "rulesets rules update":
        zone_ok()
        rs = ruleset(opts["ruleset-id"])
        ids = [r["id"] for r in rs["rules"]]
        if pos[0] not in ids:
            die("rule not found")
        idx = ids.index(pos[0])
        if validate:
            out(None)
        else:
            rule = dict(body)
            rule["id"] = pos[0]
            rule["version"] = "2"
            rs["rules"][idx] = rule
            save()
            out(rs)
    elif name == "rulesets rules delete":
        zone_ok()
        rs = ruleset(opts["ruleset-id"])
        if pos[0] not in [r["id"] for r in rs["rules"]]:
            die("rule not found")
        if "force" not in flags:
            die("needs --force in non-interactive mode")
        if validate:
            out(None)
        else:
            rs["rules"] = [r for r in rs["rules"] if r["id"] != pos[0]]
            save()
            out(rs)
    elif name == "rulesets entrypoint update":
        zone_ok()
        phase = pos[0]
        if validate:
            out(None)
        else:
            rules = []
            for r in body["rules"]:
                r = dict(r)
                r["id"] = new_id("rule")
                rules.append(r)
            for rs in state.setdefault("rulesets", []):
                if rs["kind"] == "zone" and rs["phase"] == phase:
                    rs["rules"] = rules
                    break
            else:
                rs = {"id": new_id("rs"), "kind": "zone", "phase": phase, "rules": rules}
                state["rulesets"].append(rs)
            save()
            out(rs)
    elif name.startswith("turnstile"):
        if not os.environ.get("CLOUDFLARE_ACCOUNT_ID"):
            die("no account selected")
        widgets = state.setdefault("widgets", [])
        def public(w):
            return {k: v for k, v in w.items() if k != "secret"}
        def find(sk):
            for w in widgets:
                if w["sitekey"] == sk:
                    return w
            die("widget not found: " + sk)
        if name == "turnstile widgets list":
            f = opts.get("filter", "")
            assert f.startswith("name:"), f
            needle = f[len("name:"):].lower()
            out([public(w) for w in widgets if needle in w["name"].lower()])
        elif name == "turnstile widgets get":
            w = dict(find(pos[0]))
            w["secret"] = state.get("get_secret", w.get("secret"))
            out(w)
        elif name == "turnstile widgets create":
            w = dict(body)
            w["sitekey"] = new_id("0x4AAAAAAA-fake-sitekey")
            w["secret"] = state.get("create_secret", "0x4AAAAAAA-fake-secret-for-test")
            widgets.append(w)
            save()
            out(w)
        elif name == "turnstile widgets update":
            w = find(pos[0])
            w.update(body)
            save()
            out(public(w))
    else:
        die("not implemented: " + name)
    '''
)

WRITE_CMDS = (
    ("rulesets", "rules", "create"),
    ("rulesets", "rules", "update"),
    ("rulesets", "rules", "delete"),
    ("rulesets", "entrypoint", "update"),
    ("turnstile", "widgets", "create"),
    ("turnstile", "widgets", "update"),
)
FORBIDDEN = (
    ("bot-management",),
    ("zones", "settings"),
    ("zones", "edit"),
    ("rulesets", "delete"),
)


def desired_rule(phase: str) -> dict:
    cfg = json.loads(CONFIG.read_text(encoding="utf-8"))
    return copy.deepcopy(cfg["phases"][phase][0])


def other_rules() -> list:
    return [
        {"id": "other-1", "ref": "blog-block-bad", "expression": "true", "action": "block", "enabled": True},
        {"id": "other-2", "expression": "(http.host eq \"blog.kizuku-hackathon.work\")", "action": "managed_challenge", "enabled": True},
        {"id": "other-3", "ref": "raft-game-emergency-block", "expression": "true", "action": "block", "enabled": True},
    ]


def zone_entry(zone_id: str = "zone-123") -> dict:
    return {"id": zone_id, "name": ZONE, "plan": {"legacy_id": "free", "name": "Free Website"}}


def mixed_state() -> dict:
    """Z-6 の状態: ほかのルール 3 つ＋古い値のレート制限＋raft-game-zone-old。"""
    old_rl = desired_rule("http_ratelimit")
    old_rl["ratelimit"]["requests_per_period"] = 20
    old_rl["ratelimit"]["requests_to_origin"] = False  # 応答の余分なキー
    old_rl.update({"id": "rl-ours", "version": "3", "last_updated": "2026-11-01T00:00:00Z"})
    old = {
        "id": "rule-old",
        "ref": "raft-game-zone-old",
        "description": "old",
        "expression": cf_zone.HOST_GUARD + " and (true)",
        "action": "block",
        "enabled": True,
    }
    rules_custom = other_rules() + [old]
    return {
        "zones": [zone_entry()],
        "rulesets": [
            {"id": "rs-managed", "kind": "managed", "phase": "http_request_firewall_managed", "rules": []},
            {"id": "rs-custom", "kind": "zone", "phase": "http_request_firewall_custom", "rules": rules_custom},
            {"id": "rs-rl", "kind": "zone", "phase": "http_ratelimit", "rules": [old_rl]},
        ],
    }


class FakeCfCase(unittest.TestCase):
    """偽の cf を用意し、すべての呼び出しで禁止のコマンドがないことを確かめる。"""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.tmp = Path(self._tmp.name)
        self.fake = self.tmp / "fake_cf.py"
        self.fake.write_text(FAKE_CF, encoding="utf-8")
        self.log = self.tmp / "calls.jsonl"
        self.state_path = self.tmp / "state.json"
        self.set_state({"zones": [zone_entry()], "rulesets": []})

    def tearDown(self):
        try:
            calls = self.calls()
            for c in calls:
                for bad in FORBIDDEN:
                    self.assertNotEqual(tuple(c[: len(bad)]), bad, "禁止のコマンドを呼んだ: %r" % c)
        finally:
            self._tmp.cleanup()

    def set_state(self, state: dict):
        self.state_path.write_text(json.dumps(state), encoding="utf-8")

    def state(self) -> dict:
        return json.loads(self.state_path.read_text(encoding="utf-8"))

    def raw_calls(self) -> list:
        if not self.log.exists():
            return []
        return [json.loads(line) for line in self.log.read_text(encoding="utf-8").splitlines() if line]

    def calls(self) -> list:
        return [c["args"] for c in self.raw_calls()]

    def writes(self) -> list:
        return [c for c in self.calls() if any(tuple(c[: len(w)]) == w for w in WRITE_CMDS)]

    def real_writes(self) -> list:
        return [c for c in self.writes() if "--dry-run" not in c and "--validate-only" not in c]

    def run_script(self, script: Path, *args, fail=(), env_extra=None, cf=None, drop_account=False):
        env = {k: v for k, v in os.environ.items() if not k.startswith(("FAKE_CF", "CLOUDFLARE_"))}
        env.pop("CF", None)
        env.update(
            {
                "CF": cf if cf is not None else "%s %s" % (shlex.quote(sys.executable), shlex.quote(str(self.fake))),
                "FAKE_CF_LOG": str(self.log),
                "FAKE_CF_STATE": str(self.state_path),
                "FAKE_CF_FAIL": json.dumps(list(fail)),
            }
        )
        if not drop_account:
            env["CLOUDFLARE_ACCOUNT_ID"] = "fake-account-id"
        if env_extra:
            env.update(env_extra)
        return subprocess.run(
            [sys.executable, str(script), *args],
            cwd=str(self.tmp),
            env=env,
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            encoding="utf-8",
            timeout=60,
        )


# ---------------------------------------------------------------------------
# cf_zone.py
# ---------------------------------------------------------------------------


def base_cfg() -> dict:
    return json.loads(CONFIG.read_text(encoding="utf-8"))


class ValidateConfigTest(unittest.TestCase):
    def assertViolates(self, cfg, needle=None):
        errors = cf_zone.validate_config(cfg)
        self.assertTrue(errors, "違反にならなかった")
        if needle:
            self.assertTrue(any(needle in e for e in errors), errors)

    def custom(self, cfg):
        return cfg["phases"]["http_request_firewall_custom"][0]

    def rl(self, cfg):
        return cfg["phases"]["http_ratelimit"][0]

    def test_z1_repo_config_passes(self):
        self.assertEqual(cf_zone.validate_config(base_cfg()), [])
        cfg = base_cfg()
        self.assertEqual(cfg["zone"], cf_zone.ZONE)
        self.assertEqual(tuple(cfg["hostnames"]), cf_zone.HOSTNAMES)
        self.assertEqual(cf_zone.ZONE, cf_turnstile.ZONE)
        self.assertEqual(cf_zone.HOSTNAMES, cf_turnstile.HOSTNAMES)
        for phase, rules in cfg["phases"].items():
            for r in rules:
                self.assertTrue(r["expression"].startswith(cf_zone.HOST_GUARD))

    def test_z2_zone_and_hostnames(self):
        cfg = base_cfg()
        cfg["zone"] = "example.com"
        self.assertViolates(cfg, "zone")
        for hosts in (
            ["*.kizuku-hackathon.work", HOSTS[0]],
            HOSTS + ["other.kizuku-hackathon.work"],
            [HOSTS[0]],
            [HOSTS[0], HOSTS[0]],
        ):
            cfg = base_cfg()
            cfg["hostnames"] = hosts
            self.assertViolates(cfg, "hostnames")
        cfg = base_cfg()
        cfg["hostnames"] = list(reversed(HOSTS))
        self.assertEqual(cf_zone.validate_config(cfg), [])

    def test_z2_expression(self):
        cfg = base_cfg()
        self.custom(cfg)["expression"] = '(cf.verified_bot_category in {"AI Crawler"})'
        self.assertViolates(cfg, "expression")
        cfg = base_cfg()
        self.custom(cfg)["expression"] = cf_zone.HOST_GUARD + " and (true) or (true)"
        self.assertViolates(cfg, "expression")
        cfg = base_cfg()
        self.custom(cfg)["expression"] = cf_zone.HOST_GUARD + " or (true)"
        self.assertViolates(cfg, "expression")
        cfg = base_cfg()
        self.custom(cfg)["expression"] = cf_zone.HOST_GUARD.replace("raft-demo", "*") + " and (true)"
        self.assertViolates(cfg, "expression")
        # 文字列リテラルの中の ) は数えない
        cfg = base_cfg()
        self.custom(cfg)["expression"] = cf_zone.HOST_GUARD + ' and (http.request.uri.path eq ")")'
        self.assertEqual(cf_zone.validate_config(cfg), [])
        cfg = base_cfg()
        self.custom(cfg)["expression"] = cf_zone.HOST_GUARD + ' and (http.request.uri.path eq "\\")(")'
        self.assertEqual(cf_zone.validate_config(cfg), [])

    def test_z2_refs(self):
        cfg = base_cfg()
        self.custom(cfg)["ref"] = "raft-game-emergency-block"
        self.assertViolates(cfg, "ref")
        cfg = base_cfg()
        self.custom(cfg)["ref"] = "raft-game-zone-UPPER"
        self.assertViolates(cfg, "ref")
        cfg = base_cfg()
        self.rl(cfg)["ref"] = self.custom(cfg)["ref"]
        self.assertViolates(cfg, "重複")

    def test_z2_actions(self):
        for action in ("allow", "skip"):
            cfg = base_cfg()
            self.custom(cfg)["action"] = action
            self.assertViolates(cfg, "action")
            cfg = base_cfg()
            self.rl(cfg)["action"] = action
            self.assertViolates(cfg, "action")
        cfg = base_cfg()
        self.custom(cfg)["action"] = "managed_challenge"
        self.assertEqual(cf_zone.validate_config(cfg), [])

    def test_z2_ratelimit(self):
        cfg = base_cfg()
        second = copy.deepcopy(self.rl(cfg))
        second["ref"] = "raft-game-zone-ratelimit-two"
        cfg["phases"]["http_ratelimit"].append(second)
        self.assertViolates(cfg, "1 つまで")
        cfg = base_cfg()
        self.rl(cfg)["ratelimit"]["period"] = 60
        self.assertViolates(cfg, "period")
        cfg = base_cfg()
        self.rl(cfg)["ratelimit"]["characteristics"] = ["ip.src"]
        self.assertViolates(cfg, "characteristics")
        cfg = base_cfg()
        self.rl(cfg)["ratelimit"]["requests_per_period"] = 0
        self.assertViolates(cfg, "requests_per_period")
        cfg = base_cfg()
        self.custom(cfg)["ratelimit"] = copy.deepcopy(self.rl(cfg)["ratelimit"])
        self.assertViolates(cfg, "知らないキー")

    def test_z2_enabled_and_unknown_keys(self):
        cfg = base_cfg()
        self.custom(cfg)["enabled"] = False
        self.assertViolates(cfg, "enabled")
        cfg = base_cfg()
        self.custom(cfg)["position"] = 1
        self.assertViolates(cfg, "知らないキー")
        cfg = base_cfg()
        cfg["extra"] = True
        self.assertViolates(cfg, "最上位")
        cfg = base_cfg()
        cfg["phases"]["http_request_firewall_managed"] = []
        self.assertViolates(cfg, "フェーズ")
        cfg = base_cfg()
        self.custom(cfg)["description"] = "日本語"
        self.assertViolates(cfg, "description")


class PlanPhaseTest(unittest.TestCase):
    def test_z3_no_ruleset(self):
        desired = [desired_rule("http_ratelimit")]
        plan = cf_zone.plan_phase(desired, None)
        self.assertTrue(plan.entrypoint_missing)
        self.assertEqual([r["ref"] for r in plan.create], ["raft-game-zone-ratelimit-api"])
        self.assertEqual((plan.update, plan.delete, plan.others), ([], [], 0))

    def test_z3_same_ignores_extra_keys(self):
        want = desired_rule("http_ratelimit")
        cur = copy.deepcopy(want)
        cur.update({"id": "r1", "version": "4", "last_updated": "2026-11-01T00:00:00Z"})
        cur["ratelimit"]["requests_to_origin"] = False
        plan = cf_zone.plan_phase([want], [cur])
        self.assertEqual(plan.unchanged, [want["ref"]])
        self.assertEqual(plan.changes(), 0)
        self.assertFalse(plan.entrypoint_missing)

    def test_z3_update_delete_others(self):
        want = desired_rule("http_ratelimit")
        cur = copy.deepcopy(want)
        cur["id"] = "r1"
        cur["ratelimit"]["requests_per_period"] = 20
        old = {"id": "r-old", "ref": "raft-game-zone-old", "expression": "x", "action": "block"}
        plan = cf_zone.plan_phase([want], other_rules() + [cur, old])
        self.assertEqual(len(plan.update), 1)
        self.assertEqual(plan.update[0].rule_id, "r1")
        self.assertIn("ratelimit.requests_per_period", plan.update[0].diffs)
        self.assertEqual([r["id"] for r in plan.delete], ["r-old"])
        self.assertEqual(plan.others, 3)
        self.assertEqual(plan.create, [])
        touched = {u.rule_id for u in plan.update} | {r["id"] for r in plan.delete}
        self.assertFalse(touched & {"other-1", "other-2", "other-3"})


class ZoneScriptTest(FakeCfCase):
    def test_z4_dry_run_default(self):
        for args in ((), ("--dry-run",)):
            if self.log.exists():
                self.log.unlink()
            r = self.run_script(ZONE_SCRIPT, *args, drop_account=True)
            self.assertEqual(r.returncode, 0, r.stderr)
            calls = self.calls()
            self.assertTrue(calls)
            refs_seen = set()
            for c in calls:
                self.assertIn("--dry-run", c)
                self.assertIn("--zone", c)
                self.assertEqual(c[c.index("--zone") + 1], ZONE)
                self.assertNotIn(c[:2], (["zones", "list"], ["rulesets", "list"], ["rulesets", "get"]))
                body = json.loads(c[c.index("--body") + 1])
                for rule in body.get("rules", [body]):
                    refs_seen.add(rule["ref"])
            self.assertEqual(refs_seen, {"raft-game-zone-ai-crawlers", "raft-game-zone-ratelimit-api"})
            self.assertIn('"fake": true', r.stdout)
            self.assertIn("RULESET_ID", r.stdout)
            self.assertIn("cf_zone: --dry-run: 何も送っていない", r.stdout)
            for rc in self.raw_calls():
                self.assertEqual(rc["telemetry"], "false")
                self.assertEqual(Path(rc["cwd"]).resolve(), REPO_ROOT.resolve())
            self.assertEqual(self.real_writes(), [])

    def test_z5_check_without_rulesets(self):
        r = self.run_script(ZONE_SCRIPT, "--check")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout.count("作る: "), 2, r.stdout)
        self.assertIn("変更あり（2 件）", r.stdout)
        self.assertIn("プラン free", r.stdout)
        writes = self.writes()
        self.assertTrue(writes)
        for c in writes:
            self.assertIn("--validate-only", c)
            self.assertEqual(c[:3], ["rulesets", "entrypoint", "update"])
        self.assertEqual(self.state()["rulesets"], [])

    def test_z6_check_mixed(self):
        self.set_state(mixed_state())
        before = self.state()
        r = self.run_script(ZONE_SCRIPT, "--check")
        self.assertEqual(r.returncode, 0, r.stderr)
        out = r.stdout
        self.assertEqual(out.count("直す: "), 1, out)
        self.assertIn("直す: raft-game-zone-ratelimit-api（ratelimit.requests_per_period）", out)
        self.assertEqual(out.count("作る: "), 1, out)
        self.assertIn("作る: raft-game-zone-ai-crawlers", out)
        self.assertEqual(out.count("消す: "), 1, out)
        self.assertIn("消す: raft-game-zone-old", out)
        self.assertIn("ほかのルール: 3 個（触らない）", out)
        self.assertIn("変更あり（3 件）", out)
        self.assertEqual(self.state(), before)
        self.assertEqual(self.real_writes(), [])
        for c in self.writes():
            self.assertIn("--validate-only", c)

    def test_z7_apply_mixed(self):
        self.set_state(mixed_state())
        r = self.run_script(ZONE_SCRIPT, "--apply", "--yes")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        writes = self.real_writes()
        names = [tuple(c[:3]) for c in writes]
        self.assertIn(("rulesets", "rules", "update"), names)
        self.assertIn(("rulesets", "rules", "create"), names)
        self.assertIn(("rulesets", "rules", "delete"), names)
        for c in writes:
            if c[:3] in (["rulesets", "rules", "update"], ["rulesets", "rules", "delete"]):
                self.assertIn("--ruleset-id", c)
            if c[:3] == ["rulesets", "rules", "delete"]:
                self.assertIn("--force", c)
                self.assertEqual(c[3], "rule-old")
            self.assertNotEqual(c[:3], ["rulesets", "entrypoint", "update"])
        for c in self.calls():
            for other in ("other-1", "other-2", "other-3"):
                self.assertNotIn(other, c)
        st = self.state()
        custom = next(rs for rs in st["rulesets"] if rs["id"] == "rs-custom")
        ids = [x["id"] for x in custom["rules"]]
        for other in ("other-1", "other-2", "other-3"):
            self.assertIn(other, ids)
        self.assertEqual(custom["rules"][:3], other_rules())
        self.assertNotIn("rule-old", ids)
        self.assertIn("人が確かめること", r.stdout)

        r2 = self.run_script(ZONE_SCRIPT, "--check")
        self.assertEqual(r2.returncode, 0, r2.stderr)
        self.assertIn("cf_zone: 変更なし", r2.stdout)

    def test_z7_apply_creates_entrypoints(self):
        r = self.run_script(ZONE_SCRIPT, "--apply", "--yes")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        names = [tuple(c[:3]) for c in self.real_writes()]
        self.assertEqual(names, [("rulesets", "entrypoint", "update")] * 2)
        r2 = self.run_script(ZONE_SCRIPT, "--check")
        self.assertIn("cf_zone: 変更なし", r2.stdout)

    def test_z8_apply_without_yes_non_tty(self):
        self.set_state(mixed_state())
        before = self.state()
        r = self.run_script(ZONE_SCRIPT, "--apply")
        self.assertEqual(r.returncode, 1)
        self.assertEqual(self.real_writes(), [])
        self.assertEqual(self.state(), before)

    def test_z8_remove_without_yes_non_tty(self):
        self.set_state(mixed_state())
        r = self.run_script(ZONE_SCRIPT, "--remove")
        self.assertEqual(r.returncode, 1)
        self.assertEqual(self.real_writes(), [])

    def test_z9_remove(self):
        self.set_state(mixed_state())
        r = self.run_script(ZONE_SCRIPT, "--remove", "--yes")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        st = self.state()
        all_rules = [x for rs in st["rulesets"] for x in rs["rules"]]
        self.assertFalse([x for x in all_rules if x.get("ref", "").startswith("raft-game-zone-")])
        ids = [x["id"] for x in all_rules]
        for other in ("other-1", "other-2", "other-3"):
            self.assertIn(other, ids)
        self.assertEqual({rs["id"] for rs in st["rulesets"]}, {"rs-managed", "rs-custom", "rs-rl"})
        for c in self.real_writes():
            self.assertEqual(c[:3], ["rulesets", "rules", "delete"])
            self.assertIn("--force", c)
        r2 = self.run_script(ZONE_SCRIPT, "--remove", "--yes")
        self.assertEqual(r2.returncode, 0)
        self.assertIn("消すものはない", r2.stdout)

    def test_z10_zone_count(self):
        for zones, needle in (([], "ゾーンが見つからない"), ([zone_entry("a"), zone_entry("b")], "複数")):
            self.set_state({"zones": zones, "rulesets": []})
            for mode in ("--check", "--apply"):
                r = self.run_script(ZONE_SCRIPT, mode, "--yes")
                self.assertEqual(r.returncode, 1)
                self.assertIn(needle, r.stderr)
        self.assertEqual(self.writes(), [])

    def test_z10_validate_only_failure(self):
        self.set_state(mixed_state())
        for mode in ("--check", "--apply"):
            r = self.run_script(ZONE_SCRIPT, mode, "--yes", fail=["rulesets rules create --validate-only"])
            self.assertEqual(r.returncode, 1)
            self.assertIn("not allowed on this plan", r.stderr)
        self.assertEqual(self.real_writes(), [])

    def test_z10_create_failure(self):
        self.set_state(mixed_state())
        r = self.run_script(ZONE_SCRIPT, "--apply", "--yes", fail=["rulesets rules create !--validate-only"])
        self.assertEqual(r.returncode, 1)
        self.assertIn("反映の途中で止めた", r.stderr)
        self.assertIn("--check", r.stderr)
        # create（フェーズの最初の書き込み）で止まり、その後の書き込みをしない。
        self.assertEqual([c[:3] for c in self.real_writes()], [["rulesets", "rules", "create"]])
        # validate-only だけを失敗させても書き込まない（上の test_z10_validate_only_failure）。
        r = self.run_script(ZONE_SCRIPT, "--check", fail=["rulesets rules create"])
        self.assertEqual(r.returncode, 1)

    def test_z10_auth_failure(self):
        r = self.run_script(ZONE_SCRIPT, "--check", fail=["zones list"])
        self.assertEqual(r.returncode, 1)
        self.assertIn("Authentication error", r.stderr)

    def test_z11_local_violation_calls_nothing(self):
        # 手元の検査を通らない設定では cf を呼ばない（validate_config を直接確かめ、main も確かめる）。
        cfg = base_cfg()
        cfg["phases"]["http_request_firewall_custom"][0]["expression"] = "(true)"
        self.assertTrue(cf_zone.validate_config(cfg))
        orig = cf_zone.load_config
        cf_zone.load_config = lambda _p: cfg
        try:
            env_cf = os.environ.get("CF")
            os.environ["CF"] = "/nonexistent/cf-should-not-run"
            try:
                import io
                from contextlib import redirect_stderr

                buf = io.StringIO()
                with redirect_stderr(buf):
                    for mode in ([], ["--check"], ["--apply", "--yes"], ["--remove", "--yes"]):
                        self.assertEqual(cf_zone.main(mode), 1)
                self.assertIn("ホスト名の条件", buf.getvalue())
            finally:
                if env_cf is None:
                    os.environ.pop("CF", None)
                else:
                    os.environ["CF"] = env_cf
        finally:
            cf_zone.load_config = orig
        self.assertEqual(self.calls(), [])

    def test_z11_no_zone_wide_commands(self):
        # 各モードを通して、禁止のコマンドを呼ばないこと（tearDown でも確かめる）。
        self.set_state(mixed_state())
        for args in ((), ("--check",), ("--apply", "--yes"), ("--remove", "--yes")):
            self.run_script(ZONE_SCRIPT, *args)
        for c in self.calls():
            for bad in FORBIDDEN:
                self.assertNotEqual(tuple(c[: len(bad)]), bad)

    def test_z12_usage_and_missing_cf(self):
        r = self.run_script(ZONE_SCRIPT, "--dry-run", "--check")
        self.assertEqual(r.returncode, 2)
        r = self.run_script(ZONE_SCRIPT, "--check", "--remove")
        self.assertEqual(r.returncode, 2)
        missing = str(self.tmp / "no-such-cf")
        r = self.run_script(ZONE_SCRIPT, cf=missing)
        self.assertEqual(r.returncode, 1)
        self.assertIn("cf_zone: ", r.stderr)
        self.assertEqual(self.calls(), [])


# ---------------------------------------------------------------------------
# cf_turnstile.py
# ---------------------------------------------------------------------------


def widget(name="raft-game", domains=None, sitekey="0x4AAAAAAA-fake-sitekey-1", mode="managed"):
    return {
        "name": name,
        "domains": list(HOSTS) if domains is None else domains,
        "mode": mode,
        "sitekey": sitekey,
        "secret": FAKE_SECRET,
        "created_on": "2026-11-01T00:00:00Z",
    }


class TurnstileTest(FakeCfCase):
    def assertNoSecret(self, r):
        for s in (FAKE_SECRET, FAKE_SECRET_GET):
            self.assertNotIn(s, r.stdout)
            self.assertNotIn(s, r.stderr)

    def test_ts1_dry_run(self):
        r = self.run_script(TURNSTILE_SCRIPT, drop_account=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        calls = self.calls()
        self.assertEqual(len(calls), 1)
        c = calls[0]
        self.assertEqual(c[:3], ["turnstile", "widgets", "create"])
        self.assertIn("--dry-run", c)
        self.assertEqual(
            json.loads(c[c.index("--body") + 1]),
            {"name": "raft-game", "domains": HOSTS, "mode": "managed"},
        )
        self.assertIn('"fake": true', r.stdout)

    def test_ts2_check_without_account(self):
        r = self.run_script(TURNSTILE_SCRIPT, "--check", drop_account=True)
        self.assertEqual(r.returncode, 1)
        self.assertIn("cf_turnstile: CLOUDFLARE_ACCOUNT_ID を設定する", r.stderr)
        self.assertEqual(self.calls(), [])

    def test_ts3_check_states(self):
        self.set_state({"widgets": []})
        r = self.run_script(TURNSTILE_SCRIPT, "--check")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("作る", r.stdout)

        self.set_state({"widgets": [widget(), widget(name="raft-game-old", sitekey="0x4AAAAAAA-fake-old")]})
        r = self.run_script(TURNSTILE_SCRIPT, "--check")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("変更なし", r.stdout)
        self.assertIn("0x4AAAAAAA-fake-sitekey-1", r.stdout)
        self.assertNotIn("0x4AAAAAAA-fake-old", r.stdout)
        self.assertNoSecret(r)

        self.set_state({"widgets": [widget(domains=[HOSTS[0]])]})
        r = self.run_script(TURNSTILE_SCRIPT, "--check")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("直す", r.stdout)
        self.assertIn("domains", r.stdout)
        self.assertIn("0x4AAAAAAA-fake-sitekey-1", r.stdout)

        self.set_state({"widgets": [widget(), widget(sitekey="0x4AAAAAAA-fake-sitekey-2")]})
        r = self.run_script(TURNSTILE_SCRIPT, "--check")
        self.assertEqual(r.returncode, 1)
        self.assertIn("同じ名前のウィジェットが複数ある", r.stderr)
        self.assertEqual(self.real_writes(), [])

    def test_ts3_plan_widget(self):
        p = cf_turnstile.plan_widget([widget(domains=list(reversed(HOSTS)))])
        self.assertEqual(p.action, "unchanged")
        p = cf_turnstile.plan_widget([widget(mode="invisible")])
        self.assertEqual(p.action, "update")
        self.assertTrue(any("mode" in d for d in p.diffs))
        p = cf_turnstile.plan_widget([widget(name="raft-game-old")])
        self.assertEqual(p.action, "create")

    def test_ts4_apply_creates(self):
        self.set_state({"widgets": [widget(name="raft-game-old", sitekey="0x4AAAAAAA-fake-old")]})
        (self.tmp / "new").mkdir()
        out = self.tmp / "new" / "secret"
        r = self.run_script(TURNSTILE_SCRIPT, "--apply", "--yes", "--secret-out", str(out))
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(stat.S_IMODE(out.stat().st_mode), 0o600)
        self.assertEqual(out.read_bytes(), FAKE_SECRET.encode())
        self.assertNoSecret(r)
        created = [w for w in self.state()["widgets"] if w["name"] == "raft-game"]
        self.assertEqual(len(created), 1)
        self.assertIn("cf_turnstile: サイトキー: " + created[0]["sitekey"], r.stdout)
        self.assertIn("gh secret set TURNSTILE_SECRET", r.stdout)
        names = [tuple(c[:3]) for c in self.real_writes()]
        self.assertEqual(names, [("turnstile", "widgets", "create")])

    def test_ts4_apply_unchanged_rewrites_secret(self):
        self.set_state({"widgets": [widget()], "get_secret": FAKE_SECRET_GET})
        out = self.tmp / "secret"
        r = self.run_script(TURNSTILE_SCRIPT, "--apply", "--yes", "--secret-out", str(out))
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(out.read_text(), FAKE_SECRET_GET)
        self.assertEqual(self.real_writes(), [])
        self.assertNoSecret(r)

    def test_ts5_secret_out_errors(self):
        self.set_state({"widgets": []})
        r = self.run_script(TURNSTILE_SCRIPT, "--apply", "--yes")
        self.assertEqual(r.returncode, 2)

        existing = self.tmp / "exists"
        existing.write_text("keep")
        r = self.run_script(TURNSTILE_SCRIPT, "--apply", "--yes", "--secret-out", str(existing))
        self.assertEqual(r.returncode, 1)
        self.assertEqual(existing.read_text(), "keep")

        inside = REPO_ROOT / "tmp-secret"
        self.assertFalse(inside.exists())
        r = self.run_script(TURNSTILE_SCRIPT, "--apply", "--yes", "--secret-out", str(inside))
        self.assertEqual(r.returncode, 1)
        self.assertIn("リポジトリの中", r.stderr)
        self.assertFalse(inside.exists())

        r = self.run_script(
            TURNSTILE_SCRIPT, "--apply", "--yes", "--secret-out", str(self.tmp / "no-parent" / "secret")
        )
        self.assertEqual(r.returncode, 1)
        self.assertIn("親のディレクトリ", r.stderr)

        self.assertEqual(self.writes(), [])
        self.assertEqual(self.state()["widgets"], [])

    def test_ts5_apply_without_yes_non_tty(self):
        self.set_state({"widgets": []})
        out = self.tmp / "secret"
        r = self.run_script(TURNSTILE_SCRIPT, "--apply", "--secret-out", str(out))
        self.assertEqual(r.returncode, 1)
        self.assertEqual(self.real_writes(), [])
        self.assertFalse(out.exists())

    def test_ts6_apply_updates(self):
        self.set_state({"widgets": [widget(domains=[HOSTS[0]])], "get_secret": FAKE_SECRET_GET})
        out = self.tmp / "secret"
        r = self.run_script(TURNSTILE_SCRIPT, "--apply", "--yes", "--secret-out", str(out))
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        writes = self.real_writes()
        self.assertEqual(len(writes), 1)
        self.assertEqual(writes[0][:4], ["turnstile", "widgets", "update", "0x4AAAAAAA-fake-sitekey-1"])
        self.assertIn(["turnstile", "widgets", "get", "0x4AAAAAAA-fake-sitekey-1"], self.calls())
        self.assertEqual(out.read_text(), FAKE_SECRET_GET)
        self.assertEqual(sorted(self.state()["widgets"][0]["domains"]), sorted(HOSTS))
        self.assertNoSecret(r)

    def test_ts_cf_failure_prints_stderr_only(self):
        self.set_state({"widgets": []})
        out = self.tmp / "secret"
        r = self.run_script(TURNSTILE_SCRIPT, "--apply", "--yes", "--secret-out", str(out), fail=["turnstile widgets create"])
        self.assertEqual(r.returncode, 1)
        self.assertIn("Authentication error", r.stderr)
        self.assertFalse(out.exists())


if __name__ == "__main__":
    unittest.main()
