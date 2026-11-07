"""scripts/dev-env.sh のテスト（T-008 / Issue #16 / ADR-0019）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_*.py'

ネットワーク・Docker・pnpm・Go がなくても通る。Docker は偽物のスクリプトを
環境変数 DOCKER で渡す。作業ツリーは一時ディレクトリの中に作り、--path で渡す。
"""

import os
import re
import signal
import stat
import subprocess
import tempfile
import textwrap
import time
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
SCRIPT = REPO_ROOT / "scripts" / "dev-env.sh"

# 子のプロセスに引き継がない環境変数。
DROP_KEYS = (
    "DEV_SLOT",
    "DOCKER",
    "MAKEFLAGS",
    "MAKELEVEL",
    "MFLAGS",
    "MAKEOVERRIDES",
    "DRY_RUN",
    "FAKE_IMAGE",
    "FAKE_LOG",
    "FAKE_MODE",
    "FAKE_CONTAINER",
    "FAKE_SIMILAR",
    "FAKE_PIDS",
)

TAG_RE = re.compile(r"^[a-z0-9][a-z0-9_.-]{0,127}$")
CONTAINER_RE = re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9_.-]+$")


def clean_env(**extra: str) -> dict:
    env = {k: v for k, v in os.environ.items() if k not in DROP_KEYS and not k.startswith("DEV_")}
    env.update(extra)
    return env


def run(args, *, env=None, cwd=None) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["bash", str(SCRIPT), *args],
        cwd=cwd or REPO_ROOT,
        env=env if env is not None else clean_env(),
        capture_output=True,
        text=True,
        encoding="utf-8",
        timeout=60,
    )


def parse_env(stdout: str) -> dict:
    """eval で読んだ値を返す（%q の引用を bash に解かせる）。"""
    keys = [line.split("=", 1)[0] for line in stdout.splitlines() if "=" in line]
    script = 'eval "$1"; shift; for k in "$@"; do printf "%s\\0%s\\0" "$k" "${!k}"; done'
    res = subprocess.run(
        ["bash", "-c", script, "_", stdout, *keys],
        capture_output=True,
        text=True,
        encoding="utf-8",
        check=True,
    )
    parts = res.stdout.split("\0")[:-1]
    return dict(zip(parts[0::2], parts[1::2]))


