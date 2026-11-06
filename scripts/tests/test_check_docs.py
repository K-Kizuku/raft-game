"""scripts/check-docs.py のテスト（Issue #2 / AG-1）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_*.py'
"""

import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "check-docs.py"
REPO_ROOT = SCRIPT.parent.parent


def run(root: Path) -> subprocess.CompletedProcess:
    return subprocess.run(
        [sys.executable, str(SCRIPT), str(root)],
        capture_output=True,
        text=True,
        encoding="utf-8",
    )


class CheckDocsTest(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)

    def tearDown(self) -> None:
        self._tmp.cleanup()

    def write(self, rel: str, content: str = "") -> None:
        path = self.root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(textwrap.dedent(content), encoding="utf-8")

    def test_valid_relative_links(self) -> None:
        self.write("a.md", "[b](b.md)\n[sub](sub/c.md)\n[dir](sub)\n[dir2](sub/)\n")
        self.write("b.md")
        self.write("sub/c.md", "[up](../a.md)\n[root](/b.md)\n![img](./c.md \"title\")\n")
        r = run(self.root)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(r.stdout, "")

    def test_missing_file_reports_line(self) -> None:
        self.write("a.md", "# title\n\nsee [x](missing.md) here\n[ok](a.md)\n")
        self.write("sub/b.md", "line1\n[y](../nope/z.md#sec)\n")
        r = run(self.root)
        self.assertEqual(r.returncode, 1)
        self.assertEqual(
            r.stdout.splitlines(),
            ["a.md:3: missing.md", "sub/b.md:2: ../nope/z.md#sec"],
        )

    def test_links_in_code_are_ignored(self) -> None:
        self.write(
            "a.md",
            """\
            ```
            [x](missing1.md)
            ```

            ~~~~md
            [x](missing2.md)
            ```
            [x](missing3.md)
            ~~~~

            inline `[x](missing4.md)` and ``[y](missing5.md)``
            """,
        )
        r = run(self.root)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_link_after_code_block_is_checked(self) -> None:
        self.write("a.md", "```\ncode\n```\n[x](missing.md)\n")
        r = run(self.root)
        self.assertEqual(r.returncode, 1)
        self.assertEqual(r.stdout.splitlines(), ["a.md:4: missing.md"])

    def test_external_and_anchor_only_links_are_ignored(self) -> None:
        self.write(
            "a.md",
            "[a](http://example.com/x.md) [b](https://example.com/y.md)\n"
            "[c](mailto:a@example.com) [d](#section) [e](HTTPS://EXAMPLE.COM)\n",
        )
        r = run(self.root)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_anchor_checks_only_file_existence(self) -> None:
        self.write("a.md", "[ok](b.md#no-such-anchor)\n[ng](c.md#anchor)\n")
        self.write("b.md", "# only heading\n")
        r = run(self.root)
        self.assertEqual(r.returncode, 1)
        self.assertEqual(r.stdout.splitlines(), ["a.md:2: c.md#anchor"])

    def test_japanese_and_url_encoded_names(self) -> None:
        self.write("日本語 ファイル.md")
        self.write(
            "a.md",
            "[ja](日本語%20ファイル.md)\n"
            "[enc](%E6%97%A5%E6%9C%AC%E8%AA%9E%20%E3%83%95%E3%82%A1%E3%82%A4%E3%83%AB.md)\n"
            "[angle](<日本語 ファイル.md>)\n"
            "[ng](存在しない.md)\n",
        )
        r = run(self.root)
        self.assertEqual(r.returncode, 1)
        self.assertEqual(r.stdout.splitlines(), ["a.md:4: 存在しない.md"])

    @unittest.skipIf(shutil.which("git") is None, "git がない")
    def test_git_repository_uses_tracked_files_only(self) -> None:
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        self.write("tracked.md", "[x](missing-a.md)\n")
        self.write("untracked.md", "[x](missing-b.md)\n")
        subprocess.run(["git", "-C", str(self.root), "add", "tracked.md"], check=True)
        r = run(self.root)
        self.assertEqual(r.returncode, 1)
        self.assertEqual(r.stdout.splitlines(), ["tracked.md:1: missing-a.md"])

    def test_current_repository_passes(self) -> None:
        r = run(REPO_ROOT)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)


if __name__ == "__main__":
    unittest.main()
