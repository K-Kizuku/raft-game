"""ルートの Makefile と雛形のテスト（T-001 / Issue #9）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_*.py'

`make test`・`make lint` は、このテスト自身を実行するため、ここでは `make -n`
（実行せずに表示だけ）で確かめる。
"""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from typing import Optional

from test_dev_env import make_fake_docker, read_log

REPO_ROOT = Path(__file__).resolve().parent.parent.parent

TARGETS = ["test", "lint", "dev", "dev-env", "clean-docker", "gen", "sync-data", "demo-local", "help"]

UNIMPLEMENTED: dict = {}

SKELETON_DIRS = [
    "server/cmd",
    "server/internal",
    "web",
    "data/raw",
    "data/curated",
    "data/stages",
]

MAKE = shutil.which("make")


def make(*args: str, extra_env: Optional[dict] = None) -> subprocess.CompletedProcess:
    env = dict(os.environ)
    # 外側の make から呼ばれたときのフラグ（-n など）を引き継がない。
    # Worker は DEV_SLOT を渡されて動くので、DEV_SLOT・DOCKER なども毎回消す。
    for key in ("MAKEFLAGS", "MAKELEVEL", "MFLAGS", "MAKEOVERRIDES", "DEV_SLOT", "DOCKER", "DRY_RUN"):
        env.pop(key, None)
    env.update(extra_env or {})
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

    # M-1
    def test_dev_requires_dev_slot(self) -> None:
        res = make("dev")
        self.assertNotEqual(res.returncode, 0)
        self.assertIn("DEV_SLOT", res.stdout + res.stderr)

    # M-2
    def test_dev_dry_run(self) -> None:
        res = make("dev", "DRY_RUN=1", extra_env={"DEV_SLOT": "2"})
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("--port 15193", res.stdout)
        self.assertIn("--strictPort", res.stdout)
        self.assertTrue(
            "PORT=18100" in res.stdout or "server/cmd/server がない" in res.stdout,
            res.stdout,
        )

    # M-3
    def test_dev_env(self) -> None:
        res = make("dev-env", extra_env={"DEV_SLOT": "2"})
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("DEV_WEB_PORT=15193", res.stdout)
        self.assertIn("DEV_SERVER_PORT=18100", res.stdout)

    # MK-1（T-504）: make demo-local DRY_RUN=1 は Go（デモ操作あり）と Vite の起動を表示する。
    def test_demo_local_dry_run(self) -> None:
        res = make("demo-local", "DRY_RUN=1", "CHECK=", "DEMO_DATA_DIR=server/internal/server/testdata")
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        self.assertIn("DEMO_CONTROLS=1", res.stdout)
        self.assertIn("DEV_BACKEND=standalone", res.stdout)

    # M-4
    def test_clean_docker_dry_run(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            docker = make_fake_docker(Path(tmp))
            log = Path(tmp) / "docker.log"
            res = make(
                "clean-docker",
                "DRY_RUN=1",
                extra_env={"DOCKER": str(docker), "FAKE_LOG": str(log), "FAKE_IMAGE": "raft-game-server:x"},
            )
            self.assertEqual(res.returncode, 0, res.stderr)
            calls = read_log(log)
            self.assertTrue(calls)
            self.assertFalse(any("rm" in c for c in calls), calls)


class SyncDataTest(unittest.TestCase):
    """MK-2（T-302・D-2 の案）: make sync-data が data/ を embedded/ にコピーする。"""

    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.tmp = Path(self._tmp.name)
        self.src = self.tmp / "data"
        (self.src / "stages").mkdir(parents=True)
        (self.src / "islands.json").write_text('{"x":1}', encoding="utf-8")
        (self.src / "stages" / "alpha.yaml").write_text("id: alpha\n", encoding="utf-8")
        (self.src / "stages" / "beta.yaml").write_text("id: beta\n", encoding="utf-8")
        (self.src / "stages" / "typo.yml").write_text("id: typo\n", encoding="utf-8")
        (self.src / "stages" / "sub").mkdir()
        (self.src / "stages" / "sub" / "skip.yaml").write_text("id: skip\n", encoding="utf-8")

    def tearDown(self) -> None:
        self._tmp.cleanup()

    def sync(self, dest: Path) -> subprocess.CompletedProcess:
        return make("sync-data", f"SYNC_DATA_SRC={self.src}", f"SYNC_DATA_DEST={dest}")

    def test_copies(self) -> None:
        dest = self.tmp / "out" / "embedded"
        (dest / "stages").mkdir(parents=True)
        (dest / "stale.json").write_text("{}", encoding="utf-8")
        (dest / "stages" / "old.yaml").write_text("id: old\n", encoding="utf-8")
        res = self.sync(dest)
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        self.assertIn("ステージ 2 個", res.stdout)
        self.assertEqual((dest / "islands.json").read_text(encoding="utf-8"), '{"x":1}')
        names = sorted(p.name for p in (dest / "stages").iterdir())
        self.assertEqual(names, [".gitkeep", "alpha.yaml", "beta.yaml", "typo.yml"])
        self.assertFalse((dest / "stale.json").exists())

    def test_missing_islands(self) -> None:
        (self.src / "islands.json").unlink()
        dest = self.tmp / "out" / "embedded"
        dest.mkdir(parents=True)
        (dest / "keep").write_text("x", encoding="utf-8")
        res = self.sync(dest)
        self.assertNotEqual(res.returncode, 0)
        self.assertIn("islands.json", res.stderr)
        self.assertTrue((dest / "keep").exists())

    def test_dest_must_end_with_embedded(self) -> None:
        dest = self.tmp / "out"
        dest.mkdir()
        (dest / "keep").write_text("x", encoding="utf-8")
        res = self.sync(dest)
        self.assertNotEqual(res.returncode, 0)
        self.assertIn("/embedded", res.stderr)
        self.assertTrue((dest / "keep").exists())

    def test_default_dest(self) -> None:
        res = make("-n", "sync-data")
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("server/internal/stage/embedded", res.stdout)


if __name__ == "__main__":
    unittest.main()