class TempTree(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.tmp = Path(self._tmp.name)

    def tearDown(self) -> None:
        self._tmp.cleanup()

    def tree(self, name: str) -> Path:
        p = self.tmp / name
        p.mkdir(parents=True, exist_ok=True)
        return p

    def env_of(self, path: Path, slot: str = "3") -> dict:
        res = run(["env", "--path", str(path)], env=clean_env(DEV_SLOT=slot))
        self.assertEqual(res.returncode, 0, res.stderr)
        return parse_env(res.stdout)


class PortTest(TempTree):
    # D-1
    def test_port_formula(self) -> None:
        wt = self.tree("wt")
        for slot in ["0", "1", "3", "9", "10", "99"]:
            with self.subTest(slot=slot):
                values = self.env_of(wt, slot)
                n = int(slot)
                self.assertEqual(values["DEV_SLOT"], slot)
                self.assertEqual(values["DEV_SERVER_PORT"], str(18080 + n * 10))
                self.assertEqual(values["DEV_WEB_PORT"], str(15173 + n * 10))
        self.assertEqual(self.env_of(wt, "0")["DEV_SERVER_PORT"], "18080")
        self.assertEqual(self.env_of(wt, "0")["DEV_WEB_PORT"], "15173")
        self.assertEqual(self.env_of(wt, "3")["DEV_SERVER_PORT"], "18110")
        self.assertEqual(self.env_of(wt, "3")["DEV_WEB_PORT"], "15203")
        self.assertEqual(self.env_of(wt, "99")["DEV_SERVER_PORT"], "19070")
        self.assertEqual(self.env_of(wt, "99")["DEV_WEB_PORT"], "16163")

    # D-2
    def test_ranges_do_not_overlap(self) -> None:
        wt = self.tree("wt")
        # 100 回 bash を起動しないように、env を 1 回の bash の中でまとめて呼ぶ。
        script = (
            'for s in $(seq 0 99); do DEV_SLOT=$s bash "$1" env --path "$2" '
            '| grep -E "^DEV_(SERVER|WEB)_PORT=" | sed "s/^/$s /"; done'
        )
        res = subprocess.run(
            ["bash", "-c", script, "_", str(SCRIPT), str(wt)],
            env=clean_env(),
            capture_output=True,
            text=True,
            check=True,
        )
        seen: dict = {}
        count = 0
        for line in res.stdout.splitlines():
            slot, kv = line.split(" ", 1)
            key, value = kv.split("=", 1)
            count += 1
            for port in range(int(value), int(value) + 10):
                owner = (slot, key)
                self.assertNotIn(port, seen, f"{port}: {seen.get(port)} と {owner} が重なる")
                seen[port] = owner
        self.assertEqual(count, 200)

    # D-3
    def test_invalid_slot(self) -> None:
        wt = self.tree("wt")
        (wt / "web").mkdir()
        (wt / "web" / "package.json").write_text("{}", encoding="utf-8")
        cases = [None, "", "abc", "-1", "08", "100", "1.5"]
        for slot in cases:
            env = clean_env() if slot is None else clean_env(DEV_SLOT=slot)
            for args in (["env"], ["dev", "--dry-run"]):
                with self.subTest(slot=slot, args=args):
                    res = run([*args, "--path", str(wt)], env=env)
                    self.assertEqual(res.returncode, 1, res.stdout)
                    self.assertIn("DEV_SLOT", res.stderr)
                    if slot is None:
                        self.assertIn("scripts/dev-slot.sh acquire", res.stderr)


class NameTest(TempTree):
    # D-4
    def test_eval_with_space(self) -> None:
        wt = self.tree("My Worktree")
        res = subprocess.run(
            [
                "bash",
                "-c",
                'eval "$(bash "$2" env --path "$1")"; '
                'printf "%s\\n" "$DEV_WORKTREE" "$DEV_CF_PERSIST_TO" "$DEV_DOCKER_LABEL"',
                "_",
                str(wt),
                str(SCRIPT),
            ],
            env=clean_env(DEV_SLOT="1"),
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(res.returncode, 0, res.stderr)
        real = os.path.realpath(wt)
        self.assertEqual(
            res.stdout.splitlines(),
            [real, f"{real}/web/.cloudflare/state", f"raft-game.worktree={real}"],
        )

    # D-5
    def test_docker_names(self) -> None:
        long_name = "A" * 30 + "-" + "b" * 29
        self.assertEqual(len(long_name), 60)
        cases = {
            "issue-16": r"^issue-16-[0-9a-f]{8}$",
            "My Worktree": r"^my-worktree-[0-9a-f]{8}$",
            "作業": r"^wt-[0-9a-f]{8}$",
            long_name: r"^[a-z0-9_.-]+-[0-9a-f]{8}$",
            "--Weird__名前..": r"^weird-[0-9a-f]{8}$",
        }
        for name, pattern in cases.items():
            with self.subTest(name=name):
                values = self.env_of(self.tree(name))
                slug = values["DEV_DOCKER_SLUG"]
                self.assertRegex(slug, pattern)
                self.assertLessEqual(len(slug.rsplit("-", 1)[0]), 32)
                image = values["DEV_DOCKER_IMAGE"]
                self.assertTrue(image.startswith("raft-game-server:"), image)
                self.assertRegex(image[len("raft-game-server:"):], TAG_RE)
                self.assertEqual(image, f"raft-game-server:{slug}")
                self.assertRegex(values["DEV_DOCKER_CONTAINER"], CONTAINER_RE)
                self.assertEqual(values["DEV_DOCKER_CONTAINER"], f"raft-game-server-{slug}")

    # D-6
    def test_same_basename_different_slug(self) -> None:
        a = self.env_of(self.tree("a/raft-game"))["DEV_DOCKER_SLUG"]
        b = self.env_of(self.tree("b/raft-game"))["DEV_DOCKER_SLUG"]
        self.assertNotEqual(a, b)
        self.assertEqual(a, self.env_of(self.tree("a/raft-game"))["DEV_DOCKER_SLUG"])

    # D-7
    def test_default_path(self) -> None:
        res = run(["env"], env=clean_env(DEV_SLOT="1"), cwd=REPO_ROOT)
        self.assertEqual(res.returncode, 0, res.stderr)
        top = subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            check=True,
        ).stdout.strip()
        self.assertEqual(parse_env(res.stdout)["DEV_WORKTREE"], os.path.realpath(top))

        outside = self.tree("outside")
        env = clean_env(DEV_SLOT="1", GIT_CEILING_DIRECTORIES=str(self.tmp))
        res = run(["env"], env=env, cwd=outside)
        self.assertEqual(res.returncode, 1, res.stdout)
        self.assertIn("--path", res.stderr)


class ArgsTest(TempTree):
    # D-8
    def test_bad_args(self) -> None:
        wt = self.tree("wt")
        env = clean_env(DEV_SLOT="1")
        for args in (
            ["env", "--path", str(self.tmp / "nope")],
            ["env", "--path"],
            ["nope"],
            [],
            ["env", "--path", str(wt), "--nope"],
        ):
            with self.subTest(args=args):
                res = run(args, env=env)
                self.assertEqual(res.returncode, 1, res.stdout)
                self.assertIn("dev-env:", res.stderr)
        for args in (["--help"], ["-h"], ["env", "--help"]):
            with self.subTest(args=args):
                res = run(args, env=env)
                self.assertEqual(res.returncode, 0, res.stderr)
                for word in ("env", "dev", "clean-docker"):
                    self.assertIn(word, res.stdout)


class DevDryRunTest(TempTree):
    # D-9
    def dev(self, wt: Path) -> subprocess.CompletedProcess:
        env = clean_env(DEV_SLOT="3", PATH="/usr/bin:/bin")
        return run(["dev", "--path", str(wt), "--dry-run"], env=env)

    def plus_lines(self, res: subprocess.CompletedProcess) -> list:
        return [line for line in res.stdout.splitlines() if line.startswith("+ ")]

    def test_web_only(self) -> None:
        wt = self.tree("wt")
        (wt / "web").mkdir()
        (wt / "web" / "package.json").write_text("{}", encoding="utf-8")
        res = self.dev(wt)
        self.assertEqual(res.returncode, 0, res.stderr)
        vite = [line for line in self.plus_lines(res) if "vite" in line]
        self.assertEqual(len(vite), 1, res.stdout)
        for part in ("--host 127.0.0.1", "--port 15203", "--strictPort", "DEV_SERVER_PORT=18110"):
            self.assertIn(part, vite[0])
        self.assertIn("server/cmd/server がない", res.stdout)
        self.assertFalse(any("build -o" in line for line in self.plus_lines(res)))

    def test_with_go(self) -> None:
        wt = self.tree("wt")
        (wt / "web").mkdir()
        (wt / "web" / "package.json").write_text("{}", encoding="utf-8")
        (wt / "server" / "cmd" / "server").mkdir(parents=True)
        (wt / "server" / "cmd" / "server" / "main.go").write_text("package main\n", encoding="utf-8")
        res = self.dev(wt)
        self.assertEqual(res.returncode, 0, res.stderr)
        lines = self.plus_lines(res)
        self.assertTrue(any("build -o" in l and "./cmd/server" in l for l in lines), res.stdout)
        self.assertTrue(any("MODE=standalone" in l and "PORT=18110" in l for l in lines), res.stdout)
        # MK-1（T-302）: Go に作業ツリーの data/ と Vite のオリジンを渡す。
        go = [l for l in lines if "MODE=standalone" in l]
        self.assertEqual(len(go), 1, res.stdout)
        parts = go[0].split()
        data = [p[len("DATA_DIR="):] for p in parts if p.startswith("DATA_DIR=")]
        self.assertEqual(len(data), 1, go[0])
        self.assertEqual(os.path.realpath(data[0]), os.path.realpath(wt / "data"))
        self.assertIn("ALLOWED_ORIGINS=http://127.0.0.1:15203,http://localhost:15203", parts)
        self.assertTrue(any("vite" in l and "--port 15203" in l for l in lines), res.stdout)
        self.assertNotIn("server/cmd/server がない", res.stdout)

    def test_nothing(self) -> None:
        res = self.dev(self.tree("wt"))
        self.assertEqual(res.returncode, 1, res.stdout)
        self.assertIn("dev-env:", res.stderr)


FAKE_PNPM = textwrap.dedent(
    """\
    #!/usr/bin/env bash
    # 偽物の pnpm。PID を書いてから、長く眠る（exec なので PID は変わらない）。
    echo "$$" >"$FAKE_PIDS/pnpm"
    exec sleep 300
    """
)

FAKE_GO = textwrap.dedent(
    """\
    #!/usr/bin/env bash
    # 偽物の go。build -o <out> に、PID を書いて長く眠るサーバーを作る。
    out=""
    while [ $# -gt 0 ]; do
      if [ "$1" = -o ]; then out="$2"; shift; fi
      shift
    done
    printf '#!/usr/bin/env bash\\necho "$$" >"$FAKE_PIDS/server"\\nexec sleep 300\\n' >"$out"
    chmod +x "$out"
    """
)


def write_exe(path: Path, text: str) -> None:
    path.write_text(text, encoding="utf-8")
    path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


def alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    return True


class DevSignalTest(TempTree):
    # D-13: SIGTERM で、スクリプトと裏で動くフロントエンド・Go の両方が数秒以内に終わる。
    def start(self, *, go: bool, web: bool) -> tuple:
        wt = self.tree("wt")
        if web:
            (wt / "web").mkdir()
            (wt / "web" / "package.json").write_text("{}", encoding="utf-8")
        if go:
            (wt / "server" / "cmd" / "server").mkdir(parents=True)
            (wt / "server" / "cmd" / "server" / "main.go").write_text("package main\n", encoding="utf-8")
        bindir = self.tree("bin")
        write_exe(bindir / "pnpm", FAKE_PNPM)
        write_exe(bindir / "go", FAKE_GO)
        pids = self.tree("pids")
        env = clean_env(DEV_SLOT="3", PATH=f"{bindir}:/usr/bin:/bin", FAKE_PIDS=str(pids))
        proc = subprocess.Popen(
            ["bash", str(SCRIPT), "dev", "--path", str(wt)],
            env=env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        self.addCleanup(lambda: proc.poll() is None and proc.kill())
        return proc, pids

    def wait_pids(self, pids: Path, names: list) -> list:
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if all((pids / n).exists() and (pids / n).read_text().strip() for n in names):
                return [int((pids / n).read_text()) for n in names]
            time.sleep(0.05)
        self.fail(f"子のプロセスが起動しない: {names}")

    def check(self, *, go: bool, web: bool, names: list) -> None:
        proc, pids = self.start(go=go, web=web)
        children = self.wait_pids(pids, names)
        for pid in children:
            self.addCleanup(lambda p=pid: alive(p) and os.kill(p, signal.SIGKILL))
        time.sleep(0.2)
        proc.send_signal(signal.SIGTERM)
        try:
            rc = proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.fail("SIGTERM のあと 5 秒以内にスクリプトが終わらない")
        self.assertEqual(rc, 143)
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline and any(alive(p) for p in children):
            time.sleep(0.05)
        for name, pid in zip(names, children):
            self.assertFalse(alive(pid), f"{name}（PID {pid}）が残っている")

    def test_web_only(self) -> None:
        self.check(go=False, web=True, names=["pnpm"])

    def test_go_and_web(self) -> None:
        self.check(go=True, web=True, names=["pnpm", "server"])

    def test_go_only(self) -> None:
        self.check(go=True, web=False, names=["server"])


FAKE_DOCKER = textwrap.dedent(
    """\
    #!/usr/bin/env bash
    # 偽物の docker。引数を US（\\037）区切りで 1 回 1 行ずつログに書く。
    {
      for a in "$@"; do printf '%s\\037' "$a"; done
      printf '\\n'
    } >>"$FAKE_LOG"
    mode="${FAKE_MODE:-}"
    all="$*"
    case "$1" in
      ps)
        [ "$mode" = psfail ] && { echo "cannot connect" >&2; exit 1; }
        [ "$mode" = empty ] && exit 0
        case "$all" in
          *label=*) printf 'c1\\nc2\\n' ;;
          *name=*)
            # 実物は name を正規表現として照合するので、似た名前（FAKE_SIMILAR）も返しうる。
            printf 'c2 %s\\n' "$FAKE_CONTAINER"
            for s in ${FAKE_SIMILAR:-}; do printf 'x-%s %s\\n' "$s" "$s"; done
            ;;
        esac
        ;;
      images)
        [ "$mode" = empty ] && exit 0
        case "$all" in
          *label=*) printf '%s\\n<none>:<none>\\n' "$FAKE_IMAGE" ;;
          *) printf '%s\\n' "$FAKE_IMAGE" ;;
        esac
        ;;
      rm | image)
        [ "$mode" = rmfail ] && exit 1
        ;;
    esac
    exit 0
    """
)


def make_fake_docker(directory: Path) -> Path:
    path = directory / "fake-docker"
    path.write_text(FAKE_DOCKER, encoding="utf-8")
    path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    return path


def read_log(log: Path) -> list:
    if not log.exists():
        return []
    return [line.split("\x1f")[:-1] for line in log.read_text(encoding="utf-8").splitlines()]


class CleanDockerTest(TempTree):
    def setUp(self) -> None:
        super().setUp()
        self.wt = self.tree("wt-clean")
        self.values = self.env_of(self.wt)
        bindir = self.tree("bin")
        self.docker = make_fake_docker(bindir)
        self.log = self.tmp / "docker.log"

    def clean(self, *extra: str, mode: str = "", docker: str = "", similar: str = "") -> subprocess.CompletedProcess:
        env = clean_env(
            DOCKER=docker or str(self.docker),
            FAKE_LOG=str(self.log),
            FAKE_IMAGE=self.values["DEV_DOCKER_IMAGE"],
            FAKE_CONTAINER=self.values["DEV_DOCKER_CONTAINER"],
            FAKE_SIMILAR=similar,
            FAKE_MODE=mode,
        )
        return run(["clean-docker", "--path", str(self.wt), *extra], env=env)

    def assert_safe(self, calls: list) -> None:
        real = os.path.realpath(self.wt)
        slug = self.values["DEV_DOCKER_SLUG"]
        for call in calls:
            self.assertNotIn("prune", call)
            self.assertNotIn("system", call)
            if call[:1] == ["rm"] or call[:2] == ["image", "rm"]:
                self.assertNotIn("-a", call)
            for i, a in enumerate(call):
                if a == "--filter":
                    f = call[i + 1]
                    if f.startswith("label="):
                        self.assertEqual(f, f"label=raft-game.worktree={real}")
                    elif f.startswith("name="):
                        quoted = re.sub(r"([][\\.*^$+?(){}|/])", r"\\\1", f"raft-game-server-{slug}")
                        self.assertEqual(f, f"name=^{quoted}$")
                    else:
                        self.fail(f"未知の filter: {f}")

    # D-10
    def test_dry_run(self) -> None:
        res = self.clean("--dry-run")
        self.assertEqual(res.returncode, 0, res.stderr)
        calls = read_log(self.log)
        self.assertTrue(calls)
        self.assertEqual({c[0] for c in calls}, {"ps", "images"})
        image = self.values["DEV_DOCKER_IMAGE"]
        lines = res.stdout.splitlines()
        self.assertIn(f"+ {self.docker} rm -f c1 c2", lines)
        self.assertIn(f"+ {self.docker} image rm {image}", lines)
        self.assertNotIn("<none>", res.stdout)
        self.assert_safe(calls)

    def test_run(self) -> None:
        res = self.clean()
        self.assertEqual(res.returncode, 0, res.stderr)
        calls = read_log(self.log)
        image = self.values["DEV_DOCKER_IMAGE"]
        rm = [i for i, c in enumerate(calls) if c == ["rm", "-f", "c1", "c2"]]
        irm = [i for i, c in enumerate(calls) if c == ["image", "rm", image]]
        self.assertEqual(len(rm), 1, calls)
        self.assertEqual(len(irm), 1, calls)
        self.assertLess(rm[0], irm[0])
        deletes = [c for c in calls if c[0] not in ("ps", "images")]
        self.assertEqual(len(deletes), 2, calls)
        self.assert_safe(calls)

    # D-11
    def test_no_docker(self) -> None:
        res = self.clean(docker=str(self.tmp / "no-such-docker"))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("docker がない", res.stdout)

    def test_ps_fails(self) -> None:
        res = self.clean(mode="psfail")
        self.assertEqual(res.returncode, 1, res.stdout)
        self.assertIn("Docker に接続できない", res.stderr)

    def test_nothing_to_remove(self) -> None:
        res = self.clean(mode="empty")
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("この作業ツリーの Docker のコンテナ・イメージはない", res.stdout)
        self.assertFalse(any("rm" in c for c in read_log(self.log)))

    def test_rm_fails(self) -> None:
        res = self.clean(mode="rmfail")
        self.assertEqual(res.returncode, 1, res.stdout)

    # D-12
    def test_dot_in_name(self) -> None:
        self.wt = self.tree("raft.game")
        self.values = self.env_of(self.wt)
        name = self.values["DEV_DOCKER_CONTAINER"]
        self.assertIn(".", name)
        similar = " ".join(
            [name.replace(".", "X"), name + "-old", "other-" + name, name.replace(".", "\\.")]
        )
        for dry in (True, False):
            with self.subTest(dry=dry):
                if self.log.exists():
                    self.log.unlink()
                res = self.clean(*(["--dry-run"] if dry else []), similar=similar)
                self.assertEqual(res.returncode, 0, res.stderr)
                calls = read_log(self.log)
                self.assert_safe(calls)
                names = [c[i + 1] for c in calls for i, a in enumerate(c) if a == "--filter" and c[i + 1].startswith("name=")]
                self.assertEqual(len(names), 1, calls)
                self.assertIn("raft\\.game", names[0])
                self.assertNotIn("x-", res.stdout)
                if dry:
                    self.assertIn(f"+ {self.docker} rm -f c1 c2", res.stdout.splitlines())
                else:
                    rm = [c for c in calls if c[:1] == ["rm"]]
                    self.assertEqual(rm, [["rm", "-f", "c1", "c2"]], calls)

    def test_without_dev_slot(self) -> None:
        # clean() の環境には DEV_SLOT がない（clean_env が消す）。
        self.assertNotIn("DEV_SLOT", clean_env())
        res = self.clean("--dry-run")
        self.assertEqual(res.returncode, 0, res.stderr)


class ScriptFileTest(unittest.TestCase):
    def test_mode_and_strict(self) -> None:
        self.assertTrue(os.access(SCRIPT, os.X_OK))
        lines = SCRIPT.read_text(encoding="utf-8").splitlines()
        code = "\n".join(l for l in lines if not l.lstrip().startswith("#"))
        self.assertIn("set -euo pipefail", code)
        text = code
        for banned in ("mapfile", "readarray", ",,}", "^^}"):
            self.assertNotIn(banned, text)


if __name__ == "__main__":
    unittest.main()
