"""本番の運用の手順（docs/runbooks/*.md）の検査（T-604 / Issue #68、RB-1〜RB-5）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_runbooks.py'

- RB-1・RB-2: 手順のコードブロックの中の `cf` のコマンドとフラグが、入っている `cf`
  （web/node_modules/cf/dist/_meta/ の commands.json・hand-written-commands.json）にあるか。
  web/node_modules がなければ skip する（CI の docs ジョブ）
- RB-3: コマンドの取り出しの自己テスト（常に実行）
- RB-4: docs/runbooks/README.md から deploy-rollback.md・cf-upgrade.md へ辿れるか（常に実行）
- RB-5: deploy.yml のデプロイの行が `--message` に `$BUILD_ID` を渡しているか（手順が頼る T-603 の約束）

標準ライブラリだけを使う。
"""

import json
import re
import unittest
from pathlib import Path
from typing import Dict, FrozenSet, Iterable, List, Mapping, NamedTuple, Optional, Tuple

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
RUNBOOKS_DIR = REPO_ROOT / "docs" / "runbooks"
CF_META_DIR = REPO_ROOT / "web" / "node_modules" / "cf" / "dist" / "_meta"
CF_META_FILES = ("commands.json", "hand-written-commands.json")
DEPLOY_WORKFLOW = REPO_ROOT / ".github" / "workflow-sources" / "deploy.yml"

# どのコマンドにも使えるフラグ（cf のグローバルなフラグ）
GLOBAL_FLAGS: FrozenSet[str] = frozenset(
    {"zone", "z", "quiet", "q", "profile", "mode", "m", "local", "persist-to", "help", "h", "version", "v"}
)

WORD = re.compile(r"^[a-z][a-z0-9-]*$")
# `cf ` の直前に許すもの: 行頭・空白（`exec ` を含む）・`&&`・`;`・`|`・`$(`
CF_START = re.compile(r"(?:^|(?<=\s)|(?<=&&)|(?<=;)|(?<=\|)|(?<=\$\())cf(?= )")
# 1 つの呼び出しの終わり
SEPARATOR = re.compile(r"&&|\|\||[;|)]")
LONG_FLAG = re.compile(r"(?<!\S)--([a-z][a-z0-9-]*)")
SHORT_FLAG = re.compile(r"(?<!\S)-([a-z])(?=\s|=|$)")


class CfCall(NamedTuple):
    line: int  # 1 始まり（継続行は最初の行）
    words: Tuple[str, ...]  # `cf` の後ろの小文字の語
    command: Optional[str]  # 既知のコマンド（語のないときは ""、知らないときは None）
    flags: Tuple[str, ...]  # `--` と `-` を除いた名前


def load_known_commands(meta_dir: Path) -> Dict[str, FrozenSet[str]]:
    """meta_dir の commands.json・hand-written-commands.json から {コマンド: フラグの集合} を作る。

    コマンドは先頭の `cf ` を除いたもの（例: "workers deployments list"）。フラグは options[].name と
    その alias・aliases。コマンドの aliases（最後の語の別名）も同じフラグで登録する。
    """
    known: Dict[str, set] = {}
    for name in CF_META_FILES:
        data = json.loads((meta_dir / name).read_text(encoding="utf-8"))
        for cmd in data.get("commands", []):
            words = cmd["command"].split()
            if words and words[0] == "cf":
                words = words[1:]
            flags = set()
            for opt in cmd.get("options", []) or []:
                flags.add(opt["name"])
                flags.update(_as_list(opt.get("alias")))
                flags.update(_as_list(opt.get("aliases")))
            names = [" ".join(words)]
            if words:
                names += [" ".join(words[:-1] + [a]) for a in _as_list(cmd.get("aliases"))]
            for n in names:
                known.setdefault(n, set()).update(flags)
    return {k: frozenset(v) for k, v in known.items()}


