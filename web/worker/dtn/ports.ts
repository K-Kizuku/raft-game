// DTN の部品（DTN-12）。DTN_SESSION・COST_GUARD・SESSION_SIGNING_KEY のどれかがなければ undefined で、
// ルーターは DTN の口を 404 にする。sessionId の署名の確認を外す道は作らない（G3）。

import { getCostGuardClient } from "../admin/client.ts";
import { containerInstanceOf } from "../admin/ports.ts";
import type { Env } from "../routes/env.ts";
import type { RequestContext, SecurityPorts, VerifiedSessionId } from "../routes/ports.ts";
import { readGameTimeLimits } from "../session/config.ts";
import { DTN_STAGE_INFO } from "./catalog.ts";
import { createDtnSession, type CreateDtnInput, type CreateDtnResult } from "./create.ts";
import { sessionCaller } from "./namespaces.ts";

export interface DtnPorts {
  create(input: CreateDtnInput, ctx: RequestContext): Promise<CreateDtnResult>;
  // DtnSession の GET /probe の応答（status と本文）
  probe(sessionId: VerifiedSessionId): Promise<{ status: number; body: unknown }>;
}

export function createDtnPorts(
  env: Env,
  security: SecurityPorts | undefined,
): DtnPorts | undefined {
  const callSession = sessionCaller(env);
  const costGuard = getCostGuardClient(env);
  if (callSession === undefined || costGuard === undefined || security === undefined)
    return undefined;
  const reserveUnits = costGuard.reserveUnits;
  if (reserveUnits === undefined) return undefined;
  return {
    create: (input, ctx) =>
      createDtnSession(input, ctx, {
        catalog: DTN_STAGE_INFO,
        instance: containerInstanceOf(env),
        issueSession: (c) => security.issueSession(c),
        reserveUnits: (sessionId, units) => reserveUnits(sessionId, units),
        cancel: (sessionId) => costGuard.cancel(sessionId),
        limits: () => readGameTimeLimits(costGuard),
        init: async (body) => {
          const result = await callSession(body.sessionId, "/init", body);
          if (result.status !== 201 && result.status !== 200)
            throw new Error("DtnSession: init failed");
        },
      }),
    probe: (sessionId) => callSession(sessionId, "/probe", undefined, "GET"),
  };
}
