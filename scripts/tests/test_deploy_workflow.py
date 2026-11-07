"""デプロイのワークフロー（.github/workflow-sources/deploy.yml）の静的な検査（T-603 / Issue #67、W-1〜W-8）。

実行: python3 -m unittest discover -s scripts/tests -p 'test_*.py'

PyYAML に頼らず（標準ライブラリだけ）、テキストと字下げで読む。読むのは、最上位のキー・
`jobs:` の直下（字下げ 2）のジョブ・各ジョブの `steps` の `- ` ごとの塊・`run:` の本文だけ。
"""

import re
import unittest
from pathlib import Path
from typing import Dict, List, Tuple

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
WORKFLOW = REPO_ROOT / ".github" / "workflow-sources" / "deploy.yml"

MODE_EXPR = "github.event_name == 'workflow_dispatch' && inputs.mode || 'production'"
PROD_HOST = "raft.kizuku-hackathon.work"
# 式の secrets コンテキストの参照（deploy-secrets.ts の "secrets." は含めない）
SECRET_REF = re.compile(r"(?<![A-Za-z0-9_-])secrets\.")


def indent_of(line: str) -> int:
    return len(line) - len(line.lstrip(" "))


def is_blank_or_comment(line: str) -> bool:
    s = line.strip()
    return s == "" or s.startswith("#")


def blocks(lines: List[str], indent: int) -> Dict[str, List[str]]:
    """字下げ indent の `key:` ごとに、その行から次の同じ字下げのキーの手前までを返す（順序を保つ）"""
    out: Dict[str, List[str]] = {}
    key = None
    for line in lines:
        if not is_blank_or_comment(line) and indent_of(line) == indent:
            m = re.match(r"^ *([A-Za-z0-9_-]+):", line)
            key = m.group(1) if m else None
            if key is not None:
                out[key] = []
        elif not is_blank_or_comment(line) and indent_of(line) < indent:
            key = None
        if key is not None:
            out[key].append(line)
    return out


def steps_of(job: List[str]) -> List[List[str]]:
    """ジョブの steps の `- ` ごとの塊"""
    sub = blocks(job, 4)
    steps: List[List[str]] = []
    for line in sub.get("steps", [])[1:]:
        if is_blank_or_comment(line):
            continue
        if indent_of(line) == 6 and line.lstrip().startswith("- "):
            steps.append([])
        if steps:
            steps[-1].append(line)
    return steps


def run_bodies(lines: List[str]) -> List[str]:
    """`run:` の本文（`run: |` の複数行と、1 行の `run: …`）"""
    bodies: List[str] = []
    i = 0
    while i < len(lines):
        line = lines[i]
        m = re.match(r"^( *)(?:- )?run: ?(.*)$", line)
        if m:
            base = indent_of(line)
            rest = m.group(2)
            if rest in ("|", ">", "|-", ">-"):
                body: List[str] = []
                i += 1
                while i < len(lines) and (lines[i].strip() == "" or indent_of(lines[i]) > base):
                    body.append(lines[i])
                    i += 1
                bodies.append("\n".join(body))
                continue
            bodies.append(rest)
        i += 1
    return bodies


def text(lines: List[str]) -> str:
    return "\n".join(lines)


def code_lines(lines: List[str]) -> List[str]:
    return [l for l in lines if not is_blank_or_comment(l)]


