#!/usr/bin/env python3
"""Markdown の相対リンクの先が存在するかを確かめる（Issue #2 / AG-1）。

使い方:
    python3 scripts/check-docs.py [DIR]

DIR を省略したときはリポジトリのルート（このスクリプトの 1 つ上）を対象にする。
DIR が git のリポジトリなら `git ls-files '*.md'` の結果を、そうでなければ
DIR 以下の *.md をすべて対象にする。

壊れたリンクは `path:line: target` の形式で標準出力に出し、1 件でもあれば
終了コード 1 で終わる。標準ライブラリだけを使う（Python 3.9 以上）。
"""

import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Iterator, List, Tuple
from urllib.parse import unquote

# 対象外にするリンクの先頭（大文字・小文字を区別しない）
SKIP_PREFIXES = ("http://", "https://", "mailto:")

# コードフェンスの開始・終了（行頭の空白は 3 個まで）
FENCE_RE = re.compile(r"^ {0,3}(`{3,}|~{3,})")

# インラインコード（同じ数のバッククォートで閉じる）
INLINE_CODE_RE = re.compile(r"(`+)(.+?)\1")

# [text](target "title") / ![alt](target)。
# text の中は 1 段までの角かっこの入れ子を許す。target は <...> か、
# 空白を含まない文字列（1 段までの丸かっこの入れ子を許す）。
LINK_RE = re.compile(
    r"\[(?:[^\[\]\\]|\\.|\[[^\[\]]*\])*\]"
    r"\(\s*"
    r"(?P<target><[^<>\n]*>|(?:[^()\s\\]|\\.|\([^()\s]*\))*)"
    r"(?:\s+(?:\"[^\"]*\"|'[^']*'|\([^()]*\)))?"
    r"\s*\)"
)


def list_markdown_files(root: Path) -> List[Path]:
    """root 以下の Markdown ファイルを返す（root からの相対パス）。"""
    try:
        out = subprocess.run(
            ["git", "-C", str(root), "ls-files", "-z", "--", "*.md"],
            check=True,
            capture_output=True,
        ).stdout
        return sorted(Path(p) for p in out.decode("utf-8").split("\0") if p)
    except (OSError, subprocess.CalledProcessError):
        pass
    files = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(d for d in dirnames if d != ".git")
        for name in filenames:
            if name.endswith(".md"):
                files.append(Path(dirpath, name).relative_to(root))
    return sorted(files)


def strip_inline_code(line: str) -> str:
    """インラインコードを同じ長さの空白に置き換える。"""
    return INLINE_CODE_RE.sub(lambda m: " " * len(m.group(0)), line)


def iter_links(text: str) -> Iterator[Tuple[int, str]]:
    """(行番号, リンク先) を返す。コードブロックとインラインコードの中は除く。"""
    fence = None  # 開いているフェンスの文字列（例: "```"）
    for lineno, line in enumerate(text.splitlines(), start=1):
        m = FENCE_RE.match(line)
        if fence is None:
            if m:
                fence = m.group(1)
                continue
        else:
            if m and m.group(1)[0] == fence[0] and len(m.group(1)) >= len(fence) \
                    and line.strip() == m.group(1):
                fence = None
            continue
        for lm in LINK_RE.finditer(strip_inline_code(line)):
            target = lm.group("target")
            if target.startswith("<") and target.endswith(">"):
                target = target[1:-1]
            yield lineno, target


def is_checked(target: str) -> bool:
    if not target or target.startswith("#"):
        return False
    return not target.lower().startswith(SKIP_PREFIXES)


def resolve(root: Path, md_path: Path, target: str) -> Path:
    path_part = target.split("#", 1)[0].split("?", 1)[0]
    path_part = unquote(re.sub(r"\\(.)", r"\1", path_part))
    if path_part.startswith("/"):
        return root / path_part.lstrip("/")
    return (root / md_path).parent / path_part


def check(root: Path) -> List[str]:
    """壊れたリンクを `path:line: target` の形式で返す。"""
    errors = []
    for md_path in list_markdown_files(root):
        full = root / md_path
        try:
            text = full.read_text(encoding="utf-8")
        except FileNotFoundError:
            # git の索引にはあるが作業ツリーで消されたファイル
            continue
        for lineno, target in iter_links(text):
            if not is_checked(target):
                continue
            if not resolve(root, md_path, target).exists():
                errors.append(f"{md_path.as_posix()}:{lineno}: {target}")
    return errors


def main(argv: List[str]) -> int:
    if len(argv) > 2:
        print("usage: check-docs.py [DIR]", file=sys.stderr)
        return 2
    root = Path(argv[1]) if len(argv) == 2 else Path(__file__).resolve().parent.parent
    if not root.is_dir():
        print(f"check-docs: not a directory: {root}", file=sys.stderr)
        return 2
    errors = check(root)
    for e in errors:
        print(e)
    if errors:
        print(f"check-docs: {len(errors)} broken link(s)", file=sys.stderr)
        return 1
    print("check-docs: OK", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
