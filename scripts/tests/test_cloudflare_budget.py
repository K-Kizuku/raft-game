"""scripts/cloudflare/budget_alert.py のテスト（T-607 / Issue #71 / ADR-0018 G6）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_cloudflare_budget.py'

ネットワーク・本物の cf を使わない。偽の cf（一時ディレクトリに書く Python の
スクリプト）を環境変数 CF で渡す。偽の cf は、呼ばれた引数を JSON の 1 行ずつで
記録し、ポリシーの一覧を JSON のファイルの状態に持つ。
"""

import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path
from typing import Any, Dict, List, Optional

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
SCRIPT = REPO_ROOT / "scripts" / "cloudflare" / "budget_alert.py"

# 子のプロセスに引き継がない環境変数。
DROP_KEYS = ("CF", "BUDGET_ALERT_EMAIL", "CLOUDFLARE_ACCOUNT_ID", "CF_SEND_TELEMETRY")

WRITE_CMDS = ("create", "update", "delete")

FAKE_CF = textwrap.dedent(
    r'''
    import json, os, sys

    state_path = os.environ["FAKE_CF_STATE"]
    log_path = os.environ["FAKE_CF_LOG"]
    fail = set(filter(None, os.environ.get("FAKE_CF_FAIL", "").split(",")))
    args = sys.argv[1:]
    with open(log_path, "a", encoding="utf-8") as f:
        f.write(json.dumps({
            "args": args,
            "telemetry": os.environ.get("CF_SEND_TELEMETRY"),
            "cwd": os.getcwd(),
        }) + "\n")

    if args[:2] != ["billing", "spend-policies"] or len(args) < 3:
        print("fake cf: unknown command " + " ".join(args), file=sys.stderr)
        sys.exit(2)
    cmd = args[2]
    rest = args[3:]
    opts = {}
    pos = []
    flags = set()
    i = 0
    while i < len(rest):
        a = rest[i]
        if a in ("--body", "--if-match"):
            opts[a] = rest[i + 1]
            i += 2
            continue
        if a.startswith("-"):
            flags.add(a)
        else:
            pos.append(a)
        i += 1

    if cmd in fail:
        print("fake cf: " + cmd + " failed: spend policies are not enabled for this account", file=sys.stderr)
        sys.exit(1)

    with open(state_path, encoding="utf-8") as f:
        state = json.load(f)
    body = json.loads(opts["--body"]) if "--body" in opts else None

    if "--dry-run" in flags:
        print(json.dumps({"command": "cf billing spend-policies " + cmd, "method": "POST", "body": body}, indent=2))
        sys.exit(0)

    def save():
        with open(state_path, "w", encoding="utf-8") as f:
            json.dump(state, f)

    def strip(p, key):
        p = dict(p)
        if state.get(key):
            p.pop("version", None)
        return p

    if cmd == "list":
        print(json.dumps([strip(p, "hide_version_in_list") for p in state["policies"]]))
    elif cmd == "get":
        found = [p for p in state["policies"] if p["id"] == pos[0]]
        if not found:
            print("fake cf: not found", file=sys.stderr)
            sys.exit(1)
        print(json.dumps(strip(found[0], "hide_version_in_get")))
    elif cmd == "create":
        state["next_id"] = state.get("next_id", 100) + 1
        p = dict(body)
        p["id"] = "pol-%d" % state["next_id"]
        p["version"] = 1
        state["policies"].append(p)
        save()
        print(json.dumps(p))
    elif cmd == "update":
        found = [p for p in state["policies"] if p["id"] == pos[0]]
        if not found:
            print("fake cf: not found", file=sys.stderr)
            sys.exit(1)
        p = found[0]
        if opts.get("--if-match") != str(p["version"]):
            print("fake cf: version mismatch (If-Match)", file=sys.stderr)
            sys.exit(1)
        for k in ("name", "action", "enabled", "product_id", "threshold_cents", "notification_email"):
            if k not in body:
                print("fake cf: missing field " + k, file=sys.stderr)
                sys.exit(1)
        p.update(body)
        p["version"] += 1
        save()
        print(json.dumps(p))
    elif cmd == "delete":
        state["policies"] = [p for p in state["policies"] if p["id"] != pos[0]]
        save()
    else:
        print("fake cf: unknown " + cmd, file=sys.stderr)
        sys.exit(2)
    '''
)

