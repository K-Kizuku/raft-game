"""scripts/demo-local.sh のテスト（T-504 / Issue #64）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_demo_local.py'

ネットワーク・本物の Go・pnpm を使わない（偽のコマンドを PATH の先頭に置く）。
作業ツリーは一時ディレクトリの中に作り、--path で渡す。実際に起動するテストは、
そのときに 2 つのポートが空いている DEV_SLOT を 60〜99 から選ぶ。
"""

import os
import random
import signal
import socket
import stat
import subprocess
import tempfile
import textwrap
import time
import unittest
from pathlib import Path
from typing import Optional

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
SCRIPT = REPO_ROOT / "scripts" / "demo-local.sh"

# 子のプロセスに引き継がない環境変数（DEV_ で始まるものも落とす）。
DROP_KEYS = (
    "MAKEFLAGS",
    "MAKELEVEL",
    "MFLAGS",
    "MAKEOVERRIDES",
    "DRY_RUN",
    "CHECK",
    "DEMO_DATA_DIR",
    "GOPROXY",
    "GOFLAGS",
    "VITE_TURNSTILE_SITE_KEY",
)


def clean_env(**extra: str) -> dict:
    env = {
        k: v
        for k, v in os.environ.items()
        if k not in DROP_KEYS and not k.startswith("DEV_") and not k.startswith("FAKE_")
    }
    env.update(extra)
    return env


def run(args, *, env=None, cwd=None, timeout: int = 60) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["bash", str(SCRIPT), *args],
        cwd=cwd or REPO_ROOT,
        env=env if env is not None else clean_env(),
        capture_output=True,
        text=True,
        encoding="utf-8",
        timeout=timeout,
    )


def plus_lines(stdout: str) -> list:
    return [line for line in stdout.splitlines() if line.startswith("+ ")]


def port_open(port: int) -> bool:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.settimeout(0.2)
        return s.connect_ex(("127.0.0.1", port)) == 0


def free_slot() -> str:
    """Go と Vite の 2 つのポートが空いている DEV_SLOT を 60〜99 から選ぶ（ポートの式は dev-env.sh と同じ）。"""
    slots = list(range(60, 100))
    random.shuffle(slots)
    for n in slots:
        if not port_open(18080 + n * 10) and not port_open(15173 + n * 10):
            return str(n)
    raise unittest.SkipTest("60〜99 に空いている DEV_SLOT がない")


def write_exe(path: Path, text: str) -> None:
    path.write_text(text, encoding="utf-8")
    path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


def alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    return True


def read_env(path: Path) -> dict:
    out = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if "=" in line:
            k, v = line.split("=", 1)
            out[k] = v
    return out


FAKE_SERVER = textwrap.dedent(
    """\
    #!/usr/bin/env bash
    # 偽物のサーバー。受け取った環境変数と PID を書いて眠り続ける（FAKE_SERVER_RC があればすぐ終わる）。
    env >"$FAKE_DIR/server.env"
    echo "$$" >"$FAKE_DIR/server"
    if [ -n "${FAKE_SERVER_RC:-}" ]; then exit "$FAKE_SERVER_RC"; fi
    exec sleep 300
    """
)

FAKE_GO = textwrap.dedent(
    """\
    #!/usr/bin/env bash
    # 偽物の go。受け取った環境変数と引数を書き、build -o <out> に偽物のサーバーを置く。
    env >"$FAKE_DIR/go.env"
    printf '%s\\n' "$@" >"$FAKE_DIR/go.args"
    if [ -n "${FAKE_GO_RC:-}" ]; then exit "$FAKE_GO_RC"; fi
    out=""
    while [ $# -gt 0 ]; do
      if [ "$1" = -o ]; then out="$2"; shift; fi
      shift
    done
    cp "$FAKE_DIR/fake-server" "$out"
    chmod +x "$out"
    """
)