def _as_list(value) -> List[str]:
    if value is None:
        return []
    if isinstance(value, str):
        return [value]
    return [v for v in value if isinstance(v, str)]


def _strip_comment(line: str) -> str:
    """引用符の外の `#`（行頭か空白の後）から後ろを除く"""
    quote = None
    for i, ch in enumerate(line):
        if quote:
            if ch == quote:
                quote = None
        elif ch in ("'", '"'):
            quote = ch
        elif ch == "#" and (i == 0 or line[i - 1].isspace()):
            return line[:i]
    return line


def _mask_single_quoted(line: str) -> str:
    """一重引用符の中身を 'x' に置き換える（JSON の本文の中の語をフラグや区切りと見なさない）"""
    return re.sub(r"'[^']*'", lambda m: "'" + "x" * (len(m.group(0)) - 2) + "'", line)


def _code_lines(text: str) -> Iterable[Tuple[int, str]]:
    """フェンスのコードブロックの中の行を (行番号, 行) で返す。`\\` で終わる行は次の行とつなぐ"""
    in_block = False
    pending: Optional[Tuple[int, str]] = None
    for no, line in enumerate(text.splitlines(), start=1):
        if line.lstrip().startswith("```"):
            if pending:
                yield pending
                pending = None
            in_block = not in_block
            continue
        if not in_block:
            continue
        if pending:
            start, acc = pending
            pending = None
            line_no, line = start, acc + " " + line
        else:
            line_no = no
        stripped = line.rstrip()
        if stripped.endswith("\\"):
            pending = (line_no, stripped[:-1])
            continue
        yield line_no, line
    if pending:
        yield pending


def _match_command(words: Tuple[str, ...], known: Mapping[str, FrozenSet[str]]) -> Optional[str]:
    if not words:
        return ""
    for n in range(len(words), 0, -1):
        cand = " ".join(words[:n])
        if cand in known:
            return cand
    return None


def extract_cf_calls(text: str, known: Mapping[str, FrozenSet[str]]) -> List[CfCall]:
    """Markdown の本文から、コードブロックの中の `cf` の呼び出しを取り出す"""
    calls: List[CfCall] = []
    for line_no, raw in _code_lines(text):
        line = _mask_single_quoted(_strip_comment(raw))
        for m in CF_START.finditer(line):
            rest = line[m.end():]
            sep = SEPARATOR.search(rest)
            segment = rest[: sep.start()] if sep else rest
            words: List[str] = []
            for tok in segment.split():
                if not WORD.match(tok):
                    break
                words.append(tok)
            flags = LONG_FLAG.findall(segment) + SHORT_FLAG.findall(segment)
            wt = tuple(words)
            calls.append(CfCall(line_no, wt, _match_command(wt, known), tuple(flags)))
    return calls


def runbook_files() -> List[Path]:
    return sorted(RUNBOOKS_DIR.glob("*.md"))


def find_unknown_commands(known: Mapping[str, FrozenSet[str]]) -> List[str]:
    out = []
    for path in runbook_files():
        for c in extract_cf_calls(path.read_text(encoding="utf-8"), known):
            if c.command is None:
                out.append(f"{path.relative_to(REPO_ROOT)}:{c.line}: cf {' '.join(c.words)}")
    return out


def find_unknown_flags(known: Mapping[str, FrozenSet[str]]) -> List[str]:
    out = []
    for path in runbook_files():
        for c in extract_cf_calls(path.read_text(encoding="utf-8"), known):
            if c.command is None:
                continue  # RB-1 が扱う
            allowed = known.get(c.command, frozenset()) | GLOBAL_FLAGS
            for f in c.flags:
                if f not in allowed:
                    dash = "-" if len(f) == 1 else "--"
                    out.append(f"{path.relative_to(REPO_ROOT)}:{c.line}: cf {c.command} {dash}{f}".replace("  ", " "))
    return out


