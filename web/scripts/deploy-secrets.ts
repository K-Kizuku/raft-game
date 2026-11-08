// 環境変数から、cf deploy --secrets-file に渡す JSON をモードごとに作る（T-603 / #67）。
// .github/workflow-sources/deploy.yml のデプロイの手順だけが使う。
//
//   node web/scripts/deploy-secrets.ts --mode <production|demo|dtn> --out <絶対パス>
//
// 読む環境変数: CF_SECRET_SESSION_SIGNING_KEY_PRODUCTION・CF_SECRET_SESSION_SIGNING_KEY_DEMO・
//               CF_SECRET_ADMIN_TOKEN・CF_SECRET_TURNSTILE_SECRET
//               dtn（DTN-12・docs/dtn/environment.md §5 の H3・H4）: CF_SECRET_SESSION_SIGNING_KEY_DTN・
//               CF_SECRET_TURNSTILE_SECRET_DTN・CF_SECRET_ADMIN_TOKEN_DTN・CF_SECRET_DTN_CONTACT_KEY
//               （prod・demo と別の値。SESSION_SIGNING_KEY は prod・demo と同じ値を断る）
// 成功: --out に JSON を権限 0600 で書き、{"ok":true,"mode":"<モード>","keys":[<キー名>…]} を標準出力に出して 0。
// 失敗: 理由を 1 行ずつ（変数名で始める）標準エラーに出して 1。ファイルを作らない。
// 引数の誤り（モード・--out がない・相対パス・既にある・親のディレクトリがない）→ 2。
//
// 値・値の長さ・ファイルの中身は、どこにも出力しない。
// 値の規則は Worker が読むときと同じ（web/worker/security/ports.ts・turnstile.ts）。合わない値は
// Worker が「設定されていない」とみなすので、デプロイの前に止める。
import { existsSync, statSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const DEPLOY_MODES = ["production", "demo", "dtn"] as const;
export type DeployMode = (typeof DEPLOY_MODES)[number];

type Env = Record<string, string | undefined>;

export type BuildResult =
  { ok: true; secrets: Record<string, string> } | { ok: false; errors: string[] };

interface Rule {
  pattern: RegExp;
  text: string;
}

const KEY_RULE: Rule = {
  pattern: /^[\x21-\x7E]{32,256}$/,
  text: "32〜256 文字の空白を含まない ASCII（^[\\x21-\\x7E]{32,256}$）",
};
const TURNSTILE_RULE: Rule = {
  pattern: /^[\x21-\x7E]{1,256}$/,
  text: "1〜256 文字の空白を含まない ASCII（^[\\x21-\\x7E]{1,256}$）",
};

interface Source {
  key: string; // --secrets-file の JSON のキー（Worker のシークレットの名前）
  variable: string; // 読む環境変数
  secretName: string; // GitHub の Secrets の名前
  rule: Rule;
}

const SIGNING_KEY_PRODUCTION = "CF_SECRET_SESSION_SIGNING_KEY_PRODUCTION";
const SIGNING_KEY_DEMO = "CF_SECRET_SESSION_SIGNING_KEY_DEMO";
const SIGNING_KEY_DTN = "CF_SECRET_SESSION_SIGNING_KEY_DTN";

/** モードごとの出力のキー（この順に並べる） */
const SOURCES: Record<DeployMode, Source[]> = {
  production: [
    {
      key: "SESSION_SIGNING_KEY",
      variable: SIGNING_KEY_PRODUCTION,
      secretName: "SESSION_SIGNING_KEY",
      rule: KEY_RULE,
    },
    {
      key: "TURNSTILE_SECRET",
      variable: "CF_SECRET_TURNSTILE_SECRET",
      secretName: "TURNSTILE_SECRET",
      rule: TURNSTILE_RULE,
    },
    {
      key: "ADMIN_TOKEN",
      variable: "CF_SECRET_ADMIN_TOKEN",
      secretName: "ADMIN_TOKEN",
      rule: KEY_RULE,
    },
  ],
  demo: [
    {
      key: "SESSION_SIGNING_KEY",
      variable: SIGNING_KEY_DEMO,
      secretName: "DEMO_SESSION_SIGNING_KEY",
      rule: KEY_RULE,
    },
    {
      key: "TURNSTILE_SECRET",
      variable: "CF_SECRET_TURNSTILE_SECRET",
      secretName: "TURNSTILE_SECRET",
      rule: TURNSTILE_RULE,
    },
  ],
  dtn: [
    {
      key: "SESSION_SIGNING_KEY",
      variable: SIGNING_KEY_DTN,
      secretName: "DTN_SESSION_SIGNING_KEY",
      rule: KEY_RULE,
    },
    {
      key: "TURNSTILE_SECRET",
      variable: "CF_SECRET_TURNSTILE_SECRET_DTN",
      secretName: "DTN_TURNSTILE_SECRET",
      rule: TURNSTILE_RULE,
    },
    {
      key: "ADMIN_TOKEN",
      variable: "CF_SECRET_ADMIN_TOKEN_DTN",
      secretName: "DTN_ADMIN_TOKEN",
      rule: KEY_RULE,
    },
    {
      key: "DTN_CONTACT_KEY",
      variable: "CF_SECRET_DTN_CONTACT_KEY",
      secretName: "DTN_CONTACT_KEY",
      rule: KEY_RULE,
    },
  ],
};

/** 値の誤り（なければ undefined）。文に値も値の長さも入れない */
function validate(src: Source, value: string | undefined): string | undefined {
  if (value === undefined || value === "") {
    return `${src.variable}: 設定されていない（GitHub の Secrets の ${src.secretName} を登録する）`;
  }
  if (/[\r\n]$/.test(value)) return `${src.variable}: 末尾に改行がある`;
  if (!src.rule.pattern.test(value)) return `${src.variable}: ${src.rule.text}に合わない`;
  return undefined;
}

export function buildSecrets(mode: DeployMode, env: Env): BuildResult {
  const errors: string[] = [];
  const secrets: Record<string, string> = {};
  for (const src of SOURCES[mode]) {
    const value = env[src.variable];
    const err = validate(src, value);
    if (err !== undefined) {
      errors.push(err);
      continue;
    }
    if (
      src.variable === SIGNING_KEY_DEMO &&
      env[SIGNING_KEY_PRODUCTION] !== undefined &&
      env[SIGNING_KEY_PRODUCTION] !== "" &&
      env[SIGNING_KEY_PRODUCTION] === value
    ) {
      errors.push(`${SIGNING_KEY_DEMO}: 一般公開と同じ値は使えない`);
      continue;
    }
    if (
      src.variable === SIGNING_KEY_DTN &&
      [env[SIGNING_KEY_PRODUCTION], env[SIGNING_KEY_DEMO]].some((other) => other === value)
    ) {
      errors.push(`${SIGNING_KEY_DTN}: prod・demo と同じ値は使えない`);
      continue;
    }
    secrets[src.key] = value as string;
  }
  return errors.length > 0 ? { ok: false, errors } : { ok: true, secrets };
}

const USAGE = `使い方: node web/scripts/deploy-secrets.ts --mode <${DEPLOY_MODES.join("|")}> --out <絶対パス>\n`;

function isDeployMode(v: string): v is DeployMode {
  return (DEPLOY_MODES as readonly string[]).includes(v);
}

function usageError(msg: string): number {
  process.stderr.write(`deploy-secrets: ${msg}\n${USAGE}`);
  return 2;
}

function isDirectory(path: string): boolean {
  try {
    return statSync(path).isDirectory();
  } catch {
    return false;
  }
}

export function main(argv: string[], env: Env): number {
  let mode: string | undefined;
  let out: string | undefined;
  for (let i = 0; i < argv.length; i += 2) {
    const flag = argv[i];
    const value = argv[i + 1];
    if (value === undefined) return usageError(`${flag} の値がない`);
    if (flag === "--mode" && mode === undefined) mode = value;
    else if (flag === "--out" && out === undefined) out = value;
    else return usageError(`知らない引数: ${flag}`);
  }
  if (mode === undefined) return usageError("--mode がない");
  if (!isDeployMode(mode)) return usageError(`知らないモード: ${mode}`);
  if (out === undefined) return usageError("--out がない");
  if (!isAbsolute(out)) return usageError(`--out は絶対パスにする: ${out}`);
  if (existsSync(out)) return usageError(`--out が既にある（上書きしない）: ${out}`);
  if (!isDirectory(dirname(out))) return usageError(`--out の親のディレクトリがない: ${out}`);

  const result = buildSecrets(mode, env);
  if (!result.ok) {
    for (const msg of result.errors) process.stderr.write(`${msg}\n`);
    return 1;
  }
  try {
    writeFileSync(out, JSON.stringify(result.secrets), { mode: 0o600, flag: "wx" });
  } catch (err) {
    const code = (err as { code?: unknown }).code;
    return usageError(`--out に書けない（${typeof code === "string" ? code : "不明"}）: ${out}`);
  }
  process.stdout.write(
    `${JSON.stringify({ ok: true, mode, keys: Object.keys(result.secrets) })}\n`,
  );
  return 0;
}

if (process.argv[1] !== undefined && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = main(process.argv.slice(2), process.env);
}