FAKE_PNPM = textwrap.dedent(
    """\
    #!/usr/bin/env bash
    # 偽物の pnpm。受け取った環境変数・引数と PID を書いて眠り続ける（exec なので PID は変わらない）。
    env >"$FAKE_DIR/pnpm.env"
    printf '%s\\n' "$@" >"$FAKE_DIR/pnpm.args"
    echo "$$" >"$FAKE_DIR/pnpm"
    if [ -n "${FAKE_PNPM_WRAP:-}" ]; then
      # 本物の pnpm の sh のラッパーのように、exec せずに孫（vite の代わり）を起動して待つ。
      sleep 300 &
      echo "$!" >"$FAKE_DIR/grandchild"
      wait
      exit 0
    fi
    exec sleep 300
    """
)

FAKE_CURL = textwrap.dedent(
    """\
    #!/usr/bin/env bash
    # 偽物の curl。FAKE_CURL_RC（既定は 0）で終わる。
    exit "${FAKE_CURL_RC:-0}"
    """
)


class TempTree(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.tmp = Path(self._tmp.name).resolve()

    def tearDown(self) -> None:
        self._tmp.cleanup()

    def make_tree(self, name: str = "wt") -> Path:
        wt = self.tmp / name
        (wt / "server" / "cmd" / "server").mkdir(parents=True)
        (wt / "server" / "cmd" / "server" / "main.go").write_text("package main\n", encoding="utf-8")
        (wt / "web").mkdir()
        (wt / "web" / "package.json").write_text("{}", encoding="utf-8")
        self.make_data(wt / "data")
        return wt

    @staticmethod
    def make_data(d: Path) -> None:
        (d / "stages").mkdir(parents=True)
        (d / "islands.json").write_text("{}", encoding="utf-8")
        (d / "stages" / "a.yaml").write_text("id: a\n", encoding="utf-8")

    def make_fakes(self, wt: Path) -> tuple:
        """偽の go・pnpm・curl を置き、web/node_modules/.bin/vite を作る。(bindir, fakedir) を返す。"""
        bindir = self.tmp / "bin"
        bindir.mkdir()
        write_exe(bindir / "go", FAKE_GO)
        write_exe(bindir / "pnpm", FAKE_PNPM)
        write_exe(bindir / "curl", FAKE_CURL)
        fakedir = self.tmp / "fake"
        fakedir.mkdir()
        write_exe(fakedir / "fake-server", FAKE_SERVER)
        vite = wt / "web" / "node_modules" / ".bin" / "vite"
        vite.parent.mkdir(parents=True)
        write_exe(vite, "#!/bin/sh\nexit 0\n")
        return bindir, fakedir

    def fake_env(self, bindir: Path, fakedir: Path, slot: str, **extra: str) -> dict:
        return clean_env(DEV_SLOT=slot, PATH=f"{bindir}:/usr/bin:/bin", FAKE_DIR=str(fakedir), **extra)


class DryRunTest(TempTree):
    # DL-1
    def test_default_slot(self) -> None:
        wt = self.make_tree()
        res = run(["--dry-run", "--path", str(wt)])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("DEV_SLOT が未設定なので 0 を使う", res.stdout)
        self.assertIn("DEV_SERVER_PORT=18080 DEV_WEB_PORT=15173", res.stdout)
        lines = plus_lines(res.stdout)
        self.assertEqual(len(lines), 6, res.stdout)
        self.assertEqual(lines[0], "+ mktemp -d")
        self.assertEqual(lines[1], f"+ go -C {wt}/server build -o <tmp>/server ./cmd/server")
        go = lines[2].split()
        for want in [
            "MODE=standalone",
            "PORT=18080",
            f"DATA_DIR={wt}/data",
            "ALLOWED_ORIGINS=http://127.0.0.1:15173,http://localhost:15173",
            "DEMO_CONTROLS=1",
            "<tmp>/server",
            "&",
        ]:
            self.assertIn(want, go)
        self.assertEqual(lines[3], "+ curl -fsS -o /dev/null --max-time 1 http://127.0.0.1:18080/api/healthz")
        self.assertEqual(
            lines[4],
            "+ DEV_BACKEND=standalone DEV_SERVER_PORT=18080 VITE_TURNSTILE_SITE_KEY= "
            f"pnpm -C {wt}/web exec vite --host 127.0.0.1 --port 15173 --strictPort &",
        )
        self.assertEqual(lines[5], "+ curl -fsS -o /dev/null --max-time 2 http://127.0.0.1:15173/")
        # 1 行目の表示が「+ 」の行より前にある。
        out = res.stdout.splitlines()
        head = [i for i, l in enumerate(out) if l.startswith("demo-local: DEV_SLOT=0 ")]
        self.assertEqual(len(head), 1, res.stdout)
        self.assertLess(head[0], out.index(lines[0]))

    # DL-2
    def test_slot_3(self) -> None:
        wt = self.make_tree()
        res = run(["--dry-run", "--path", str(wt)], env=clean_env(DEV_SLOT="3"))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("未設定", res.stdout)
        lines = plus_lines(res.stdout)
        self.assertIn("PORT=18110", lines[2].split())
        self.assertIn("ALLOWED_ORIGINS=http://127.0.0.1:15203,http://localhost:15203", lines[2].split())
        self.assertIn("http://127.0.0.1:18110/api/healthz", lines[3])
        self.assertIn("DEV_SERVER_PORT=18110", lines[4].split())
        self.assertIn("--port 15203", lines[4])
        self.assertIn("http://127.0.0.1:15203/", lines[5])

    # DL-3
    def test_bad_slot(self) -> None:
        wt = self.make_tree()
        for slot in ["abc", "100", "07"]:
            with self.subTest(slot=slot):
                res = run(["--dry-run", "--path", str(wt)], env=clean_env(DEV_SLOT=slot))
                self.assertEqual(res.returncode, 1, res.stdout)
                self.assertIn("DEV_SLOT の値が不正", res.stderr)
                self.assertEqual(plus_lines(res.stdout), [])

    # DL-4
    def test_missing_stages(self) -> None:
        wt = self.make_tree()
        (wt / "data" / "stages" / "a.yaml").unlink()
        (wt / "data" / "stages" / "a.yml").write_text("id: a\n", encoding="utf-8")
        res = run(["--dry-run", "--path", str(wt)])
        self.assertEqual(res.returncode, 1, res.stdout)
        self.assertIn("ステージ定義がない", res.stderr)
        self.assertEqual(plus_lines(res.stdout), [])

    def test_missing_islands(self) -> None:
        wt = self.make_tree()
        (wt / "data" / "islands.json").unlink()
        res = run(["--dry-run", "--path", str(wt)])
        self.assertEqual(res.returncode, 1, res.stdout)
        self.assertIn("ステージ定義がない", res.stderr)

    def test_missing_data_dir(self) -> None:
        wt = self.make_tree()
        res = run(["--dry-run", "--path", str(wt), "--data", str(self.tmp / "nothing")])
        self.assertEqual(res.returncode, 1, res.stdout)
        self.assertIn("ステージ定義がない", res.stderr)

    def test_data_option(self) -> None:
        wt = self.make_tree()
        (wt / "data" / "islands.json").unlink()
        other = self.tmp / "other" / "stage-data"
        self.make_data(other)
        res = run(["--dry-run", "--path", str(wt), "--data", str(other)])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn(f"DATA_DIR={other}", plus_lines(res.stdout)[2].split())
        # 相対パスを渡しても、DATA_DIR= は絶対パス。
        res = run(["--dry-run", "--path", str(wt), "--data", "stage-data"], cwd=self.tmp / "other")
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn(f"DATA_DIR={other}", plus_lines(res.stdout)[2].split())
        self.assertIn(f"DATA_DIR={other}", res.stdout.splitlines()[1])

    # DL-5
    def test_missing_files(self) -> None:
        for rel in ["server/cmd/server/main.go", "web/package.json"]:
            with self.subTest(file=rel):
                wt = self.make_tree(rel.replace("/", "_"))
                (wt / rel).unlink()
                res = run(["--dry-run", "--path", str(wt)])
                self.assertEqual(res.returncode, 1, res.stdout)
                self.assertIn(rel, res.stderr)
                self.assertTrue(res.stderr.startswith("demo-local: "), res.stderr)


class ArgsTest(TempTree):
    # DL-6
    def test_bad_args(self) -> None:
        wt = self.make_tree()
        cases = [
            ["--bogus"],
            ["--data"],
            ["--path"],
            ["--check", "--dry-run"],
            ["--dry-run", "--dry-run"],
            ["--check", "--check"],
            ["--data", "a", "--data", "b"],
        ]
        for args in cases:
            with self.subTest(args=args):
                res = run(["--path", str(wt), *args] if args != ["--path"] else args)
                self.assertEqual(res.returncode, 1, res.stdout)
                self.assertIn("使い方", res.stderr)
                self.assertIn("demo-local: ", res.stderr)

    def test_help(self) -> None:
        for flag in ["--help", "-h"]:
            with self.subTest(flag=flag):
                res = run([flag])
                self.assertEqual(res.returncode, 0, res.stderr)
                self.assertIn("--check", res.stdout)


class CheckTest(TempTree):
    # DL-7
    def check(self, *, go_rc: Optional[str] = None, vite: bool = True) -> tuple:
        wt = self.make_tree()
        bindir, fakedir = self.make_fakes(wt)
        if not vite:
            (wt / "web" / "node_modules" / ".bin" / "vite").unlink()
        extra = {} if go_rc is None else {"FAKE_GO_RC": go_rc}
        res = run(["--check", "--path", str(wt)], env=self.fake_env(bindir, fakedir, free_slot(), **extra))
        self.assertFalse((fakedir / "pnpm.args").exists(), "pnpm が呼ばれた")
        self.assertFalse((fakedir / "server").exists(), "サーバーが起動した")
        return res, fakedir, wt

    def test_ok(self) -> None:
        res, fakedir, wt = self.check()
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        self.assertIn("準備 OK", res.stdout)
        env = read_env(fakedir / "go.env")
        self.assertEqual(env.get("GOPROXY"), "off")
        self.assertEqual(env.get("GOFLAGS"), "-mod=readonly")
        args = (fakedir / "go.args").read_text(encoding="utf-8").splitlines()
        self.assertEqual(args[:4], ["-C", f"{wt}/server", "build", "-o"])
        self.assertEqual(args[5:], ["./cmd/server"])
        # 一時ディレクトリを消している。
        self.assertFalse(Path(args[4]).parent.exists(), args[4])

    def test_go_fails(self) -> None:
        res, _, _ = self.check(go_rc="1")
        self.assertEqual(res.returncode, 1, res.stdout)
        self.assertIn("ネットワークなしでは Go をビルドできない", res.stderr)
        self.assertNotIn("準備 OK", res.stdout)

    def test_no_vite(self) -> None:
        res, fakedir, _ = self.check(vite=False)
        self.assertEqual(res.returncode, 1, res.stdout)
        self.assertIn("web の依存が入っていない", res.stderr)
        self.assertFalse((fakedir / "go.args").exists())

    def test_port_in_use(self) -> None:
        wt = self.make_tree()
        bindir, fakedir = self.make_fakes(wt)
        slot = int(free_slot())
        for port in (18080 + slot * 10, 15173 + slot * 10):
            with self.subTest(port=port), socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
                try:
                    s.bind(("127.0.0.1", port))
                except OSError:
                    self.skipTest(f"ポート {port} を取れない")
                s.listen(1)
                res = run(["--check", "--path", str(wt)], env=self.fake_env(bindir, fakedir, str(slot)))
                self.assertEqual(res.returncode, 1, res.stdout)
                self.assertIn(f"ポート {port} は使用中", res.stderr)
                self.assertFalse((fakedir / "go.args").exists())


class StartTest(TempTree):
    def start(self, **extra: str) -> tuple:
        wt = self.make_tree()
        bindir, fakedir = self.make_fakes(wt)
        slot = free_slot()
        out = self.tmp / "stdout.txt"
        err = self.tmp / "stderr.txt"
        with open(out, "w", encoding="utf-8") as fo, open(err, "w", encoding="utf-8") as fe:
            proc = subprocess.Popen(
                ["bash", str(SCRIPT), "--path", str(wt)],
                env=self.fake_env(bindir, fakedir, slot, **extra),
                stdout=fo,
                stderr=fe,
            )
        self.addCleanup(lambda: proc.poll() is None and proc.kill())
        return proc, wt, fakedir, slot, out, err

    def wait_ready(self, proc: subprocess.Popen, out: Path) -> str:
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            text = out.read_text(encoding="utf-8")
            if "準備完了" in text:
                return text
            if proc.poll() is not None:
                break
            time.sleep(0.05)
        self.fail(f"準備完了が出ない: {out.read_text(encoding='utf-8')}")

    def child_pids(self, fakedir: Path) -> dict:
        # 偽の curl はすぐ 0 で終わるので、準備完了の後に子が PID を書くことがある（環境変数は PID より先に書く）。
        names = ["server", "pnpm"]
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if all((fakedir / n).exists() and (fakedir / n).read_text().strip() for n in names):
                break
            time.sleep(0.05)
        else:
            self.fail(f"子のプロセスが PID を書かない: {names}")
        pids = {n: int((fakedir / n).read_text().strip()) for n in names}
        for pid in pids.values():
            self.addCleanup(lambda p=pid: alive(p) and os.kill(p, signal.SIGKILL))
        return pids

    def assert_gone(self, pids: list) -> None:
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline and any(alive(p) for p in pids):
            time.sleep(0.05)
        for pid in pids:
            self.assertFalse(alive(pid), f"PID {pid} が残っている")

    # DL-8
    def test_start_and_sigterm(self) -> None:
        proc, wt, fakedir, slot, out, _ = self.start()
        text = self.wait_ready(proc, out)
        n = int(slot)
        p, w = 18080 + n * 10, 15173 + n * 10
        self.assertIn(f"準備完了 → http://127.0.0.1:{w}/", text)
        pids = self.child_pids(fakedir)

        server = read_env(fakedir / "server.env")
        self.assertEqual(server.get("MODE"), "standalone")
        self.assertEqual(server.get("PORT"), str(p))
        self.assertEqual(server.get("DATA_DIR"), f"{wt}/data")
        self.assertEqual(server.get("ALLOWED_ORIGINS"), f"http://127.0.0.1:{w},http://localhost:{w}")
        self.assertEqual(server.get("DEMO_CONTROLS"), "1")

        pnpm = read_env(fakedir / "pnpm.env")
        self.assertEqual(pnpm.get("DEV_BACKEND"), "standalone")
        self.assertEqual(pnpm.get("DEV_SERVER_PORT"), str(p))
        self.assertIn("VITE_TURNSTILE_SITE_KEY", pnpm)
        self.assertEqual(pnpm["VITE_TURNSTILE_SITE_KEY"], "")
        args = (fakedir / "pnpm.args").read_text(encoding="utf-8").splitlines()
        self.assertEqual(
            args,
            ["-C", f"{wt}/web", "exec", "vite", "--host", "127.0.0.1", "--port", str(w), "--strictPort"],
        )

        proc.send_signal(signal.SIGTERM)
        try:
            rc = proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.fail("SIGTERM のあと 5 秒以内にスクリプトが終わらない")
        self.assertEqual(rc, 143)
        self.assert_gone(list(pids.values()))

    def test_sigint(self) -> None:
        proc, _, fakedir, _, out, _ = self.start()
        self.wait_ready(proc, out)
        pids = self.child_pids(fakedir)
        proc.send_signal(signal.SIGINT)
        self.assertEqual(proc.wait(timeout=5), 130)
        self.assert_gone(list(pids.values()))

    # pnpm が sh のラッパーで、孫（vite）を exec せずに起動していても、SIGTERM で孫まで止まる。
    def test_sigterm_stops_grandchild(self) -> None:
        proc, _, fakedir, _, out, _ = self.start(FAKE_PNPM_WRAP="1")
        self.wait_ready(proc, out)
        pids = self.child_pids(fakedir)
        path = fakedir / "grandchild"
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline and not (path.exists() and path.read_text().strip()):
            time.sleep(0.05)
        grandchild = int(path.read_text().strip())
        self.addCleanup(lambda: alive(grandchild) and os.kill(grandchild, signal.SIGKILL))
        proc.send_signal(signal.SIGTERM)
        self.assertEqual(proc.wait(timeout=10), 143)
        self.assert_gone([*pids.values(), grandchild])

    # DL-9
    def test_server_exits_immediately(self) -> None:
        proc, _, fakedir, _, out, err = self.start(FAKE_SERVER_RC="1", FAKE_CURL_RC="7")
        try:
            rc = proc.wait(timeout=30)
        except subprocess.TimeoutExpired:
            self.fail("スクリプトが終わらない")
        self.assertEqual(rc, 1)
        self.assertIn("Go のサーバーが起動しなかった", err.read_text(encoding="utf-8"))
        self.assertNotIn("準備完了", out.read_text(encoding="utf-8"))
        self.assertTrue((fakedir / "server").exists(), "サーバーが起動していない")
        self.assertFalse((fakedir / "pnpm.args").exists(), "pnpm が呼ばれた")

    # DL-10
    def test_vite_stops(self) -> None:
        proc, _, fakedir, _, out, err = self.start()
        self.wait_ready(proc, out)
        pids = self.child_pids(fakedir)
        os.kill(pids["pnpm"], signal.SIGTERM)
        try:
            rc = proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.fail("Vite が止まったあと 5 秒以内にスクリプトが終わらない")
        self.assertEqual(rc, 1)
        self.assertIn("Vite が止まった", err.read_text(encoding="utf-8"))
        self.assert_gone([pids["server"]])

    def test_server_stops(self) -> None:
        proc, _, fakedir, _, out, err = self.start()
        self.wait_ready(proc, out)
        pids = self.child_pids(fakedir)
        os.kill(pids["server"], signal.SIGTERM)
        self.assertEqual(proc.wait(timeout=5), 1)
        self.assertIn("Go のサーバーが止まった", err.read_text(encoding="utf-8"))
        self.assert_gone([pids["pnpm"]])


class ScriptFileTest(unittest.TestCase):
    def test_mode_and_strict(self) -> None:
        self.assertTrue(os.access(SCRIPT, os.X_OK))
        lines = SCRIPT.read_text(encoding="utf-8").splitlines()
        code = "\n".join(l for l in lines if not l.lstrip().startswith("#"))
        self.assertIn("set -euo pipefail", code)
        for banned in ("mapfile", "readarray", "wait -n", ",,}", "^^}"):
            self.assertNotIn(banned, code)

    def test_no_port_formula(self) -> None:
        # ポートは scripts/dev-env.sh env から得る（AC-4）。
        code = SCRIPT.read_text(encoding="utf-8")
        for literal in ("18080", "15173"):
            self.assertNotIn(literal, code)
        self.assertIn("dev-env.sh", code)


if __name__ == "__main__":
    unittest.main()
