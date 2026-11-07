"""CLAUDE.md・CONTRIBUTING.md・README.md と実際の構成の食い違いを見つけるテスト（T-006 / Issue #14）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_*.py'

文書に書いた `make <ターゲット>` が Makefile に定義されていることと、インラインコードに
書いたリポジトリの中のパスが実在する（追跡されているか、.gitignore の対象）ことを確かめる。
標準ライブラリだけを使う（Python 3.9 以上）。
"""

import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from typing import Callable, Iterable, Iterator, List, Optional, Set, Tuple

REPO_ROOT = Path(__file__).resolve().parent.parent.parent

DOCS = ["CLAUDE.md", "CONTRIBUTING.md", "README.md"]

# コードフェンスの開始・終了（行頭の空白は 3 個まで。scripts/check-docs.py と同じ）
FENCE_RE = re.compile(r"^ {0,3}(`{3,}|~{3,})")

# インラインコード（同じ数のバッククォートで閉じる）
INLINE_CODE_RE = re.compile(r"(`+)(.+?)\1")

# `make <ターゲット>`。`make -C …`・`make -n …` のようにオプションが続くものは取らない
MAKE_TARGET_RE = re.compile(r"(?<![\w-])make\s+([a-z][a-z0-9-]*)")

# Makefile のターゲットの定義
MAKEFILE_TARGET_RE = re.compile(r"^([a-z][a-z0-9-]*):", re.MULTILINE)

# 実在を確かめるパスの先頭
PATH_PREFIXES = ("server/", "web/", "data/", "spike/", "scripts/", "tools/", "docs/", ".github/")

# これを含むパスは、glob・置き換え・省略なので確かめない
SKIP_CHARS = set("*?<>{}…$")


def iter_code(text: str) -> Iterator[Tuple[int, str, bool]]:
    """(行番号, コードの中身, フェンスの中か) を順に返す。

    フェンスの中は 1 行ずつ、フェンスの外はインラインコードを 1 つずつ返す。
    """
    fence: Optional[str] = None
    for lineno, line in enumerate(text.splitlines(), start=1):
        m = FENCE_RE.match(line)
        if fence is None:
            if m:
                fence = m.group(1)
                continue
            for im in INLINE_CODE_RE.finditer(line):
                yield lineno, im.group(2), False
        else:
            # 閉じるフェンスは、同じ文字で同じ長さ以上、後ろに何も書かない（scripts/check-docs.py と同じ）
            if m and m.group(1)[0] == fence[0] and len(m.group(1)) >= len(fence) \
                    and line.strip() == m.group(1):
                fence = None
                continue
            yield lineno, line, True


def extract_make_targets(text: str) -> List[Tuple[int, str]]:
    """インラインコードとフェンスの中から `make <ターゲット>` の (行番号, ターゲット) を集める。"""
    found: List[Tuple[int, str]] = []
    for lineno, code, _ in iter_code(text):
        for m in MAKE_TARGET_RE.finditer(code):
            found.append((lineno, m.group(1)))
    return found


def extract_paths(text: str) -> List[Tuple[int, str]]:
    """インラインコードから、リポジトリの中のパスの (行番号, パス) を集める。"""
    found: List[Tuple[int, str]] = []
    for lineno, code, in_fence in iter_code(text):
        if in_fence:
            continue
        parts = code.strip().split()
        if not parts:
            continue
        head = parts[0]
        if not head.startswith(PATH_PREFIXES):
            continue
        if any(c in SKIP_CHARS for c in head):
            continue
        head = head.rstrip("/")
        if head:
            found.append((lineno, head))
    return found


def makefile_targets(makefile: str) -> Set[str]:
    return set(MAKEFILE_TARGET_RE.findall(makefile))


def git_env() -> dict:
    """外側の git（hooks・worktree など）の環境変数を引き継がない。"""
    env = dict(os.environ)
    for key in list(env):
        if key.startswith("GIT_"):
            env.pop(key)
    return env


class RepoPaths:
    """git のリポジトリの中にパスがあるか（追跡されているか、その下に追跡されたファイルがあるか、.gitignore の対象か）。"""

    def __init__(self, root: Path) -> None:
        self.root = root
        out = subprocess.run(
            ["git", "-C", str(root), "ls-files", "-z"],
            capture_output=True,
            check=True,
            env=git_env(),
        ).stdout.decode("utf-8")
        self.files: Set[str] = {f for f in out.split("\0") if f}
        self.dirs: Set[str] = set()
        for f in self.files:
            parts = f.split("/")
            for i in range(1, len(parts)):
                self.dirs.add("/".join(parts[:i]))

    def exists(self, path: str) -> bool:
        if path in self.files or path in self.dirs:
            return True
        r = subprocess.run(
            ["git", "-C", str(self.root), "check-ignore", "-q", "--", path],
            capture_output=True,
            env=git_env(),
        )
        return r.returncode == 0


def find_problems(
    name: str,
    text: str,
    targets: Set[str],
    exists: Callable[[str], bool],
) -> List[str]:
    """文書の中で見つからないターゲットとパスを「文書:行番号: …」の形ですべて返す。"""
    problems: List[str] = []
    for lineno, target in extract_make_targets(text):
        if target not in targets:
            problems.append(f"{name}:{lineno}: Makefile にないターゲット: make {target}")
    for lineno, path in extract_paths(text):
        if not exists(path):
            problems.append(f"{name}:{lineno}: リポジトリにないパス: {path}")
    return problems


