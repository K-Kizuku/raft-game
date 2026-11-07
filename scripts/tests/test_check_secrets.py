"""scripts/check-secrets.sh のテスト（T-009 / Issue #17）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_*.py'

本物の gitleaks・go・ネットワークを使わない。一時ディレクトリに git のリポジトリを作って
スクリプトと設定を写し（浅いクローンの CI でも動くように）、偽の gitleaks を PATH の先頭に置く。
偽の gitleaks は、最後の引数がリポジトリのルート（scripts/check-secrets.sh がある）なら本物の検査、
そうでなければ自己テストとして扱い、環境変数で決めた終了コードを返して引数をファイルに書く。
"""

import os
import re
import shutil
import stat
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path
from typing import List, Optional

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
SCRIPT = REPO_ROOT / "scripts" / "check-secrets.sh"
CONFIG = REPO_ROOT / ".gitleaks.toml"
IGNORE = REPO_ROOT / ".gitleaksignore"

GIT = shutil.which("git")
MAKE = shutil.which("make")

FINGERPRINT_RE = re.compile(r"^[0-9a-f]{40}:[^:]+:[a-z0-9-]+:[0-9]+$")
VERSION_LINE_RE = re.compile(r'^GITLEAKS_VERSION="v8\.[0-9]+\.[0-9]+"$', re.MULTILINE)
TOKEN_RE = re.compile(r"ghp_[0-9A-Za-z]{36}")

# 子のプロセスに引き継がない環境変数。
DROP_KEYS = ("MAKEFLAGS", "MAKELEVEL", "MFLAGS", "MAKEOVERRIDES", "DEV_SLOT", "DOCKER", "DRY_RUN", "GOTOOLCHAIN")

FAKE_GITLEAKS = textwrap.dedent(
    """\
    #!/bin/bash
    # 偽の gitleaks（scripts/tests/test_check_secrets.py）。
    if [ "$1" = "version" ]; then
      echo "$FAKE_GITLEAKS_VERSION"
      exit 0
    fi
    last="${!#}"
    if [ -f "$last/scripts/check-secrets.sh" ]; then
      printf '%s\\n' "$@" >"$FAKE_LOG_DIR/real.args"
      echo "fake gitleaks: real scan"
      exit "${FAKE_REAL_EXIT:-0}"
    fi
    printf '%s\\n' "$@" >"$FAKE_LOG_DIR/self.args"
    git -C "$last" show HEAD:selftest.txt >"$FAKE_LOG_DIR/self.content" 2>&1
    echo "fake gitleaks: self-test"
    exit "${FAKE_SELF_EXIT:-1}"
    """
)


def git(cwd: Path, *args: str) -> str:
    assert GIT is not None
    return subprocess.run(
        [
            GIT,
            "-C",
            str(cwd),
            "-c",
            "user.name=test",
            "-c",
            "user.email=test@example.invalid",
            "-c",
            "commit.gpgsign=false",
            "-c",
            "core.hooksPath=/dev/null",
            "-c",
            "init.defaultBranch=main",
            *args,
        ],
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
    ).stdout.strip()