class DeployWorkflowTest(unittest.TestCase):
    src: str
    lines: List[str]
    top: Dict[str, List[str]]
    jobs: Dict[str, List[str]]

    @classmethod
    def setUpClass(cls) -> None:
        cls.src = WORKFLOW.read_text(encoding="utf-8")
        cls.lines = cls.src.splitlines()
        cls.top = blocks(cls.lines, 0)
        cls.jobs = blocks(cls.top["jobs"][1:], 2)

    def job_steps(self, job: str) -> List[List[str]]:
        return steps_of(self.jobs[job])

    def step_index(self, job: str, *needles: str) -> int:
        """すべての needles を含む最初の手順の番号"""
        for i, step in enumerate(self.job_steps(job)):
            t = text(step)
            if all(n in t for n in needles):
                return i
        self.fail(f"{job}: {needles} を含む手順がない")

    def step_with(self, job: str, *needles: str) -> List[str]:
        return self.job_steps(job)[self.step_index(job, *needles)]

    # W-1
    def test_w1_triggers(self) -> None:
        on = blocks(self.top["on"][1:], 2)
        self.assertEqual(list(on), ["pull_request", "push", "workflow_dispatch"])
        on_text = text(self.top["on"])
        for bad in ("pull_request_target", "schedule", "workflow_run"):
            self.assertNotIn(bad, on_text)
            self.assertNotIn(f"{bad}:", self.src)
        for ev in ("pull_request", "push"):
            self.assertIn("    branches: [main]", on[ev])

        def paths(ev: str) -> List[str]:
            sub = blocks(on[ev][1:], 4)
            return [l.strip() for l in code_lines(sub["paths"][1:])]

        self.assertEqual(paths("pull_request"), paths("push"))
        got = [p.removeprefix("- ").strip('"') for p in paths("push")]
        for want in (
            "web/**",
            "server/**",
            "data/**",
            "pnpm-lock.yaml",
            "Makefile",
            ".github/workflows/deploy.yml",
        ):
            self.assertIn(want, got)
        self.assertNotIn("docs/**", got)

        wd = text(on["workflow_dispatch"])
        self.assertRegex(wd, r"\n {6}mode:\n")
        self.assertIn("type: choice", wd)
        self.assertIn("options: [demo, production]", wd)
        self.assertIn("default: demo", wd)

        self.assertIsNone(re.search(r"&[A-Za-z]", self.src), "YAML のアンカーを使わない")
        self.assertIsNone(re.search(r": \*[A-Za-z]", self.src), "YAML の別名を使わない")
        self.assertNotIn("<<:", self.src)

    # W-2
    def test_w2_permissions(self) -> None:
        self.assertEqual(code_lines(self.top["permissions"]), ["permissions:", "  contents: read"])
        self.assertNotIn(": write", self.src)
        self.assertIn('  CF_SEND_TELEMETRY: "false"', self.top["env"])

    # W-3
    def test_w3_jobs(self) -> None:
        self.assertEqual(sorted(self.jobs), ["deploy", "dry-run"])
        ifs = {}
        for name, job in self.jobs.items():
            sub = blocks(job[1:], 4)
            self.assertIn("timeout-minutes", sub, name)
            self.assertIn("if", sub, name)
            ifs[name] = sub["if"][0]
        self.assertIn("github.event_name == 'pull_request'", ifs["dry-run"])
        self.assertNotIn("push", ifs["dry-run"])
        self.assertNotIn("workflow_dispatch", ifs["dry-run"])
        self.assertIn("refs/heads/main", ifs["deploy"])
        self.assertNotIn("pull_request", ifs["deploy"])

    # W-4
    def test_w4_dry_run(self) -> None:
        job = text(self.jobs["dry-run"])
        self.assertIsNone(SECRET_REF.search(job))
        for bad in ("--secrets-file", "CLOUDFLARE_API_TOKEN"):
            self.assertNotIn(bad, job)
        deploys = [l for l in self.jobs["dry-run"] if "exec cf deploy" in l]
        self.assertTrue(deploys)
        for l in deploys:
            self.assertIn("--prebuilt", l)
            self.assertIn("--dry-run", l)
        sub = blocks(self.jobs["dry-run"][1:], 4)
        self.assertIn("mode: [production, demo]", text(sub["strategy"]))
        self.assertIn("fail-fast: false", text(sub["strategy"]))
        self.assertIn("cancel-in-progress: true", text(sub["concurrency"]))

    # W-5
    def test_w5_secrets_placement(self) -> None:
        # deploy ジョブの行の範囲（ジョブは続いた行の塊）
        start = self.lines.index("  deploy:")
        end = start + len(self.jobs["deploy"])
        self.assertEqual(self.lines[start:end], self.jobs["deploy"])
        outside = self.lines[:start] + self.lines[end:]
        self.assertFalse([l for l in outside if SECRET_REF.search(l)])

        found: List[Tuple[str, int]] = []
        for i, step in enumerate(self.job_steps("deploy")):
            for line in step:
                if not SECRET_REF.search(line):
                    continue
                m = re.match(r"^ {10}[A-Z][A-Z0-9_]*: \$\{\{ secrets\.([A-Z][A-Z0-9_]*) \}\}$", line)
                self.assertIsNotNone(m, f"env の行の形でない: {line}")
                assert m is not None
                found.append((m.group(1), i))
                env_block = blocks(step[1:], 8).get("env", [])
                self.assertIn(line, env_block, f"手順の env にない: {line}")

        names = {n for n, _ in found}
        self.assertEqual(
            names,
            {
                "CLOUDFLARE_API_TOKEN",
                "CLOUDFLARE_ACCOUNT_ID",
                "SESSION_SIGNING_KEY",
                "DEMO_SESSION_SIGNING_KEY",
                "ADMIN_TOKEN",
                "TURNSTILE_SECRET",
            },
        )
        steps = self.job_steps("deploy")
        for name, i in found:
            body = "\n".join(run_bodies(steps[i]))
            if name.startswith("CLOUDFLARE_"):
                self.assertIn("cf deploy", body)
                self.assertIn("--secrets-file", body)
                self.assertNotIn("--dry-run", body)
            else:
                self.assertIn("deploy-secrets.ts", body)
        for bad in ("CLOUDFLARE_ACCESS_KEY_ID", "CLOUDFLARE_SECRET_ACCESS_KEY", "CLOUDFLARE_S3_API_ENDPOINT"):
            self.assertNotIn(bad, self.src)

    # W-6
    def test_w6_order_deploy(self) -> None:
        order = [
            self.step_index("deploy", "wait-healthz.ts", PROD_HOST, "if: env.MODE == 'demo'"),
            self.step_index("deploy", "make sync-data"),
            self.step_index("deploy", 'cf build --mode "$MODE"'),
            self.step_index("deploy", 'check-build-output.ts --mode "$MODE"'),
            self.step_index("deploy", 'cf deploy --prebuilt --dry-run --mode "$MODE"'),
            self.step_index("deploy", 'deploy-secrets.ts --mode "$MODE"'),
            self.step_index("deploy", 'cf deploy --prebuilt --mode "$MODE" --secrets-file'),
            self.step_index("deploy", "rm -f", "if: always()"),
            self.step_index("deploy", "wait-healthz.ts", '--build "$BUILD_ID"'),
        ]
        self.assertEqual(order, sorted(set(order)), order)

    def test_w6_order_dry_run(self) -> None:
        order = [
            self.step_index("dry-run", "make sync-data"),
            self.step_index("dry-run", 'cf build --mode "$MODE"'),
            self.step_index("dry-run", 'check-build-output.ts --mode "$MODE"'),
            self.step_index("dry-run", 'cf deploy --prebuilt --dry-run --mode "$MODE"'),
        ]
        self.assertEqual(order, sorted(set(order)), order)

    def test_w6_mode_everywhere(self) -> None:
        for job in self.jobs:
            for body in run_bodies(self.jobs[job]):
                for line in body.splitlines():
                    if re.search(r"exec cf (build|deploy)|check-build-output\.ts|deploy-secrets\.ts", line):
                        self.assertIn('--mode "$MODE"', line, f"{job}: {line}")

    # W-7
    def test_w7_style(self) -> None:
        for body in run_bodies(self.lines):
            self.assertNotIn("${{", body)
            if "\n" in body.strip():
                self.assertEqual(body.strip().splitlines()[0], "set -euo pipefail", body)
        for bad in ("set -x", 'cat "$CF_SECRETS_FILE"', "printenv", "actions/upload-artifact", "actions/cache", "cache:"):
            self.assertNotIn(bad, self.src)
        uses = [l.strip() for l in self.lines if re.match(r"^ *(- )?uses: ", l)]
        self.assertTrue(uses)
        for u in uses:
            self.assertTrue(u.removeprefix("- ").startswith("uses: actions/"), u)
        for job in self.jobs:
            checkouts = [s for s in self.job_steps(job) if "uses: actions/checkout@" in text(s)]
            self.assertTrue(checkouts, job)
            for s in checkouts:
                self.assertIn("persist-credentials: false", text(s))

    def test_w7_turnstile_site_key(self) -> None:
        for job in self.jobs:
            step = self.step_with(job, 'cf build --mode "$MODE"')
            self.assertIn("          VITE_TURNSTILE_SITE_KEY: ${{ vars.TURNSTILE_SITE_KEY }}", step)
        deploy_build = text(self.step_with("deploy", 'cf build --mode "$MODE"'))
        self.assertIn('-z "${VITE_TURNSTILE_SITE_KEY:-}"', deploy_build)
        self.assertIn("exit 1", deploy_build)

    # W-8
    def test_w8_deploy_concurrency_environment(self) -> None:
        sub = blocks(self.jobs["deploy"][1:], 4)
        conc = blocks(sub["concurrency"][1:], 6)
        self.assertEqual(code_lines(conc["cancel-in-progress"]), ["      cancel-in-progress: false"])
        group = conc["group"][0].split(":", 1)[1].strip()
        self.assertTrue(group.startswith("deploy-"), group)
        self.assertIn(MODE_EXPR, group)
        env = blocks(sub["environment"][1:], 6)
        self.assertIn("name", env)
        self.assertIn("url", env)
        self.assertIn(MODE_EXPR, env["name"][0])
        self.assertIn("https://", env["url"][0])
        self.assertIn(f"MODE: ${{{{ {MODE_EXPR} }}}}", text(sub["env"]))


if __name__ == "__main__":
    unittest.main()