def in_git_repo(root: Path) -> bool:
    if shutil.which("git") is None:
        return False
    r = subprocess.run(
        ["git", "-C", str(root), "rev-parse", "--is-inside-work-tree"],
        capture_output=True,
        text=True,
        env=git_env(),
    )
    return r.returncode == 0 and r.stdout.strip() == "true"


def targets_of(pairs: Iterable[Tuple[int, str]]) -> List[str]:
    return [t for _, t in pairs]


class ExtractMakeTargetsTest(unittest.TestCase):
    def test_inline_and_fence(self) -> None:
        text = "\n".join(
            [
                "`make test` で確かめる",
                "",
                "```bash",
                "make dev-env    # 説明",
                "DRY_RUN=1 make dev",
                "(cd web && make x)",
                "```",
            ]
        )
        self.assertEqual(
            extract_make_targets(text),
            [(1, "test"), (4, "dev-env"), (5, "dev"), (6, "x")],
        )

    def test_prose_is_ignored(self) -> None:
        self.assertEqual(extract_make_targets("GNU make 3.81 で動く"), [])

    def test_options_are_ignored(self) -> None:
        self.assertEqual(extract_make_targets("`make -n test`"), [])
        self.assertEqual(extract_make_targets("```\nmake -C dir test\n```\n"), [])

    def test_word_boundary(self) -> None:
        self.assertEqual(extract_make_targets("`cmake build`・`remake x`・`my-make y`"), [])


class ExtractPathsTest(unittest.TestCase):
    def test_paths(self) -> None:
        text = "\n".join(
            [
                "`server/internal/sim` 以下",
                "`scripts/raft/verify-patch.sh --with-tests` を実行",
                "状態は `web/.cloudflare/state` に置く",
                "`docs/adr/` を見る",
            ]
        )
        self.assertEqual(
            extract_paths(text),
            [
                (1, "server/internal/sim"),
                (2, "scripts/raft/verify-patch.sh"),
                (3, "web/.cloudflare/state"),
                (4, "docs/adr"),
            ],
        )

    def test_skipped(self) -> None:
        text = "\n".join(
            [
                "`data/stages/*.yaml`",
                '`"$PWD/…"`',
                "`.claude/agents/*.md`",
                "`agent/issue-<n>`",
                "`make test`",
                "`go -C server test ./...`",
            ]
        )
        self.assertEqual(extract_paths(text), [])

    def test_fence_is_ignored(self) -> None:
        self.assertEqual(extract_paths("```text\nserver/no-such\n```\n"), [])


class RepoPathsTest(unittest.TestCase):
    def setUp(self) -> None:
        if shutil.which("git") is None:
            self.skipTest("git がない")
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        subprocess.run(["git", "init", "-q", str(self.root)], check=True, env=git_env())
        (self.root / "a" / "b").mkdir(parents=True)
        (self.root / "a" / "b" / "c.txt").write_text("x\n", encoding="utf-8")
        (self.root / ".gitignore").write_text("ignored/\nlocal-state\n", encoding="utf-8")
        subprocess.run(["git", "-C", str(self.root), "add", "."], check=True, env=git_env())

    def tearDown(self) -> None:
        self._tmp.cleanup()

    def test_exists(self) -> None:
        repo = RepoPaths(self.root)
        self.assertTrue(repo.exists("a/b/c.txt"))  # 追跡されているファイル
        self.assertTrue(repo.exists("a/b"))  # ディレクトリの接頭辞
        self.assertTrue(repo.exists("a"))
        self.assertTrue(repo.exists("ignored/state"))  # .gitignore の対象
        self.assertTrue(repo.exists("local-state"))
        self.assertFalse(repo.exists("a/b/c"))  # 接頭辞でも「/」の区切りでなければない
        self.assertFalse(repo.exists("nope"))
        self.assertFalse(repo.exists("a/nope.txt"))


class RealDocsTest(unittest.TestCase):
    def setUp(self) -> None:
        if not in_git_repo(REPO_ROOT):
            self.skipTest("git がないか、リポジトリの中でない")
        self.repo = RepoPaths(REPO_ROOT)
        self.targets = makefile_targets((REPO_ROOT / "Makefile").read_text(encoding="utf-8"))

    def read(self, name: str) -> str:
        return (REPO_ROOT / name).read_text(encoding="utf-8")

    def test_docs_match_repository(self) -> None:
        problems: List[str] = []
        for name in DOCS:
            problems += find_problems(name, self.read(name), self.targets, self.repo.exists)
        self.assertEqual(problems, [], "\n" + "\n".join(problems))

    def test_docs_mention_something(self) -> None:
        # 抽出が壊れて何も集めなくなったときに、上のテストが素通りしないようにする
        text = self.read("CLAUDE.md")
        self.assertIn("test", targets_of(extract_make_targets(text)))
        self.assertIn("server/third_party/etcd-raft", targets_of(extract_paths(text)))

    def test_broken_doc_is_reported(self) -> None:
        text = self.read("CLAUDE.md") + "\n`make no-such-target`\n\n`scripts/no-such.sh`\n"
        problems = find_problems("CLAUDE.md", text, self.targets, self.repo.exists)
        self.assertEqual(len(problems), 2, "\n".join(problems))
        self.assertTrue(any("make no-such-target" in p for p in problems), problems)
        self.assertTrue(any("scripts/no-such.sh" in p for p in problems), problems)
        n = len(text.splitlines())
        self.assertIn(f"CLAUDE.md:{n - 2}:", problems[0])
        self.assertIn(f"CLAUDE.md:{n}:", problems[1])


if __name__ == "__main__":
    unittest.main()
