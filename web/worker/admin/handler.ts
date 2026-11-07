// /api/admin/guards の中身（T-309・プロトコル §2）。ルーター（T-307）がパス・メソッド・Origin を
// 確かめた後に呼ぶ。
//
// 順序: ADMIN_TOKEN の形（404）→ 認証（401）→ GET は view、PATCH は本文の検証（400）→ patch。
// 認証を通るまで本文を読まず、CostGuard を呼ばない。ログは出さない。

import { errorResponse, json, readJsonBody } from "../routes/http.ts";
import type { RequestContext } from "../routes/ports.ts";
import { checkAdminAuth } from "./auth.ts";
import type { CostGuardClient } from "./client.ts";
import { parseGuardsPatch } from "./guards.ts";

const MAX_BODY_BYTES = 4096;
export const ADMIN_ACTOR = "admin";

function guardsInvalid(): Response {
  return errorResponse(503, "guards_invalid", "Guards configuration is invalid");
}

function patchError(code: "bad_request" | "locked"): Response {
  return code === "locked"
    ? errorResponse(400, "locked", "Guard is locked")
    : errorResponse(400, "bad_request", "Invalid request body");
}

export async function handleAdmin(ctx: RequestContext, client: CostGuardClient): Promise<Response> {
  const auth = await checkAdminAuth(ctx.request, ctx.env);
  if (auth === "disabled") return errorResponse(404, "not_found", "Not found");
  if (auth !== "ok") {
    return errorResponse(401, "unauthorized", "Unauthorized", { "www-authenticate": "Bearer" });
  }

  if (ctx.request.method === "GET") {
    const view = await client.view();
    if (view === "guards_invalid") return guardsInvalid();
    return json(view, 200);
  }

  if (ctx.request.method !== "PATCH") {
    return errorResponse(405, "method_not_allowed", "Method not allowed", {
      allow: "GET, PATCH",
    });
  }

  const read = await readJsonBody(ctx.request, MAX_BODY_BYTES);
  if (!read.ok) return patchError("bad_request");
  const parsed = parseGuardsPatch(read.value);
  if (!parsed.ok) return patchError(parsed.code);

  const result = await client.patch(parsed.change, parsed.reason, ADMIN_ACTOR);
  if (result.ok) return json(result.view, 200);
  if (result.status === 503) return guardsInvalid();
  if (result.code === "guards_invalid") throw new Error("CostGuard: unexpected patch result");
  return patchError(result.code);
}
