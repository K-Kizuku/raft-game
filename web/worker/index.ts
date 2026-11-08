// Worker の入り口（T-307）。静的ファイルは assets が配り、/api/* と /ws/* だけがここに届く
// （cloudflare.config.ts の runWorkerFirst）。
//
// 部品は各ディレクトリの ports.ts が作る。後続のタスクはこの配線を変えず、自分の ports.ts を
// 置き換える（T-310 は security/、T-309 は admin/、T-308 は session/）。

import { createCostGuardPorts } from "./admin/ports.ts";
import { createDtnPorts } from "./dtn/ports.ts";
import type { Env } from "./routes/env.ts";
import { createHandler } from "./routes/router.ts";
import { STAGE_CATALOG } from "./routes/stage-catalog.ts";
import { createSecurityPorts } from "./security/ports.ts";
import { createSessionPorts } from "./session/ports.ts";
export { ActorHost } from "./actor-host.ts";
export { CostGuard } from "./cost-guard.ts";
export { DtnSession } from "./dtn-session.ts";
export { GameSession } from "./game-session.ts";

export const handleRequest = createHandler((env) => {
  const security = createSecurityPorts(env);
  return {
    stages: STAGE_CATALOG,
    security,
    costGuard: createCostGuardPorts(env),
    session: createSessionPorts(env),
    dtn: createDtnPorts(env, security),
  };
});

export default {
  fetch: handleRequest,
} satisfies { fetch(request: Request, env: Env): Promise<Response> };
