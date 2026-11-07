"""ルートの Makefile と雛形のテスト（T-001 / Issue #9）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_*.py'

`make test`・`make lint` は、このテスト自身を実行するため、ここでは `make -n`
（実行せずに表示だけ）で確かめる。
"""

import json
import os
import shutil
import subprocess
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent

TARGETS = ["test", "lint", "dev", "gen", "sync-data", "demo-local", "help"]

UNIMPLEMENTED = {
    "gen": "T-301",
    "sync-data": "T-201/T-205",
    "dev": "T-008",
    "demo-local": "T-504",
}

SKELETON_DIRS = [
    "server/cmd",
    "server/internal",
    "web",
    "data/raw",
    "data/curated",
    "data/stages",
]

MAKE = shutil.which("make")


def make(*args: str) -> subprocess.CompletedProcess:
    env = dict(os.environ)
    # 外側の make から呼ばれたときのフラグ（-n など）を引き継がない。
    for key in ("MAKEFLAGS", "MAKELEVEL", "MFLAGS", "MAKEOVERRIDES"):
        env.pop(key, None)
    return subprocess.run(
        [MAKE, "--no-print-directory", *args],
        cwd=REPO_ROOT,
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        timeout=60,
    )


class SkeletonTest(unittest.TestCase):
    def test_skeleton_dirs_exist(self) -> None:
        for d in SKELETON_DIRS:
            with self.subTest(dir=d):
                self.assertTrue((REPO_ROOT / d).is_dir(), f"{d} がない")
                self.assertTrue((REPO_ROOT / d / ".gitkeep").is_file(), f"{d}/.gitkeep がない")

    def test_pnpm_workspace_has_web(self) -> None:
        text = (REPO_ROOT / "pnpm-workspace.yaml").read_text(encoding="utf-8")
        entries = [
            line.strip()[1:].strip().strip("'\"")
            for line in text.splitlines()
            if line.strip().startswith("-")
        ]
        self.assertIn("packages:", text)
        self.assertIn("web", entries)

    def test_root_package_json(self) -> None:
        pkg = json.loads((REPO_ROOT / "package.json").read_text(encoding="utf-8"))
        self.assertIs(pkg.get("private"), True)
        self.assertEqual(pkg.get("packageManager"), "pnpm@9.15.0")
        self.assertNotIn("scripts", pkg)

    def test_gitignore_entries(self) -> None:
        lines = (REPO_ROOT / ".gitignore").read_text(encoding="utf-8").splitlines()
        for entry in [
            "node_modules/",
            "dist/",
            "web/.cloudflare/",
            ".cloudflare/",
            "server/internal/stage/embedded/",
            "coverage/",
            # 既存の行が残っている
            ".agents/run/",
            "__pycache__/",
        ]:
            with self.subTest(entry=entry):
                self.assertIn(entry, lines)


@unittest.skipUnless(MAKE, "make がない")
class MakefileTest(unittest.TestCase):
    def test_default_goal_is_help(self) -> None:
        default = make()
        self.assertEqual(default.returncode, 0, default.stderr)
        self.assertEqual(default.stdout, make("help").stdout)

    def test_help_lists_all_targets(self) -> None:
        res = make("help")
        self.assertEqual(res.returncode, 0, res.stderr)
        listed = {line.split()[0] for line in res.stdout.splitlines() if line.startswith("  ")}
        for t in TARGETS:
            with self.subTest(target=t):
                self.assertIn(t, listed)

    def test_dry_run_test_and_lint(self) -> None:
        for t in ["test", "lint"]:
            with self.subTest(target=t):
                res = make("-n", t)
                self.assertEqual(res.returncode, 0, res.stderr)

    def test_unimplemented_targets(self) -> None:
        for t, task in UNIMPLEMENTED.items():
            with self.subTest(target=t):
                res = make(t)
                self.assertEqual(res.returncode, 0, res.stderr)
                self.assertIn("未実装", res.stdout)
                self.assertIn(task, res.stdout)


if __name__ == "__main__":
    unittest.main()