DEFAULT_ALERT = {
    "id": "pol-default",
    "name": "Default spend alert",
    "action": "notification",
    "enabled": True,
    "product_id": "workers",
    "threshold_cents": 1000,
    "notification_email": "billing@example.com",
    "version": 4,
}


def own(product: str, **kw: Any) -> Dict[str, Any]:
    p = {
        "id": "pol-own-" + product,
        "name": "raft-game-budget-" + product.replace("_", "-"),
        "action": "notification",
        "enabled": True,
        "product_id": product,
        "threshold_cents": 500,
        "notification_email": "owner@example.com",
        "version": 2,
    }
    p.update(kw)
    return p


def load_module():
    spec = importlib.util.spec_from_file_location("budget_alert", SCRIPT)
    mod = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(mod)
    return mod


class BudgetAlertTest(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.tmp = Path(self._tmp.name)
        self.fake = self.tmp / "fake_cf.py"
        self.fake.write_text(FAKE_CF, encoding="utf-8")
        self.state_path = self.tmp / "state.json"
        self.log_path = self.tmp / "log.jsonl"
        self.set_state([])

    def tearDown(self) -> None:
        self._tmp.cleanup()

    # -- 補助 -------------------------------------------------------------

    def set_state(self, policies: List[Dict[str, Any]], **extra: Any) -> None:
        state = {"policies": policies, "next_id": 100}
        state.update(extra)
        self.state_path.write_text(json.dumps(state), encoding="utf-8")

    def state(self) -> List[Dict[str, Any]]:
        return json.loads(self.state_path.read_text(encoding="utf-8"))["policies"]

    def calls(self) -> List[Dict[str, Any]]:
        if not self.log_path.exists():
            return []
        return [json.loads(line) for line in self.log_path.read_text(encoding="utf-8").splitlines() if line]

    def cmds(self) -> List[str]:
        return [c["args"][2] for c in self.calls()]

    def bodies(self) -> List[Dict[str, Any]]:
        out = []
        for c in self.calls():
            a = c["args"]
            if "--body" in a:
                out.append(json.loads(a[a.index("--body") + 1]))
        return out

    def run_script(
        self,
        *args: str,
        account: bool = True,
        fail: str = "",
        env: Optional[Dict[str, str]] = None,
        stdin: Any = subprocess.DEVNULL,
    ) -> subprocess.CompletedProcess:
        e = {k: v for k, v in os.environ.items() if k not in DROP_KEYS}
        e["CF"] = f"{sys.executable} {self.fake}"
        e["FAKE_CF_STATE"] = str(self.state_path)
        e["FAKE_CF_LOG"] = str(self.log_path)
        e["FAKE_CF_FAIL"] = fail
        if account:
            e["CLOUDFLARE_ACCOUNT_ID"] = "0123456789abcdef"
        if env:
            e.update(env)
        return subprocess.run(
            [sys.executable, str(SCRIPT), *args],
            cwd=str(self.tmp),
            env=e,
            stdin=stdin,
            capture_output=True,
            text=True,
        )

    def assert_common_invariants(self) -> None:
        # B-10: 送った本文の action はすべて notification。delete を呼ばない。
        for b in self.bodies():
            self.assertEqual(b["action"], "notification", b)
        self.assertNotIn("delete", self.cmds())
        for c in self.calls():
            self.assertEqual(c["telemetry"], "false")
            self.assertEqual(Path(c["cwd"]).resolve(), REPO_ROOT.resolve())
            self.assertNotIn("--quiet", c["args"])
            self.assertNotIn("-q", c["args"])

    # -- B-1・B-2: --dry-run -----------------------------------------------

    def test_b1_dry_run_default_without_email(self) -> None:
        r = self.run_script(account=False)
        self.assertEqual(r.returncode, 0, r.stderr)
        calls = self.calls()
        self.assertEqual([c["args"][2] for c in calls], ["create", "create"])
        for c in calls:
            self.assertIn("--dry-run", c["args"])
        bodies = self.bodies()
        self.assertEqual([b["product_id"] for b in bodies], ["workers", "durable_objects"])
        self.assertEqual([b["name"] for b in bodies], ["raft-game-budget-workers", "raft-game-budget-durable-objects"])
        for b in bodies:
            self.assertEqual(b["threshold_cents"], 500)
            self.assertEqual(b["action"], "notification")
            self.assertIs(b["enabled"], True)
            self.assertEqual(b["notification_email"], "you@example.com")
        self.assertNotIn("list", self.cmds())
        self.assertIn("you@example.com", r.stdout)
        self.assertIn('"command": "cf billing spend-policies create"', r.stdout)
        self.assertIn("budget_alert: --dry-run: 何も送っていない。今の状態との差は --check で確かめる", r.stdout)
        self.assertEqual(self.state(), [])
        self.assert_common_invariants()

    def test_b1_explicit_dry_run_flag(self) -> None:
        r = self.run_script("--dry-run", account=False)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.cmds(), ["create", "create"])

    def test_b2_dry_run_threshold_and_email(self) -> None:
        r = self.run_script("--dry-run", "--threshold-usd", "3", "--email", "owner@example.com", account=False)
        self.assertEqual(r.returncode, 0, r.stderr)
        bodies = self.bodies()
        self.assertEqual(len(bodies), 2)
        for b in bodies:
            self.assertEqual(b["threshold_cents"], 300)
            self.assertEqual(b["notification_email"], "owner@example.com")
        self.assert_common_invariants()

    def test_dry_run_email_from_env(self) -> None:
        r = self.run_script(account=False, env={"BUDGET_ALERT_EMAIL": "  env@example.com  "})
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual({b["notification_email"] for b in self.bodies()}, {"env@example.com"})

    # -- B-3: 入力の誤り ------------------------------------------------------

    def test_b3_usage_errors(self) -> None:
        cases = [
            ["--threshold-usd", "0"],
            ["--threshold-usd", "31"],
            ["--threshold-usd", "5.5"],
            ["--threshold-usd", "abc"],
            ["--check"],
            ["--apply", "--yes"],
            ["--check", "--email", "Name <a@example.com>"],
            ["--check", "--email", "a b@example.com"],
            ["--check", "--email", "a@example"],
            ["--dry-run", "--email", "Name <a@example.com>"],
            ["--dry-run", "--apply"],
            ["--check", "--apply"],
        ]
        for args in cases:
            with self.subTest(args=args):
                if self.log_path.exists():
                    self.log_path.unlink()
                r = self.run_script(*args)
                self.assertEqual(r.returncode, 2, (args, r.stdout, r.stderr))
                self.assertEqual(self.calls(), [], args)

    def test_b3_too_long_email(self) -> None:
        email = "a" * 250 + "@example.com"
        r = self.run_script("--check", "--email", email)
        self.assertEqual(r.returncode, 2)
        self.assertEqual(self.calls(), [])

    # -- B-4: アカウントがない ------------------------------------------------

    def test_b4_check_without_account(self) -> None:
        for mode in ("--check", "--apply"):
            with self.subTest(mode=mode):
                r = self.run_script(mode, "--email", "owner@example.com", "--yes", account=False)
                self.assertEqual(r.returncode, 1, r.stderr)
                self.assertIn("CLOUDFLARE_ACCOUNT_ID", r.stderr)
                self.assertEqual(self.calls(), [])

    # -- B-5: ポリシーがない --------------------------------------------------

    def test_b5_check_no_policies(self) -> None:
        r = self.run_script("--check", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout.count(": 作る（"), 2, r.stdout)
        self.assertIn("budget_alert: 変更あり（2 件）。--apply で反映する", r.stdout)
        self.assertEqual(self.cmds(), ["list"])
        self.assertEqual(self.state(), [])
        self.assert_common_invariants()

    # -- B-6・B-7: 既定のアラートと、しきい値の違う自分のポリシー ---------------

    def b6_state(self) -> None:
        self.set_state([dict(DEFAULT_ALERT), own("workers", threshold_cents=300, version=7)])

    def test_b6_check_update_and_create(self) -> None:
        self.b6_state()
        before = self.state()
        r = self.run_script("--check", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("workers: 直す（", r.stdout)
        self.assertIn("threshold_cents: 300 → 500", r.stdout)
        self.assertIn("durable_objects: 作る（", r.stdout)
        self.assertIn("変更あり（2 件）", r.stdout)
        # 既定のアラートを表に出す
        self.assertIn("ほかのポリシー（触らない）", r.stdout)
        line = [ln for ln in r.stdout.splitlines() if "Default spend alert" in ln]
        self.assertEqual(len(line), 1, r.stdout)
        self.assertIn("notification", line[0])
        self.assertIn("1000", line[0])
        self.assertEqual(self.cmds(), ["list"])
        self.assertEqual(self.state(), before)
        self.assert_common_invariants()

    def test_b7_apply_yes(self) -> None:
        self.b6_state()
        r = self.run_script("--apply", "--yes", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        calls = self.calls()
        self.assertEqual([c["args"][2] for c in calls], ["list", "update", "create", "list"])
        upd = calls[1]["args"]
        self.assertEqual(upd[3], "pol-own-workers")
        self.assertEqual(upd[upd.index("--if-match") + 1], "7")
        body = json.loads(upd[upd.index("--body") + 1])
        self.assertEqual(
            body,
            {
                "name": "raft-game-budget-workers",
                "action": "notification",
                "enabled": True,
                "product_id": "workers",
                "threshold_cents": 500,
                "notification_email": "owner@example.com",
            },
        )
        created = json.loads(calls[2]["args"][calls[2]["args"].index("--body") + 1])
        self.assertEqual(created["product_id"], "durable_objects")
        # 既定のアラートには触れない
        for c in calls:
            self.assertNotIn("pol-default", c["args"])
        self.assertIn(DEFAULT_ALERT, self.state())
        self.assertIn(
            "budget_alert: 予算アラート（5 USD）: workers・durable_objects。通知は owner@example.com。"
            "届くのは前日の分が翌日（ADR-0018）",
            r.stdout,
        )
        self.assert_common_invariants()
        # 終わった状態で --check が「変更なし」
        r2 = self.run_script("--check", "--email", "owner@example.com")
        self.assertEqual(r2.returncode, 0, r2.stderr)
        self.assertIn("budget_alert: 変更なし", r2.stdout)
        # もう一度 --apply しても書き込まない
        n = len(self.calls())
        r3 = self.run_script("--apply", "--email", "owner@example.com")
        self.assertEqual(r3.returncode, 0, r3.stderr)
        self.assertEqual([c["args"][2] for c in self.calls()[n:]], ["list"])

    # -- B-8: 確認ができない ----------------------------------------------------

    def test_b8_apply_without_yes_not_tty(self) -> None:
        self.b6_state()
        before = self.state()
        r = self.run_script("--apply", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        for w in WRITE_CMDS:
            self.assertNotIn(w, self.cmds())
        self.assertEqual(self.state(), before)

    # -- B-9: usage_block ------------------------------------------------------

    def test_b9_usage_block_warning(self) -> None:
        self.set_state(
            [
                {
                    "id": "pol-block",
                    "name": "someone block",
                    "action": "usage_block",
                    "enabled": True,
                    "product_id": "durable_objects",
                    "threshold_cents": 2000,
                    "version": 1,
                },
                {
                    "id": "pol-block-off",
                    "name": "disabled block",
                    "action": "usage_block",
                    "enabled": False,
                    "product_id": "workers",
                    "threshold_cents": 2000,
                    "version": 1,
                },
                own("workers"),
                own("durable_objects"),
            ]
        )
        r = self.run_script("--check", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(
            "budget_alert: 注意: 利用を止めるポリシーがある（someone block）。"
            "しきい値に達すると、その製品（durable_objects）が請求期間の終わりまで止まる",
            r.stdout,
        )
        self.assertNotIn("（disabled block）", r.stdout)
        self.assertIn("budget_alert: 変更なし", r.stdout)

    def test_b9_own_usage_block_is_reverted(self) -> None:
        self.set_state([own("workers", action="usage_block"), own("durable_objects")])
        r = self.run_script("--check", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("workers: 直す（", r.stdout)
        self.assertIn("action: usage_block → notification", r.stdout)
        self.assertIn("変更あり（1 件）", r.stdout)
        r = self.run_script("--apply", "--yes", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual({p["action"] for p in self.state()}, {"notification"})
        self.assert_common_invariants()

    # -- B-10: どのモードでも usage_block を送らず delete を呼ばない ----------------

    def test_b10_never_sends_usage_block_or_delete(self) -> None:
        scenarios = [
            ([], ["--apply", "--yes", "--email", "owner@example.com"]),
            ([own("workers", action="usage_block", enabled=False)], ["--apply", "--yes", "--email", "o@example.com"]),
            ([], ["--threshold-usd", "30"]),
        ]
        for policies, args in scenarios:
            with self.subTest(args=args):
                self.set_state(policies)
                if self.log_path.exists():
                    self.log_path.unlink()
                r = self.run_script(*args)
                self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
                self.assertTrue(self.bodies())
                self.assert_common_invariants()

    def test_b10_ensure_notification_rejects_usage_block(self) -> None:
        mod = load_module()
        body = mod.build_body("workers", 5, "o@example.com")
        mod.ensure_notification(body)
        body["action"] = "usage_block"
        with self.assertRaises(mod.Failure):
            mod.ensure_notification(body)

    # -- B-11: 失敗 ------------------------------------------------------------

    def test_b11_duplicate_names(self) -> None:
        self.set_state([own("workers", id="a"), own("workers", id="b")])
        r = self.run_script("--check", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 1, r.stdout)
        self.assertIn("同じ名前のポリシーが複数ある", r.stderr)
        r = self.run_script("--apply", "--yes", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 1)
        for w in WRITE_CMDS:
            self.assertNotIn(w, self.cmds())

    def test_b11_update_version_mismatch(self) -> None:
        # 一覧の version と状態の version を食い違わせる（ほかの誰かが同時に変えた）
        fake = self.fake.read_text(encoding="utf-8")
        fake = fake.replace(
            'print(json.dumps([strip(p, "hide_version_in_list") for p in state["policies"]]))',
            'print(json.dumps([dict(strip(p, "hide_version_in_list"), version=p["version"] - 1) '
            'for p in state["policies"]]))',
        )
        self.fake.write_text(fake, encoding="utf-8")
        self.set_state([own("workers", threshold_cents=300, version=5), own("durable_objects")])
        r = self.run_script("--apply", "--yes", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("version mismatch", r.stderr)
        self.assertIn("update", self.cmds())
        self.assertEqual(self.state()[0]["threshold_cents"], 300)

    def test_b11_list_fails(self) -> None:
        for mode in (["--check"], ["--apply", "--yes"]):
            with self.subTest(mode=mode):
                r = self.run_script(*mode, "--email", "owner@example.com", fail="list")
                self.assertEqual(r.returncode, 1, r.stdout)
                self.assertIn("fake cf: list failed: spend policies are not enabled for this account", r.stderr)
                for w in WRITE_CMDS:
                    self.assertNotIn(w, self.cmds())

    def test_dry_run_cf_fails(self) -> None:
        r = self.run_script(account=False, fail="create")
        self.assertEqual(r.returncode, 1)
        self.assertIn("fake cf: create failed", r.stderr)

    def test_cf_not_found(self) -> None:
        r = self.run_script(account=False, env={"CF": str(self.tmp / "no-such-cf")})
        self.assertEqual(r.returncode, 1)
        self.assertIn("cf を実行できない", r.stderr)

    # -- version の取り方 -------------------------------------------------------

    def test_version_from_get_when_missing_in_list(self) -> None:
        self.set_state([own("workers", threshold_cents=300, version=3), own("durable_objects")], hide_version_in_list=True)
        r = self.run_script("--apply", "--yes", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        calls = self.calls()
        self.assertEqual([c["args"][2] for c in calls], ["list", "get", "update", "list"])
        self.assertEqual(calls[1]["args"][3], "pol-own-workers")
        upd = calls[2]["args"]
        self.assertEqual(upd[upd.index("--if-match") + 1], "3")

    def test_version_missing_everywhere(self) -> None:
        self.set_state(
            [own("workers", threshold_cents=300), own("durable_objects")],
            hide_version_in_list=True,
            hide_version_in_get=True,
        )
        r = self.run_script("--apply", "--yes", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("--if-match を送れない", r.stderr)
        self.assertNotIn("update", self.cmds())

    def test_mismatch_after_apply(self) -> None:
        # create が状態を変えない偽の cf にして、反映の後の確認で差を出す
        fake = self.fake.read_text(encoding="utf-8")
        fake = fake.replace('state["policies"].append(p)\n', "pass\n")
        self.fake.write_text(fake, encoding="utf-8")
        r = self.run_script("--apply", "--yes", "--email", "owner@example.com")
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("budget_alert: 反映の後の確認で差がある", r.stderr)


class PureFunctionTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.mod = load_module()

    def test_constants(self) -> None:
        m = self.mod
        self.assertEqual(m.PRODUCTS, ("workers", "durable_objects"))
        self.assertEqual(m.DEFAULT_THRESHOLD_USD, 5)
        self.assertEqual(m.MAX_THRESHOLD_USD, 30)
        self.assertEqual(m.policy_name("durable_objects"), "raft-game-budget-durable-objects")

    def test_threshold(self) -> None:
        m = self.mod
        self.assertEqual(m.parse_threshold_usd("1"), 1)
        self.assertEqual(m.parse_threshold_usd("30"), 30)
        for bad in ("0", "31", "5.5", "abc", "", "-1", "1e1"):
            with self.subTest(bad=bad):
                with self.assertRaises(m.UsageError):
                    m.parse_threshold_usd(bad)

    def test_email(self) -> None:
        m = self.mod
        self.assertEqual(m.validate_email("  a@example.com "), "a@example.com")
        for bad in ("", "Name <a@example.com>", "a b@example.com", "a@b", "a@@example.com", '"a"@example.com'):
            with self.subTest(bad=bad):
                with self.assertRaises(m.UsageError):
                    m.validate_email(bad)

    def test_diff_strict_types(self) -> None:
        m = self.mod
        body = m.build_body("workers", 5, "o@example.com")
        cur = dict(body, enabled=1)
        self.assertEqual(m.diff_policy(cur, body), ["enabled"])
        self.assertEqual(m.diff_policy(dict(body), body), [])

    def test_cf_command(self) -> None:
        m = self.mod
        old = os.environ.get("CF")
        try:
            os.environ["CF"] = "/usr/bin/env python3 'a b.py'"
            self.assertEqual(m.cf_command(), ["/usr/bin/env", "python3", "a b.py"])
            os.environ.pop("CF")
            self.assertEqual(m.cf_command(), [str(REPO_ROOT / "web" / "node_modules" / ".bin" / "cf")])
        finally:
            if old is None:
                os.environ.pop("CF", None)
            else:
                os.environ["CF"] = old


if __name__ == "__main__":
    unittest.main()