def deploy_lines(workflow_text: str) -> List[str]:
    """コメントの行を除き、`cf deploy --prebuilt` を含み `--dry-run` を含まない行"""
    out = []
    for line in workflow_text.splitlines():
        if line.strip().startswith("#"):
            continue
        if "cf deploy --prebuilt" in line and "--dry-run" not in line:
            out.append(line)
    return out


def _skip_without_cf_meta(test: unittest.TestCase) -> Dict[str, FrozenSet[str]]:
    if not (CF_META_DIR / "commands.json").is_file():
        test.skipTest(f"{CF_META_DIR.relative_to(REPO_ROOT)}/commands.json がない（web で pnpm install を実行する）")
    return load_known_commands(CF_META_DIR)


class RunbookCfCommandsTest(unittest.TestCase):
    def test_rb1_commands_exist(self) -> None:
        """RB-1: 手順の中の cf のコマンドが、入っている cf にある"""
        known = _skip_without_cf_meta(self)
        self.assertTrue(runbook_files(), "docs/runbooks/*.md がない")
        unknown = find_unknown_commands(known)
        self.assertEqual(unknown, [], "今の cf にないコマンド:\n" + "\n".join(unknown))

    def test_rb2_flags_exist(self) -> None:
        """RB-2: 手順の中の cf のフラグが、そのコマンドかグローバルのもの"""
        known = _skip_without_cf_meta(self)
        unknown = find_unknown_flags(known)
        self.assertEqual(unknown, [], "そのコマンドにないフラグ:\n" + "\n".join(unknown))


SAMPLE_KNOWN: Dict[str, FrozenSet[str]] = {
    "deploy": frozenset({"prebuilt", "dry-run", "message"}),
    "workers deployments list": frozenset({"worker", "dry-run"}),
    "workers deployments create": frozenset({"worker", "body", "dry-run"}),
    "workers versions get": frozenset({"worker-id"}),
    "auth scopes": frozenset({"filter"}),
}


def _md(*code_lines: str, before: str = "", after: str = "") -> str:
    return "\n".join([before, "```bash", *code_lines, "```", after])


class ExtractTest(unittest.TestCase):
    """RB-3: 取り出しの関数の自己テスト"""

    def test_command_and_flag_comment_removed(self) -> None:
        calls = extract_cf_calls(_md("pnpm exec cf workers deployments list --worker raft-game # 一覧 --quiet"), SAMPLE_KNOWN)
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0].command, "workers deployments list")
        self.assertEqual(calls[0].flags, ("worker",))
        self.assertEqual(calls[0].line, 3)

    def test_two_calls_on_one_line(self) -> None:
        calls = extract_cf_calls(_md("pnpm -C web exec cf --version && pnpm -C spike exec cf --version"), SAMPLE_KNOWN)
        self.assertEqual(len(calls), 2)
        for c in calls:
            self.assertEqual(c.words, ())
            self.assertEqual(c.command, "")
            self.assertEqual(c.flags, ("version",))

    def test_outside_code_block_is_ignored(self) -> None:
        text = _md("ls", before="説明の中の cf deploy --prebuilt は対象外", after="cf deploy --bogus も対象外")
        self.assertEqual(extract_cf_calls(text, SAMPLE_KNOWN), [])

    def test_not_a_cf_word(self) -> None:
        text = _md("echo cf", "scf deploy --x", "pnpm -C web add -D -E cf@1.0.0", "cd web/cf")
        self.assertEqual(extract_cf_calls(text, SAMPLE_KNOWN), [])

    def test_longest_known_command_and_positional_args(self) -> None:
        calls = extract_cf_calls(_md("pnpm exec cf auth scopes billing", "cf workers versions get <ID> --worker-id raft-game"), SAMPLE_KNOWN)
        self.assertEqual([c.command for c in calls], ["auth scopes", "workers versions get"])
        self.assertEqual(calls[1].flags, ("worker-id",))

    def test_unknown_command(self) -> None:
        calls = extract_cf_calls(_md("pnpm exec cf workers rollback --worker raft-game"), SAMPLE_KNOWN)
        self.assertEqual(len(calls), 1)
        self.assertIsNone(calls[0].command)
        self.assertEqual(calls[0].words, ("workers", "rollback"))

    def test_quoted_body_and_continuation(self) -> None:
        calls = extract_cf_calls(
            _md(
                "pnpm exec cf workers deployments create --worker raft-game --dry-run \\",
                "  --body '{\"a\":\"x --not-a-flag; y | z\"}' -q",
            ),
            SAMPLE_KNOWN,
        )
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0].line, 3)
        self.assertEqual(calls[0].flags, ("worker", "dry-run", "body", "q"))

    def test_after_pipe_and_command_substitution(self) -> None:
        calls = extract_cf_calls(_md("x=$(cf deploy --prebuilt) | cf deploy --message m"), SAMPLE_KNOWN)
        self.assertEqual([c.flags for c in calls], [("prebuilt",), ("message",)])

    def test_load_known_commands(self) -> None:
        import tempfile

        with tempfile.TemporaryDirectory() as d:
            p = Path(d)
            (p / "commands.json").write_text(
                json.dumps({"commands": [{"command": "cf workers versions list", "options": [{"name": "worker-id", "alias": "w"}]}]}),
                encoding="utf-8",
            )
            (p / "hand-written-commands.json").write_text(
                json.dumps({"commands": [{"command": "cf access tcp", "aliases": ["ssh"], "options": [{"name": "url", "aliases": ["u"]}]}]}),
                encoding="utf-8",
            )
            known = load_known_commands(p)
        self.assertEqual(known["workers versions list"], frozenset({"worker-id", "w"}))
        self.assertEqual(known["access tcp"], frozenset({"url", "u"}))
        self.assertEqual(known["access ssh"], frozenset({"url", "u"}))


