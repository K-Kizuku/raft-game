// CostGuard の部品（T-309）。T-307 の既定の配線（常に undefined）を置き換える。
// COST_GUARD がない・形が違う間は undefined で、ルーターは POST /api/sessions を 503、
// /api/admin/guards を 404 にする。

import type { Env } from "../routes/env.ts";
import type { CostGuardPorts } from "../routes/ports.ts";
import { getCostGuardClient, type ContainerInstance } from "./client.ts";
import { handleAdmin } from "./handler.ts";

// "lite" のときだけ lite。それ以外（未設定・"standard-1"・不正な値）は高い方で予約する（安全側）。
export function containerInstanceOf(env: Env): ContainerInstance {
  return env["CONTAINER_INSTANCE"] === "lite" ? "lite" : "standard-1";
}

export const createCostGuardPorts: (env: Env) => CostGuardPorts | undefined = (env) => {
  const client = getCostGuardClient(env);
  if (client === undefined) return undefined;
  return {
    reserve: (sessionId, ctx) => client.reserve(sessionId, containerInstanceOf(ctx.env)),
    cancel: (sessionId) => client.cancel(sessionId),
    handleAdmin: (ctx) => handleAdmin(ctx, client),
  };
};