@unittest.skipIf(GIT is None, "git がない")
class CheckSecretsTest(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.tmp = Path(self._tmp.name).resolve()
        self.logs = self.tmp / "logs"
        self.logs.mkdir()
        self.bin = self.tmp / "bin"
        self.bin.mkdir()
        fake = self.bin / "gitleaks"
        fake.write_text(FAKE_GITLEAKS, encoding="utf-8")
        fake.chmod(fake.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
        # go は PATH から隠し、git だけを個別に置く。
        assert GIT is not None
        (self.bin / "git").symlink_to(GIT)
        self.repo = self.make_repo()

    def tearDown(self) -> None:
        self._tmp.cleanup()

    def make_repo(self, ignore: str = "# comment\n") -> Path:
        repo = self.tmp / "repo"
        (repo / "scripts").mkdir(parents=True)
        shutil.copy2(SCRIPT, repo / "scripts" / "check-secrets.sh")
        shutil.copy2(CONFIG, repo / ".gitleaks.toml")
        (repo / ".gitleaksignore").write_text(ignore, encoding="utf-8")
        git(repo, "init", "-q")
        git(repo, "add", "-A")
        git(repo, "commit", "-q", "-m", "init")
        return Path(git(repo, "rev-parse", "--show-toplevel"))

    def run_script(
        self,
        *,
        version: str = "8.30.1",
        self_exit: int = 1,
        real_exit: int = 0,
        script: Optional[Path] = None,
    ) -> subprocess.CompletedProcess:
        env = {k: v for k, v in os.environ.items() if k not in DROP_KEYS}
        env.update(
            PATH=f"{self.bin}:/usr/bin:/bin",
            FAKE_GITLEAKS_VERSION=version,
            FAKE_SELF_EXIT=str(self_exit),
            FAKE_REAL_EXIT=str(real_exit),
            FAKE_LOG_DIR=str(self.logs),
            GIT_CEILING_DIRECTORIES=str(self.tmp),
        )
        return subprocess.run(
            ["bash", str(script or self.repo / "scripts" / "check-secrets.sh")],
            cwd=self.tmp,
            env=env,
            capture_output=True,
            text=True,
            encoding="utf-8",
            timeout=60,
        )

    def args_of(self, name: str) -> List[str]:
        path = self.logs / f"{name}.args"
        self.assertTrue(path.is_file(), f"{name} の呼び出しがない")
        return path.read_text(encoding="utf-8").splitlines()

    def set_ignore(self, text: str) -> None:
        (self.repo / ".gitleaksignore").write_text(text, encoding="utf-8")
        git(self.repo, "add", "-A")
        git(self.repo, "commit", "-q", "-m", "ignore")

    def script_version(self) -> str:
        m = VERSION_LINE_RE.search(SCRIPT.read_text(encoding="utf-8"))
        assert m is not None
        return m.group(0).split('"')[1].lstrip("v")

    # --- 動作 ---------------------------------------------------------------
    def test_pass(self) -> None:
        res = self.run_script(version=self.script_version())
        out = res.stdout + res.stderr
        self.assertEqual(res.returncode, 0, out)
        real = self.args_of("real")
        self.assertEqual(real[0], "git")
        self.assertIn("--redact", real)
        self.assertIn("--log-opts=HEAD", real)
        self.assertIn(f"--config\n{self.repo}/.gitleaks.toml", "\n".join(real))
        self.assertIn(f"--gitleaks-ignore-path\n{self.repo}/.gitleaksignore", "\n".join(real))
        self.assertEqual(real[-1], str(self.repo))
        self.assertNotIn("--report-path", real)
        # 自己テストは同じ設定を使い、偽のトークンをコミットしたリポジトリを検査する。
        selftest = self.args_of("self")
        self.assertIn(f"--config\n{self.repo}/.gitleaks.toml", "\n".join(selftest))
        self.assertIn("--redact", selftest)
        content = (self.logs / "self.content").read_text(encoding="utf-8")
        self.assertRegex(content, TOKEN_RE)

    def test_version_with_v_prefix(self) -> None:
        res = self.run_script(version="v" + self.script_version())
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)

    def test_selftest_not_detected(self) -> None:
        res = self.run_script(version=self.script_version(), self_exit=0)
        out = res.stdout + res.stderr
        self.assertEqual(res.returncode, 1, out)
        self.assertIn("自己テスト", out)
        self.assertFalse((self.logs / "real.args").exists(), "自己テストの失敗の後に本物の検査をした")

    def test_found(self) -> None:
        res = self.run_script(version=self.script_version(), real_exit=1)
        out = res.stdout + res.stderr
        self.assertEqual(res.returncode, 1, out)
        self.assertIn("gitleaks:allow", out)
        self.assertIn(".gitleaksignore に足してよいのは main にあるコミットの指紋だけ", out)

    def test_wrong_version_without_go(self) -> None:
        if shutil.which("go", path="/usr/bin:/bin"):
            self.skipTest("/usr/bin か /bin に go がある")
        res = self.run_script(version="8.0.0")
        out = res.stdout + res.stderr
        self.assertEqual(res.returncode, 2, out)
        self.assertIn("go がない", out)
        self.assertIn("固定の版", out)
        self.assertFalse((self.logs / "real.args").exists())

    def test_outside_git(self) -> None:
        d = self.tmp / "nogit" / "scripts"
        d.mkdir(parents=True)
        shutil.copy2(SCRIPT, d / "check-secrets.sh")
        res = self.run_script(version=self.script_version(), script=d / "check-secrets.sh")
        self.assertEqual(res.returncode, 2, res.stdout + res.stderr)

    # --- .gitleaksignore の書式 ---------------------------------------------
    def test_ignore_valid_fingerprint(self) -> None:
        head = git(self.repo, "rev-parse", "HEAD")
        self.set_ignore(f"# a.ts: テストの偽のトークン\n\n{head}:a.ts:generic-api-key:4\n")
        res = self.run_script(version=self.script_version())
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)

    def test_ignore_bad_format(self) -> None:
        self.set_ignore("not-a-fingerprint\n")
        res = self.run_script(version=self.script_version())
        out = res.stdout + res.stderr
        self.assertEqual(res.returncode, 1, out)
        self.assertIn("not-a-fingerprint", out)
        self.assertFalse((self.logs / "self.args").exists())

    def test_ignore_commit_not_ancestor(self) -> None:
        line = "0123456789abcdef0123456789abcdef01234567:a.ts:generic-api-key:4"
        self.set_ignore(line + "\n")
        res = self.run_script(version=self.script_version())
        out = res.stdout + res.stderr
        self.assertEqual(res.returncode, 1, out)
        self.assertIn(line, out)

    def test_ignore_commit_not_on_origin_main(self) -> None:
        # origin/main があれば、HEAD にあっても origin/main にないコミットは認めない。
        base = git(self.repo, "rev-parse", "HEAD")
        git(self.repo, "update-ref", "refs/remotes/origin/main", base)
        git(self.repo, "commit", "-q", "--allow-empty", "-m", "branch")
        branch = git(self.repo, "rev-parse", "HEAD")
        self.set_ignore(f"{branch}:a.ts:generic-api-key:4\n")
        res = self.run_script(version=self.script_version())
        out = res.stdout + res.stderr
        self.assertEqual(res.returncode, 1, out)
        self.assertIn("origin/main", out)


