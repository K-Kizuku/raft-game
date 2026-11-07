// IP ごとのレート制限（T-310・ADR-0018 G4・ADR-0024・プロトコル §2）。
//
// 回数（1 分に 10 回）は Rate Limiting の束縛 SESSION_RATE_LIMIT の設定
// （simple: { limit: 10, period: 60 }）で決まり、Worker の中では数えない。束縛は Cloudflare の
// 拠点ごとに数える近似の値。束縛がない・壊れている・例外のときは拒否する側に倒す。
//
// キー: IPv4 はそのまま、IPv6 は先頭の 4 グループ（/64）、IPv4 射影の IPv6 は IPv4 として扱う。
// どれにも当たらない（なし・ゾーン ID 付き・ほかの埋め込み表記など）は全員で "unknown" を共有する。

import type { Env } from "../routes/env.ts";

export type RateLimitResult = "ok" | "rate_limited";

const IPV4_PATTERN = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;
const IPV6_GROUP_PATTERN = /^[0-9A-Fa-f]{1,4}$/;
const IPV4_MAPPED_PREFIX = ["0", "0", "0", "0", "0", "ffff"] as const;
const UNKNOWN_KEY = "unknown";

// 16 進のグループを小文字にし、先頭の 0 を落とす（"0000" は "0"）。
function normalizeGroup(group: string): string {
  return Number.parseInt(group, 16).toString(16);
}

function splitGroups(part: string): string[] | null {
  if (part === "") return [];
  const groups = part.split(":");
  return groups.every((group) => IPV6_GROUP_PATTERN.test(group)) ? groups : null;
}

// 8 つのグループ（正規化済み）に展開する。展開できなければ null。
function expandIpv6(text: string, groupCount: number): string[] | null {
  const halves = text.split("::");
  if (halves.length > 2) return null;
  const head = splitGroups(halves[0] ?? "");
  if (head === null) return null;
  if (halves.length === 1) {
    return head.length === groupCount ? head.map(normalizeGroup) : null;
  }
  const tail = splitGroups(halves[1] ?? "");
  if (tail === null) return null;
  const missing = groupCount - head.length - tail.length;
  if (missing < 1) return null;
  return [...head, ...Array<string>(missing).fill("0"), ...tail].map(normalizeGroup);
}

export function rateLimitKey(clientIp: string | null): string {
  if (clientIp === null || clientIp === "") return UNKNOWN_KEY;
  if (IPV4_PATTERN.test(clientIp)) return `ip4:${clientIp}`;

  if (clientIp.includes(".")) {
    // IPv4 射影の IPv6（::ffff:a.b.c.d）だけを受け付ける。
    const index = clientIp.lastIndexOf(":");
    const ipv4 = clientIp.slice(index + 1);
    if (index < 0 || !IPV4_PATTERN.test(ipv4)) return UNKNOWN_KEY;
    const prefixText = clientIp.slice(0, index + 1);
    // 末尾の ":" を落とす（"::ffff:" → "::ffff"）。"::" で終わる場合はそのまま。
    const head = prefixText.endsWith("::") ? prefixText : prefixText.slice(0, -1);
    const prefix = expandIpv6(head, IPV4_MAPPED_PREFIX.length);
    if (prefix === null || prefix.some((group, i) => group !== IPV4_MAPPED_PREFIX[i])) {
      return UNKNOWN_KEY;
    }
    return `ip4:${ipv4}`;
  }

  const groups = expandIpv6(clientIp, 8);
  if (groups === null) return UNKNOWN_KEY;
  return `ip6:${groups.slice(0, 4).join(":")}`;
}

interface RateLimiterLike {
  limit(options: { key: string }): unknown;
}

function asRateLimiter(value: unknown): RateLimiterLike | undefined {
  if (typeof value !== "object" || value === null || !("limit" in value)) return undefined;
  const { limit } = value;
  if (typeof limit !== "function") return undefined;
  return { limit: (options) => limit.call(value, options) as unknown };
}

export interface RateLimitInput {
  readonly enabled: boolean; // rateLimit.enabled（CostGuard の設定）
  readonly env: Env;
  readonly clientIp: string | null;
}

export async function checkSessionRateLimit(input: RateLimitInput): Promise<RateLimitResult> {
  if (!input.enabled) return "ok";
  const limiter = asRateLimiter(input.env["SESSION_RATE_LIMIT"]);
  if (limiter === undefined) return "rate_limited";
  try {
    const result = await limiter.limit({ key: rateLimitKey(input.clientIp) });
    if (typeof result !== "object" || result === null) return "rate_limited";
    return "success" in result && result.success === true ? "ok" : "rate_limited";
  } catch {
    return "rate_limited";
  }
}
