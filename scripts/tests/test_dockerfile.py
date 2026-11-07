"""server/Dockerfile・server/.dockerignore・scripts/docker-smoke.sh の静的な検査（T-306 / Issue #42）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_*.py'

Docker・ネットワークを使わない（標準ライブラリだけ）。イメージそのものの確認は
scripts/docker-smoke.sh（Docker が要る）で行う。
"""

import json
import re
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
DOCKERFILE = REPO_ROOT / "server" / "Dockerfile"
DOCKERIGNORE = REPO_ROOT / "server" / ".dockerignore"
ETCD_RAFT_GOMOD = REPO_ROOT / "server" / "third_party" / "etcd-raft" / "go.mod"
SMOKE = REPO_ROOT / "scripts" / "docker-smoke.sh"

DISTROLESS_RE = re.compile(r"^gcr\.io/distroless/static-debian1[2-9]:nonroot@sha256:[0-9a-f]{64}$")


def parse_dockerfile(text: str) -> list:
    """(命令, 引数) の一覧。コメントと空行を除き、行の継続（末尾の \\）をつなぐ。"""
    instrs = []
    buf = ""
    for raw in text.splitlines():
        line = raw.strip()
        if not buf and (not line or line.startswith("#")):
            continue
        if buf and line.startswith("#"):
            continue  # 継続の途中のコメント行は Docker と同じく読み飛ばす
        if line.endswith("\\"):
            buf += line[:-1].rstrip() + " "
            continue
        buf += line
        if buf.strip():
            parts = buf.strip().split(None, 1)
            instrs.append((parts[0].upper(), parts[1] if len(parts) > 1 else ""))
        buf = ""
    if buf.strip():
        parts = buf.strip().split(None, 1)
        instrs.append((parts[0].upper(), parts[1] if len(parts) > 1 else ""))
    return instrs


def split_stages(instrs: list) -> list:
    """FROM ごとに区切った命令の一覧の一覧（最初の FROM より前の命令は捨てる）。"""
    stages = []
    for ins in instrs:
        if ins[0] == "FROM":
            stages.append([ins])
        elif stages:
            stages[-1].append(ins)
    return stages


def parse_from(args: str) -> dict:
    """FROM の引数を platform・image・alias に分ける。"""
    toks = args.split()
    platform = ""
    rest = []
    for t in toks:
        if t.startswith("--platform="):
            platform = t[len("--platform=") :]
        else:
            rest.append(t)
    alias = ""
    if len(rest) == 3 and rest[1].upper() == "AS":
        alias = rest[2]
    return {"platform": platform, "image": rest[0] if rest else "", "alias": alias, "rest": rest}


def env_pairs(args: str) -> dict:
    """ENV k=v k2=v2 の形を辞書にする（引用符なしの値だけを扱う）。"""
    out = {}
    for t in args.split():
        if "=" in t:
            k, v = t.split("=", 1)
            out[k] = v
    return out


def toolchain_version() -> str:
    m = re.search(r"^toolchain go(\S+)\s*$", ETCD_RAFT_GOMOD.read_text(encoding="utf-8"), re.M)
    if not m:
        raise AssertionError(f"{ETCD_RAFT_GOMOD} に toolchain の行がない")
    return m.group(1)


class ParserTest(unittest.TestCase):
    def test_joins_continuations_and_skips_comments(self) -> None:
        text = "# c\nFROM a AS b\n\nRUN x \\\n  # mid\n  y \\\n  z\nENV A=1 B=2\n"
        self.assertEqual(
            parse_dockerfile(text),
            [("FROM", "a AS b"), ("RUN", "x y z"), ("ENV", "A=1 B=2")],
        )

    def test_parse_from(self) -> None:
        f = parse_from("--platform=linux/amd64 golang:1.26.4 AS build")
        self.assertEqual((f["platform"], f["image"], f["alias"]), ("linux/amd64", "golang:1.26.4", "build"))


