"""shell のスクリプトを bash 3.2（macOS の /bin/bash）でも動かすための検査（#107 / AG-10）。

bash 3.2 と UTF-8 のロケールでは、"$t）" のように変数の直後に ASCII 以外の文字があると、
そのバイトまで変数名として読まれ、set -u で「t�: unbound variable」になる。
（hooks が終了コード 1 で落ち、Claude Code が拒否として扱わずに操作を通した。）
変数の直後に ASCII 以外の文字が続くときは ${t} と書く。

実行: python3 -m unittest discover -s scripts/tests -p 'test_*.py'
"""

import re
import subprocess
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent

# $name の直後に ASCII 以外の文字。${name}・$1・$?・$$ などは対象外。
# 引用符やコメントの中も区別しない（どこにあっても ${name} と書く）。
BARE_VAR_BEFORE_NON_ASCII = re.compile(r"\$[A-Za-z_][A-Za-z0-9_]*(?=[^\x00-\x7f])")


def find_bare_vars(text: str) -> list:
    """(行番号, 一致した $name, 行) の一覧。"""
    hits = []
    for lineno, line in enumerate(text.splitlines(), 1):
        for m in BARE_VAR_BEFORE_NON_ASCII.finditer(line):
            hits.append((lineno, m.group(0), line.strip()))
    return hits


def shell_scripts() -> list:
    """Git が追跡している *.sh（Git が使えなければ、作業ツリーの *.sh）。"""
    try:
        res = subprocess.run(
            ["git", "-C", str(REPO_ROOT), "ls-files", "-z", "--", "*.sh"],
            capture_output=True,
            check=True,
        )
        files = [REPO_ROOT / p for p in res.stdout.decode("utf-8").split("\0") if p]
    except (OSError, subprocess.CalledProcessError):
        files = [
            p
            for p in REPO_ROOT.rglob("*.sh")
            if not any(part in ("node_modules", ".git", "worktrees") for part in p.parts)
        ]
    return sorted(p for p in files if p.is_file())


class DetectorTest(unittest.TestCase):
    def test_detects_bare_var_before_non_ascii(self) -> None:
        for src in ['deny "対象が危険（$t）"', "echo $n件", 'x="$PROTECTED_MSG（a）"', "# $CLAUDE_PROJECT_DIR（コメント）"]:
            with self.subTest(src=src):
                self.assertEqual(len(find_bare_vars(src)), 1, src)

    def test_allows_braces_and_ascii(self) -> None:
        for src in [
            'deny "対象が危険（${t}）"',
            'echo "$t は危険"',
            'echo "$1（"',
            'echo "$?（"',
            'echo "$$（"',
            'echo "${t:-なし}（"',
            "echo $t.",
            'echo "${#arr[@]}件"',
        ]:
            with self.subTest(src=src):
                self.assertEqual(find_bare_vars(src), [], src)


class ShellScriptsTest(unittest.TestCase):
    def test_scripts_found(self) -> None:
        names = {p.name for p in shell_scripts()}
        self.assertIn("guard-bash.sh", names)
        self.assertIn("spawn-worker.sh", names)

    def test_no_bare_var_before_non_ascii(self) -> None:
        problems = []
        for path in shell_scripts():
            text = path.read_text(encoding="utf-8")
            for lineno, var, line in find_bare_vars(text):
                problems.append(f"{path.relative_to(REPO_ROOT)}:{lineno}: {var} → ${{{var[1:]}}}: {line}")
        self.assertEqual(
            problems,
            [],
            "変数の直後に ASCII 以外の文字が続く（bash 3.2 では変数名の一部として読まれる。${var} と書く）:\n"
            + "\n".join(problems),
        )


if __name__ == "__main__":
    unittest.main()
