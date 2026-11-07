// Worker から GameSession（Durable Object）を呼ぶクライアント（T-308・ADR-0023）。
//
// GameSession は 1 ゲームに 1 つ（idFromName(sessionId)）。スタブは呼ぶたびに作る。
// init・connect が呼ばれるまで idFromName も get も呼ばない（G3: 署名を確かめる前に DO に触れない）。
// トークンは URL ではなくヘッダー（X-Session-Token）で渡す（DO の要求の URL がログに残っても出ないように）。

import type { DurableObjectNamespaceLike } from "../admin/cf-types.ts";
import type { SessionInit, VerifiedSessionId } from "../routes/ports.ts";

const BASE_URL = "https://game-session.internal";

export interface GameSessionClient {
  // 201・200 なら解決し、それ以外（400・409・500・本文が JSON でない）なら Error を投げる。
  init(sessionId: VerifiedSessionId, init: SessionInit): Promise<void>;
  // DO の応答（101 など）を包み直さずにそのまま返す。token が null ならヘッダーを付けない。
  connect(sessionId: VerifiedSessionId, token: string | null): Promise<Response>;
  // GET /input-log（T-714）。DO の応答（200・404 など）を包み直さずにそのまま返す。
  inputLog(sessionId: VerifiedSessionId, token: string): Promise<Response>;
}

export function createGameSessionClient(namespace: DurableObjectNamespaceLike): GameSessionClient {
  const stubOf = (sessionId: VerifiedSessionId) => namespace.get(namespace.idFromName(sessionId));

  return {
    async init(sessionId, init) {
      const body = {
        sessionId,
        stageId: init.stageId,
        seed: init.seed,
        demo: init.demo,
        tokenHash: init.tokenHash,
      };
      const response = await stubOf(sessionId).fetch(
        new Request(`${BASE_URL}/init`, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify(body),
        }),
      );
      let parsed: unknown;
      try {
        parsed = await response.json();
      } catch {
        throw new Error("GameSession: unexpected response from init");
      }
      const ok = response.status === 201 || response.status === 200;
      if (!ok || typeof parsed !== "object" || parsed === null) {
        throw new Error("GameSession: init failed");
      }
    },

    async connect(sessionId, token) {
      const headers: Record<string, string> = { upgrade: "websocket" };
      if (token !== null) headers["x-session-token"] = token;
      return stubOf(sessionId).fetch(
        new Request(`${BASE_URL}/connect`, { method: "GET", headers }),
      );
    },

    async inputLog(sessionId, token) {
      return stubOf(sessionId).fetch(
        new Request(`${BASE_URL}/input-log`, {
          method: "GET",
          headers: { "x-session-token": token },
        }),
      );
    },
  };
}