class RunbookIndexTest(unittest.TestCase):
    def test_rb4_index_links(self) -> None:
        """RB-4: deploy-rollback.md・cf-upgrade.md があり、README.md から両方へリンクしている"""
        readme = RUNBOOKS_DIR / "README.md"
        self.assertTrue(readme.is_file(), "docs/runbooks/README.md がない")
        text = readme.read_text(encoding="utf-8")
        for name in ("deploy-rollback.md", "cf-upgrade.md"):
            self.assertTrue((RUNBOOKS_DIR / name).is_file(), f"docs/runbooks/{name} がない")
            self.assertRegex(text, r"\]\((\./)?" + re.escape(name) + r"(#[^)]*)?\)", f"README.md から {name} へのリンクがない")


class DeployWorkflowContractTest(unittest.TestCase):
    def test_rb5_deploy_message_has_build_id(self) -> None:
        """RB-5: deploy.yml のデプロイの行がちょうど 1 つで、--message に $BUILD_ID を渡す"""
        if not DEPLOY_WORKFLOW.is_file():
            self.skipTest(".github/workflow-sources/deploy.yml がない")
        lines = deploy_lines(DEPLOY_WORKFLOW.read_text(encoding="utf-8"))
        self.assertEqual(len(lines), 1, f"cf deploy --prebuilt（--dry-run なし）の行が 1 つでない: {lines}")
        self.assertIn("--message", lines[0])
        self.assertIn("$BUILD_ID", lines[0])

    def test_deploy_lines_ignores_comments_and_dry_run(self) -> None:
        text = "\n".join(
            [
                "# cf deploy --prebuilt --message x",
                "  run: pnpm -C web exec cf deploy --prebuilt --dry-run --mode \"$MODE\"",
                "  run: pnpm -C web exec cf deploy --prebuilt --mode \"$MODE\" --message \"$BUILD_ID run $GITHUB_RUN_ID\"",
            ]
        )
        self.assertEqual(len(deploy_lines(text)), 1)


if __name__ == "__main__":
    unittest.main()
