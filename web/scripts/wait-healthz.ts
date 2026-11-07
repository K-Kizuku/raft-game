// https://<ホスト名>/api/healthz が 200 で、build が期待した値になるまで待つ（T-603 / #67）。
// .github/workflow-sources/deploy.yml が、デプロイの後と、demo のデプロイの前（一般公開が動いているか）に使う。
//
//   node web/scripts/wait-healthz.ts --url <https://…/api/healthz> [--build <BUILD_ID>] [--timeout <秒、既定 600>] [--interval <秒、既定 15>]
//
// 成功: {"ok":true,"url":"…","build":"…","attempts":<n>} を標準出力に出して 0。
// 失敗（時間切れ）: 最後の理由を出して 1。
// 引数の誤り（https 以外・--build が ^[A-Za-z0-9._:-]{1,64}$ に合わない・秒が 0 以上の整数でない・
// --interval が 1 未満）→ 2。
//
// 応答は web/worker/routes/health.ts の { status: "ok", build }。本文はそのまま出さず、status・build の値だけを出す。
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const BUILD_ID_RE = /^[A-Za-z0-9._:-]{1,64}$/;
const REQUEST_TIMEOUT_MS = 10000;
const SHOW_MAX = 64;

export interface FetchInit {
  redirect: "manual";
  signal: AbortSignal;
}

export interface FetchResponse {
  status: number;
  text(): Promise<string>;
}

export type FetchFn = (url: string, init: FetchInit) => Promise<FetchResponse>;

export interface WaitOptions {
  url: string;
  build?: string;
  timeoutMs: number;
  intervalMs: number;
  fetch: FetchFn;
  sleep: (ms: number) => Promise<void>;
  now: () => number;
  /** 試すたびの 1 行（既定は標準エラー） */
  log?: (line: string) => void;
}

export type WaitResult =
  { ok: true; attempts: number } | { ok: false; attempts: number; reason: string };

/** 応答の値を短く見せる（文字列は 64 文字まで。ほかは型だけ） */
function show(v: unknown): string {
  if (typeof v === "string") {
    return JSON.stringify(v.length > SHOW_MAX ? `${v.slice(0, SHOW_MAX)}…` : v);
  }
  if (v === undefined) return "なし";
  if (v === null || typeof v === "number" || typeof v === "boolean") return String(v);
  return Array.isArray(v) ? "配列" : typeof v;
}

/** 合えば undefined、合わなければ理由 */
export function checkHealthz(
  status: number,
  body: string,
  expectedBuild: string | undefined,
): string | undefined {
  if (status !== 200) return `HTTP ${status}（200 であること）`;
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch {
    return "本文が JSON でない";
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return "本文が JSON のオブジェクトでない";
  }
  const obj = parsed as Record<string, unknown>;
  if (obj["status"] !== "ok") return `status が ${show(obj["status"])}（"ok" であること）`;
  if (expectedBuild !== undefined && obj["build"] !== expectedBuild) {
    return `build が ${show(obj["build"])}（${JSON.stringify(expectedBuild)} であること）`;
  }
  return undefined;
}

function errorText(err: unknown): string {
  if (!(err instanceof Error)) return String(err);
  const cause = (err as { cause?: unknown }).cause;
  const code =
    typeof cause === "object" && cause !== null ? (cause as { code?: unknown }).code : undefined;
  return typeof code === "string"
    ? `${err.name}: ${err.message}（${code}）`
    : `${err.name}: ${err.message}`;
}

export async function waitHealthz(opts: WaitOptions): Promise<WaitResult> {
  const log = opts.log ?? ((line: string) => void process.stderr.write(`${line}\n`));
  const start = opts.now();
  let attempts = 0;
  for (;;) {
    attempts++;
    let reason: string | undefined;
    try {
      const res = await opts.fetch(opts.url, {
        redirect: "manual",
        signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
      });
      reason = checkHealthz(res.status, await res.text(), opts.build);
    } catch (err) {
      reason = `要求に失敗した: ${errorText(err)}`;
    }
    if (reason === undefined) {
      log(`wait-healthz: ${attempts} 回目: 合う`);
      return { ok: true, attempts };
    }
    log(`wait-healthz: ${attempts} 回目: ${reason}`);
    if (opts.now() - start + opts.intervalMs > opts.timeoutMs) {
      return { ok: false, attempts, reason };
    }
    await opts.sleep(opts.intervalMs);
  }
}

const USAGE =
  "使い方: node web/scripts/wait-healthz.ts --url <https://…/api/healthz> [--build <BUILD_ID>] [--timeout <秒、既定 600>] [--interval <秒、既定 15>]\n";

export interface MainDeps {
  fetch: FetchFn;
  sleep: (ms: number) => Promise<void>;
  now: () => number;
  stdout: (s: string) => void;
  stderr: (s: string) => void;
}

const defaultDeps: MainDeps = {
  fetch: (url, init) => fetch(url, init),
  sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
  now: () => Date.now(),
  stdout: (s) => void process.stdout.write(s),
  stderr: (s) => void process.stderr.write(s),
};

function parseSeconds(v: string): number | undefined {
  return /^[0-9]+$/.test(v) ? Number(v) : undefined;
}

export async function main(argv: string[], deps: MainDeps = defaultDeps): Promise<number> {
  const usageError = (msg: string): number => {
    deps.stderr(`wait-healthz: ${msg}\n${USAGE}`);
    return 2;
  };
  const args = new Map<string, string>();
  for (let i = 0; i < argv.length; i += 2) {
    const flag = argv[i] as string;
    const value = argv[i + 1];
    if (!["--url", "--build", "--timeout", "--interval"].includes(flag) || args.has(flag)) {
      return usageError(`知らない引数: ${flag}`);
    }
    if (value === undefined) return usageError(`${flag} の値がない`);
    args.set(flag, value);
  }
  const url = args.get("--url");
  if (url === undefined) return usageError("--url がない");
  let parsedUrl: URL;
  try {
    parsedUrl = new URL(url);
  } catch {
    return usageError(`--url が URL でない: ${url}`);
  }
  if (parsedUrl.protocol !== "https:") return usageError(`--url は https にする: ${url}`);
  const build = args.get("--build");
  if (build !== undefined && !BUILD_ID_RE.test(build)) {
    return usageError(`--build が ${BUILD_ID_RE.source} に合わない`);
  }
  const timeout = parseSeconds(args.get("--timeout") ?? "600");
  if (timeout === undefined) return usageError("--timeout は 0 以上の整数（秒）にする");
  const interval = parseSeconds(args.get("--interval") ?? "15");
  if (interval === undefined || interval < 1) {
    return usageError("--interval は 1 以上の整数（秒）にする");
  }

  const result = await waitHealthz({
    url,
    build,
    timeoutMs: timeout * 1000,
    intervalMs: interval * 1000,
    fetch: deps.fetch,
    sleep: deps.sleep,
    now: deps.now,
    log: (line) => deps.stderr(`${line}\n`),
  });
  if (!result.ok) {
    deps.stderr(
      `wait-healthz: ${timeout} 秒で合わなかった（${result.attempts} 回）: ${result.reason}\n`,
    );
    return 1;
  }
  deps.stdout(
    `${JSON.stringify({ ok: true, url, build: build ?? null, attempts: result.attempts })}\n`,
  );
  return 0;
}

if (process.argv[1] !== undefined && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  void main(process.argv.slice(2)).then((code) => {
    process.exitCode = code;
  });
}