class StaticTest(unittest.TestCase):
    def test_single_pinned_version(self) -> None:
        text = SCRIPT.read_text(encoding="utf-8")
        self.assertEqual(len(VERSION_LINE_RE.findall(text)), 1)
        self.assertEqual(len(re.findall(r"^GITLEAKS_VERSION=", text, re.MULTILINE)), 1)

    def test_no_literal_token(self) -> None:
        self.assertIsNone(TOKEN_RE.search(SCRIPT.read_text(encoding="utf-8")))

    def test_config_uses_default_rules_without_allowlist(self) -> None:
        text = CONFIG.read_text(encoding="utf-8")
        body = "\n".join(line for line in text.splitlines() if not line.lstrip().startswith("#"))
        self.assertIn("useDefault = true", body)
        self.assertNotIn("allowlist", body)
        self.assertNotIn("[[rules]]", body)

    def test_gitleaksignore_format(self) -> None:
        lines = [
            line
            for line in IGNORE.read_text(encoding="utf-8").splitlines()
            if line.strip() and not line.startswith("#")
        ]
        self.assertTrue(lines)
        for line in lines:
            with self.subTest(line=line):
                self.assertRegex(line, FINGERPRINT_RE)

    @unittest.skipIf(GIT is None, "git がない")
    def test_gitleaksignore_commits_on_main(self) -> None:
        shallow = subprocess.run(
            [GIT, "-C", str(REPO_ROOT), "rev-parse", "--is-shallow-repository"],
            capture_output=True,
            text=True,
        ).stdout.strip()
        if shallow != "false":
            self.skipTest("浅いクローン（または git のリポジトリの外）では祖先を確かめられない")
        has_origin = (
            subprocess.run(
                [GIT, "-C", str(REPO_ROOT), "rev-parse", "--verify", "--quiet", "refs/remotes/origin/main^{commit}"],
                capture_output=True,
            ).returncode
            == 0
        )
        base = "origin/main" if has_origin else "HEAD"
        for line in IGNORE.read_text(encoding="utf-8").splitlines():
            if not line.strip() or line.startswith("#"):
                continue
            commit = line.split(":", 1)[0]
            with self.subTest(commit=commit):
                res = subprocess.run(
                    [GIT, "-C", str(REPO_ROOT), "merge-base", "--is-ancestor", commit, base],
                    capture_output=True,
                )
                self.assertEqual(res.returncode, 0, f"{commit} が {base} の祖先でない")


def make(*args: str) -> subprocess.CompletedProcess:
    env = {k: v for k, v in os.environ.items() if k not in DROP_KEYS}
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
        res = make("-n", "check-secrets")
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("bash scripts/check-secrets.sh", res.stdout)

    def test_help_lists_target(self) -> None:
        res = make("help")
        self.assertEqual(res.returncode, 0, res.stderr)
        listed = {line.split()[0] for line in res.stdout.splitlines() if line.startswith("  ")}
        self.assertIn("check-secrets", listed)


if __name__ == "__main__":
    unittest.main()
