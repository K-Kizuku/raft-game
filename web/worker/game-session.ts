// Durable Object GameSession（T-308・DesignDoc §12.3・ADR-0016・ADR-0018・ADR-0023）。
//
// 1 ゲームに 1 つ（idFromName(sessionId)）。DurableObject を継承せず、fetch のハンドラーで内部の API を
// 受ける（Workers のランタイムのモジュールの import・RPC を使わない。ADR-0023）。中身は session/core.ts の
// GameSessionCore。
//
//   POST /init     {sessionId, stageId, seed, demo, tokenHash} → 201 {}・200 {}・400・409・500 start_failed
//   GET  /connect  Upgrade: websocket・X-Session-Token        → 101・404・426

import { getCostGuardClient } from "./admin/client.ts";
import type { Env } from "./routes/env.ts";
import type { GameSessionStateLike, WebSocketLike } from "./session/cf-types.ts";
import { GameSessionCore, type GameSessionDeps } from "./session/core.ts";

export type { GameSessionDeps } from "./session/core.ts";

type WebSocketPairConstructor = new () => { readonly 0: WebSocketLike; readonly 1: WebSocketLike };

function defaultDeps(env: Env): GameSessionDeps {
  return {
    now: () => Date.now(),
    sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
    log: (line) => console.log(line),
    createPair: () => {
      const Pair = (globalThis as unknown as { readonly WebSocketPair: WebSocketPairConstructor })
        .WebSocketPair;
      const pair = new Pair();
      return [pair[0], pair[1]];
    },
    upgradeResponse: (client) =>
      new Response(null, { status: 101, webSocket: client } as ResponseInit),
    timeoutSignal: (ms) => AbortSignal.timeout(Math.max(1, Math.ceil(ms))),
    costGuard: getCostGuardClient(env),
    setInterval: (callback, ms) => {
      const id = globalThis.setInterval(callback, ms);
      return () => globalThis.clearInterval(id);
    },
  };
}

export class GameSession {
  readonly #core: GameSessionCore;

  constructor(ctx: GameSessionStateLike, env: Env, deps: Partial<GameSessionDeps> = {}) {
    this.#core = new GameSessionCore(ctx, env, { ...defaultDeps(env), ...deps });
  }

  fetch(request: Request): Promise<Response> {
    return this.#core.fetch(request);
  }

  alarm(): Promise<void> {
    return this.#core.alarm();
  }

  // テスト用: 進行中の非同期の処理（コンテナへの接続・終わり）がすべて終わるまで待つ。
  drain(): Promise<void> {
    return this.#core.drain();
  }
}
