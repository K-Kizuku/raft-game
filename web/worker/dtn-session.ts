// Durable Object DtnSession（Session Front。DTN-12・docs/dtn/environment.md §4・ADR-0023）。
//
// 1 ゲームに 1 つ（idFromName(sessionId)）。DurableObject を継承せず、fetch のハンドラーで内部の API を
// 受ける。中身は dtn/session/core.ts の DtnSessionCore。Worker は署名を確かめた sessionId でだけ呼ぶ（G3）。

import { getCostGuardClient } from "./admin/client.ts";
import { containerInstanceOf } from "./admin/ports.ts";
import type { Env } from "./routes/env.ts";
import { DTN_STAGE_INFO } from "./dtn/catalog.ts";
import type { DtnStateLike } from "./dtn/cf-types.ts";
import { contactKeyOf } from "./dtn/contact.ts";
import { hostCaller, unavailable } from "./dtn/namespaces.ts";
import {
  DEFAULT_COORDINATOR_START_TIMEOUT_MS,
  DEFAULT_DTN_START_TIMEOUT_MS,
  DtnSessionCore,
  type DtnSessionDeps,
} from "./dtn/session/core.ts";

export type { DtnSessionDeps } from "./dtn/session/core.ts";

const POSITIVE_INTEGER = /^[1-9][0-9]*$/;

// DTN_START_TIMEOUT_MS（全体の起動の上限。Q-COLD）。なし・不正・範囲外（10 秒〜5 分）なら既定値。
export function dtnStartTimeoutMsOf(env: Env): number {
  const raw = env["DTN_START_TIMEOUT_MS"];
  if (typeof raw !== "string" || !POSITIVE_INTEGER.test(raw)) return DEFAULT_DTN_START_TIMEOUT_MS;
  const value = Number(raw);
  return value >= 10_000 && value <= 300_000 ? value : DEFAULT_DTN_START_TIMEOUT_MS;
}

function defaultDeps(env: Env): DtnSessionDeps {
  return {
    now: () => Date.now(),
    sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
    log: (line) => console.log(line),
    timeoutSignal: (ms) => AbortSignal.timeout(Math.max(1, Math.ceil(ms))),
    instance: containerInstanceOf(env),
    contactKey: contactKeyOf(env),
    startTimeoutMs: dtnStartTimeoutMsOf(env),
    coordinatorStartTimeoutMs: DEFAULT_COORDINATOR_START_TIMEOUT_MS,
    callHost: hostCaller(env) ?? unavailable,
    costGuard: getCostGuardClient(env),
    catalog: DTN_STAGE_INFO,
  };
}

export class DtnSession {
  readonly #core: DtnSessionCore;

  constructor(ctx: DtnStateLike, env: Env, deps: Partial<DtnSessionDeps> = {}) {
    this.#core = new DtnSessionCore(ctx, { ...defaultDeps(env), ...deps });
  }

  fetch(request: Request): Promise<Response> {
    return this.#core.fetch(request);
  }

  alarm(): Promise<void> {
    return this.#core.alarm();
  }

  // テスト用
  drain(): Promise<void> {
    return this.#core.drain();
  }
}