class DockerfileTest(unittest.TestCase):
    def setUp(self) -> None:
        self.text = DOCKERFILE.read_text(encoding="utf-8")
        self.instrs = parse_dockerfile(self.text)
        self.stages = split_stages(self.instrs)

    def instr_args(self, stage: list, name: str) -> list:
        return [a for (n, a) in stage if n == name]

    def all_env(self, stage: list) -> dict:
        out = {}
        for a in self.instr_args(stage, "ENV"):
            out.update(env_pairs(a))
        return out

    def test_d1_two_from_with_platform(self) -> None:
        froms = [parse_from(a) for (n, a) in self.instrs if n == "FROM"]
        self.assertEqual(len(froms), 2, "FROM はちょうど 2 つ")
        for f in froms:
            self.assertEqual(f["platform"], "linux/amd64", f)
        self.assertEqual(froms[0]["alias"], "build", "1 つ目は AS build")

    def test_d2_golang_matches_toolchain(self) -> None:
        image = parse_from(self.stages[0][0][1])["image"]
        m = re.fullmatch(r"golang:(\S+)", image)
        self.assertIsNotNone(m, image)
        self.assertEqual(m.group(1), toolchain_version(), "golang の版は etcd-raft の toolchain と同じ")

    def test_d3_distroless_static_nonroot_digest(self) -> None:
        image = parse_from(self.stages[-1][0][1])["image"]
        self.assertRegex(image, DISTROLESS_RE)

    def test_d4_go_build_flags_and_env(self) -> None:
        build = self.stages[0]
        runs = [a for a in self.instr_args(build, "RUN") if "go build" in a]
        self.assertEqual(len(runs), 1, "go build の RUN は 1 つ")
        line = runs[0]
        for want in ("-tags embeddata", "-trimpath", "-buildvcs=false", "-X main.build=${BUILD_ID}", "./cmd/server"):
            self.assertIn(want, line)
        self.assertNotIn("./...", line, "ビルドするのは ./cmd/server だけ")
        env = self.all_env(build)
        for k, v in (("CGO_ENABLED", "0"), ("GOOS", "linux"), ("GOARCH", "amd64"), ("GOTOOLCHAIN", "local")):
            self.assertEqual(env.get(k), v, k)

    def test_d5_dependency_layer_stage_check_and_build_arg(self) -> None:
        build = self.stages[0]
        idx_download = next(i for i, (n, a) in enumerate(build) if n == "RUN" and "go mod download" in a)
        idx_copy_raft = next(
            (i for i, (n, a) in enumerate(build) if n == "COPY" and "third_party/etcd-raft/go.mod" in a),
            None,
        )
        self.assertIsNotNone(idx_copy_raft, "third_party/etcd-raft/go.mod を COPY する")
        self.assertLess(idx_copy_raft, idx_download, "go mod download より前に COPY する")
        idx_copy_all = next(i for i, (n, a) in enumerate(build) if n == "COPY" and a.split() == [".", "."])
        self.assertLess(idx_download, idx_copy_all, "依存の取得を COPY . . より前の層に分ける")

        checks = [a for a in self.instr_args(build, "RUN") if "internal/stage/embedded/islands.json" in a]
        self.assertEqual(len(checks), 1, "islands.json を確かめる RUN がある")
        self.assertIn("make sync-data", checks[0])
        self.assertIn("test -f", checks[0])

        args = self.instr_args(build, "ARG")
        self.assertTrue(any(a.split("=", 1)[0] == "BUILD_ID" for a in args), "ARG BUILD_ID がある")
        all_args = [a for (n, a) in self.instrs if n == "ARG"]
        self.assertEqual([a.split("=", 1)[0] for a in all_args], ["BUILD_ID"], "ARG は BUILD_ID だけ")
        validate = [a for a in self.instr_args(build, "RUN") if "BUILD_ID が不正" in a]
        self.assertEqual(len(validate), 1, "BUILD_ID を確かめる RUN がある")
        self.assertIn("64", validate[0])

    def test_d6_final_stage_and_forbidden(self) -> None:
        final = self.stages[-1]
        self.assertIn("65532:65532", self.instr_args(final, "USER"))
        self.assertIn("8080", self.instr_args(final, "EXPOSE"))
        env = self.all_env(final)
        self.assertEqual(env.get("MODE"), "single")
        self.assertEqual(env.get("PORT"), "8080")
        entry = self.instr_args(final, "ENTRYPOINT")
        self.assertEqual(len(entry), 1)
        self.assertEqual(json.loads(entry[0]), ["/server"], "ENTRYPOINT は exec の形（JSON の配列）")
        copies = self.instr_args(final, "COPY")
        self.assertEqual(copies, ["--from=build /out/server /server"], "最後の段に置くのは /server だけ")

        for word in ("HEALTHCHECK", "ADD ", ":latest", "curl", "wget"):
            self.assertNotIn(word, self.text, word)


class DockerignoreTest(unittest.TestCase):
    def test_d7_allowlist(self) -> None:
        lines = [
            ln.strip()
            for ln in DOCKERIGNORE.read_text(encoding="utf-8").splitlines()
            if ln.strip() and not ln.strip().startswith("#")
        ]
        self.assertEqual(lines[0], "*", "最初の行で全部を除く")
        for want in ("!go.mod", "!go.sum", "!cmd/server/", "!internal/", "!third_party/", "**/*_test.go", "**/testdata/"):
            self.assertIn(want, lines)
        for ln in lines:
            self.assertFalse(ln.startswith("internal/stage"), f"internal/stage を除く行がある: {ln}")
            self.assertFalse(ln.startswith("/internal/stage"), f"internal/stage を除く行がある: {ln}")


class SmokeScriptTest(unittest.TestCase):
    def setUp(self) -> None:
        self.text = SMOKE.read_text(encoding="utf-8")

    def test_d8_uses_dev_env_names_and_ephemeral_port(self) -> None:
        self.assertIn("dev-env.sh env", self.text)
        self.assertIn("-p 127.0.0.1::8080", self.text)
        self.assertNotIn("8080:8080", self.text)
        for name in ("DEV_DOCKER_IMAGE", "DEV_DOCKER_CONTAINER", "DEV_DOCKER_LABEL"):
            self.assertIn(name, self.text)

    def test_strict_mode_and_cleanup(self) -> None:
        self.assertIn("set -euo pipefail", self.text)
        self.assertRegex(self.text, r"trap \S+ EXIT")
        code = "\n".join(ln for ln in self.text.splitlines() if not ln.lstrip().startswith("#"))
        for bash4 in ("mapfile", "readarray", ",,}", "^^}"):
            self.assertNotIn(bash4, code, f"bash 3.2 で動かない: {bash4}")


if __name__ == "__main__":
    unittest.main()
