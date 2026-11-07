"""scripts/check-license.py のテスト（T-009 / Issue #17）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_*.py'

一時ディレクトリに L-1〜L-5 を満たす最小の木を作り、1 か所ずつ壊して確かめる。
"""

import contextlib
import importlib.util
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Optional

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
SCRIPT = REPO_ROOT / "scripts" / "check-license.py"

_spec = importlib.util.spec_from_file_location("check_license", SCRIPT)
assert _spec is not None and _spec.loader is not None
check_license = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(check_license)

MAKE = shutil.which("make")

TITLE = "Project LINKS アイディアソン公開データ（2026年度）離島に関するデータ"
LICENSE_NAME = "政府標準利用規約（第2.0版）"
TERMS = "https://www.mlit.go.jp/links/terms-of-use.html"

NOTICE = f"""example
Copyright 2026 The raft-game Authors

This product is licensed under the Apache License, Version 2.0 (see LICENSE).

etcd-io/raft (go.etcd.io/raft/v3 v3.7.0)
This repository includes a modified copy under server/third_party/etcd-raft/.
See server/third_party/etcd-raft/PATCHES.md.

Open data
{TITLE}
利用規約：{LICENSE_NAME}（{TERMS}）

Generated assets
web/public/assets/CREDITS.md に記載します。
"""

GO_MOD = """module example.com/server

go 1.25

require go.etcd.io/raft/v3 v3.7.0

replace go.etcd.io/raft/v3 => ./third_party/etcd-raft
"""


def run_main(*args: str):
    out = io.StringIO()
    err = io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        code = check_license.main(list(args))
    return code, out.getvalue(), err.getvalue()


