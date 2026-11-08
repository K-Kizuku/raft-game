// Durable Object ActorHost（DTN-12・docs/dtn/persistence-and-recovery.md・ADR-0023）。
//
// 1 Actor（島・船）に 1 つ（idFromName("<sessionId>/<actor>")）。DurableObject を継承せず、fetch の
// ハンドラーで内部の API を受ける。中身は dtn/host/core.ts の ActorHostCore。ブラウザからは届かない
// （Worker のルーターはこの DO を呼ばない。呼ぶのは DtnSession と、RPC CLA の相手の ActorHost だけ）。

import { containerInstanceOf } from "./admin/ports.ts";
import type { Env } from "./routes/env.ts";
import type { DtnStateLike } from "./dtn/cf-types.ts";
import { contactKeyOf } from "./dtn/contact.ts";
import { ActorHostCore, DEFAULT_HOST_DEPS, type ActorHostDeps } from "./dtn/host/core.ts";
import { hostCaller, sessionCaller, unavailable } from "./dtn/namespaces.ts";

export type { ActorHostDeps } from "./dtn/host/core.ts";

function defaultDeps(env: Env): ActorHostDeps {
  const callHost = hostCaller(env);
  const callSession = sessionCaller(env);
  return {
    ...DEFAULT_HOST_DEPS,
    now: () => Date.now(),
    sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
    log: (line) => console.log(line),
    timeoutSignal: (ms) => AbortSignal.timeout(Math.max(1, Math.ceil(ms))),
    setTimer: (callback, ms) => {
      const id = setTimeout(callback, ms);
      return () => clearTimeout(id);
    },
    instance: containerInstanceOf(env),
    contactKey: contactKeyOf(env),
    callHost: callHost ?? unavailable,
    callSession:
      callSession === undefined ? unavailable : (id, path, body) => callSession(id, path, body),
  };
}

export class ActorHost {
  readonly #core: ActorHostCore;

  constructor(ctx: DtnStateLike, env: Env, deps: Partial<ActorHostDeps> = {}) {
    this.#core = new ActorHostCore(ctx, { ...defaultDeps(env), ...deps });
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
