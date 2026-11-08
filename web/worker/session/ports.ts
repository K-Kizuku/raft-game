// GameSession の部品（T-308）。T-307 の既定の配線（常に undefined）を置き換える。
// GAME_SESSION がない・形が違う間は undefined で、ルーターは POST /api/sessions を 503、
// /ws/sessions/{id} を 404 にする。
//
// init・connect は VerifiedSessionId しか受け取らない。ルーターが reserve の ok の後でだけ init を
// 呼び（G1）、verifySessionId の後でだけ connect を呼ぶ（G3）。

import { asDurableObjectNamespace } from "../admin/cf-types.ts";
import type { Env } from "../routes/env.ts";
import type { SessionPorts } from "../routes/ports.ts";
import { createGameSessionClient } from "./client.ts";

export const createSessionPorts: (env: Env) => SessionPorts | undefined = (env) => {
  const namespace = asDurableObjectNamespace(env["GAME_SESSION"]);
  if (namespace === undefined) return undefined;
  const client = createGameSessionClient(namespace);
  return {
    init: (sessionId, init) => client.init(sessionId, init),
    // 元の要求のほかのヘッダー（Cookie など）は渡さない。
    connect: (sessionId, ctx) => client.connect(sessionId, ctx.url.searchParams.get("token")),
    // トークンは URL ではなくヘッダー（X-Session-Token）で受ける（T-714）。ほかのヘッダーは渡さない。
    inputLog: (sessionId, ctx) =>
      client.inputLog(sessionId, ctx.request.headers.get("x-session-token") ?? ""),
  };
};