class CheckLicenseTest(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        self.make_tree()

    def tearDown(self) -> None:
        self._tmp.cleanup()

    # --- 補助 ---------------------------------------------------------------
    def write(self, rel: str, content: str) -> None:
        path = self.root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")

    def sources(self, *extra: dict) -> None:
        items = {"mlit": {"title": TITLE, "license": LICENSE_NAME, "termsUrl": TERMS}}
        for i, e in enumerate(extra):
            items[f"extra{i}"] = e
        self.write("data/islands.json", json.dumps({"sources": items, "islands": []}, ensure_ascii=False))

    def make_tree(self) -> None:
        lic = (REPO_ROOT / "LICENSE").read_bytes()
        (self.root / "LICENSE").write_bytes(lic)
        self.write("NOTICE", NOTICE)
        self.write("server/go.mod", GO_MOD)
        (self.root / "server/third_party/etcd-raft").mkdir(parents=True)
        (self.root / "server/third_party/etcd-raft/LICENSE").write_bytes(lic)
        self.write("server/third_party/etcd-raft/PATCHES.md", "# patches\n")
        self.sources()
        self.write("data/raw/README.md", f"| 利用規約 | {TERMS} |\n")

    def check(self, expect_code: int, ng: Optional[str] = None):
        code, out, err = run_main("--root", str(self.root))
        self.assertEqual(code, expect_code, out + err)
        self.assertNotIn("Traceback", out + err)
        if ng is not None:
            self.assertIn(f"NG {ng}:", out)
        if expect_code == 0:
            self.assertIn("OK: LICENSE・NOTICE（L-1〜L-5）", out)
        return out

    def replace_notice(self, old: str, new: str) -> None:
        text = (self.root / "NOTICE").read_text(encoding="utf-8")
        self.assertIn(old, text)
        self.write("NOTICE", text.replace(old, new))

    # --- 全体 ---------------------------------------------------------------
    def test_real_repo(self) -> None:
        code, out, err = run_main("--root", str(REPO_ROOT))
        self.assertEqual(code, 0, out + err)

    def test_default_root_is_repo_cli(self) -> None:
        # --root を省くと、カレントディレクトリでなくスクリプトの 1 つ上（このリポジトリ）を見る。
        res = subprocess.run(
            [sys.executable, str(SCRIPT)],
            cwd=self.root,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        self.assertIn("OK: LICENSE・NOTICE（L-1〜L-5）", res.stdout)

    def test_minimal_tree(self) -> None:
        self.check(0)

    def test_newline_inside_title(self) -> None:
        self.replace_notice("（2026年度）離島", "（2026年度）\n離島")
        self.check(0)

    def test_multiple_problems_all_reported(self) -> None:
        (self.root / "LICENSE").unlink()
        self.write("server/go.mod", GO_MOD.replace("v3.7.0", "v3.7.1"))
        out = self.check(1)
        self.assertIn("NG L-1:", out)
        self.assertIn("NG L-3:", out)

    def test_root_not_found(self) -> None:
        code, _, _ = run_main("--root", str(self.root / "nope"))
        self.assertEqual(code, 2)

    def test_bad_argument(self) -> None:
        code, _, _ = run_main("--unknown")
        self.assertEqual(code, 2)

    # --- L-1 ---------------------------------------------------------------
    def test_l1_missing(self) -> None:
        (self.root / "LICENSE").unlink()
        self.check(1, "L-1")

    def test_l1_modified(self) -> None:
        data = bytearray((self.root / "LICENSE").read_bytes())
        data[100] = ord("X") if data[100] != ord("X") else ord("Y")
        (self.root / "LICENSE").write_bytes(bytes(data))
        self.check(1, "L-1")

    # --- L-2 ---------------------------------------------------------------
    def test_l2_missing(self) -> None:
        (self.root / "NOTICE").unlink()
        self.check(1, "L-2")

    def test_l2_empty(self) -> None:
        self.write("NOTICE", "")
        self.check(1, "L-2")

    def test_l2_no_license_name(self) -> None:
        self.replace_notice("Apache License, Version 2.0", "MIT License")
        self.check(1, "L-2")

    # --- L-3 ---------------------------------------------------------------
    def test_l3_version_mismatch(self) -> None:
        self.write("server/go.mod", GO_MOD.replace("v3.7.0", "v3.7.1"))
        out = self.check(1, "L-3")
        self.assertIn("go.etcd.io/raft/v3 v3.7.1", out)

    def test_l3_require_block(self) -> None:
        self.write(
            "server/go.mod",
            "module x\n\nrequire (\n\tgo.etcd.io/raft/v3 v3.7.0 // indirect\n)\n",
        )
        self.check(0)

    def test_l3_other_dir_without_license(self) -> None:
        (self.root / "server/third_party/foo").mkdir()
        self.replace_notice("Open data", "server/third_party/foo/\nOpen data")
        out = self.check(1, "L-3")
        self.assertIn("server/third_party/foo", out)

    def test_l3_other_dir_not_in_notice(self) -> None:
        self.write("server/third_party/foo/LICENSE", "MIT\n")
        out = self.check(1, "L-3")
        self.assertIn("server/third_party/foo/", out)

    def test_l3_other_dir_ok(self) -> None:
        self.write("server/third_party/foo/COPYING", "GPL\n")
        self.replace_notice("Open data", "server/third_party/foo/\nOpen data")
        self.check(0)

    def test_l3_patches_missing(self) -> None:
        (self.root / "server/third_party/etcd-raft/PATCHES.md").unlink()
        self.check(1, "L-3")

    def test_l3_etcd_license_not_apache(self) -> None:
        self.write("server/third_party/etcd-raft/LICENSE", "something else\n")
        self.check(1, "L-3")

    def test_l3_no_require(self) -> None:
        self.write("server/go.mod", "module x\n\nreplace go.etcd.io/raft/v3 v3.7.0 => ./x\n")
        out = self.check(1, "L-3")
        self.assertIn("require", out)

    def test_l3_no_third_party(self) -> None:
        shutil.rmtree(self.root / "server/third_party")
        out = self.check(1, "L-3")
        self.assertIn("server/third_party/etcd-raft", out)

    # --- L-4 ---------------------------------------------------------------
    def test_l4_other_terms(self) -> None:
        self.sources({"title": TITLE, "license": LICENSE_NAME, "termsUrl": "https://example.com/terms"})
        out = self.check(1, "L-4")
        self.assertIn("https://example.com/terms", out)

    def test_l4_same_terms_twice(self) -> None:
        self.sources({"title": TITLE, "license": LICENSE_NAME, "termsUrl": TERMS})
        self.check(0)

    def test_l4_sources_list(self) -> None:
        self.write(
            "data/islands.json",
            json.dumps({"sources": [{"title": TITLE, "license": LICENSE_NAME, "termsUrl": TERMS}]}),
        )
        self.check(0)

    def test_l4_raw_readme_without_terms(self) -> None:
        self.write("data/raw/README.md", "# raw\n")
        out = self.check(1, "L-4")
        self.assertIn("data/raw/README.md", out)

    def test_l4_invalid_json(self) -> None:
        self.write("data/islands.json", "{not json")
        self.check(1, "L-4")

    def test_l4_no_sources(self) -> None:
        self.write("data/islands.json", json.dumps({"islands": []}))
        self.check(1, "L-4")

    # --- L-5 ---------------------------------------------------------------
    def test_l5_notice_without_credits(self) -> None:
        self.replace_notice("web/public/assets/CREDITS.md", "docs/07-data-spec.md")
        self.check(1, "L-5")

    def test_l5_gitkeep_only(self) -> None:
        self.write("web/public/assets/generated/.gitkeep", "")
        self.check(0)

    def test_l5_generated_without_credits(self) -> None:
        self.write("web/public/assets/generated/a.png", "png")
        self.check(1, "L-5")

    def test_l5_credits_without_file(self) -> None:
        self.write("web/public/assets/generated/a.png", "png")
        self.write("web/public/assets/CREDITS.md", "# クレジット\n- b.png\n")
        out = self.check(1, "L-5")
        self.assertIn("a.png", out)

    def test_l5_credits_with_file(self) -> None:
        self.write("web/public/assets/generated/a.png", "png")
        self.write("web/public/assets/CREDITS.md", "# クレジット\n- a.png: 画像生成 AI\n")
        self.check(0)


def make(*args: str) -> subprocess.CompletedProcess:
    env = dict(os.environ)
    # 外側の make から呼ばれたときのフラグ（-n など）を引き継がない。
    for key in ("MAKEFLAGS", "MAKELEVEL", "MFLAGS", "MAKEOVERRIDES", "DEV_SLOT", "DOCKER", "DRY_RUN"):
        env.pop(key, None)
    assert MAKE is not None
    return subprocess.run(
        [MAKE, "--no-print-directory", *args],
        cwd=REPO_ROOT,
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        timeout=60,
    )


@unittest.skipIf(MAKE is None, "make がない")
class MakeTargetTest(unittest.TestCase):
    def test_dry_run(self) -> None:
        res = make("-n", "check-license")
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("python3 scripts/check-license.py", res.stdout)

    def test_help_lists_target(self) -> None:
        res = make("help")
        self.assertEqual(res.returncode, 0, res.stderr)
        listed = {line.split()[0] for line in res.stdout.splitlines() if line.startswith("  ")}
        self.assertIn("check-license", listed)


if __name__ == "__main__":
    unittest.main()
