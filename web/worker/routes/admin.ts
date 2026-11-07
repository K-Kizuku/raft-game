// /api/admin/guards（プロトコル §2）。認証（ADMIN_TOKEN）と中身は T-309 の handleAdmin が行う。

import { errorResponse } from "./http.ts";
import type { RequestContext, WorkerDeps } from "./ports.ts";

export async function handleAdminGuards(ctx: RequestContext, deps: WorkerDeps): Promise<Response> {
  if (deps.costGuard === undefined) return errorResponse(404, "not_found", "Not found");
  return deps.costGuard.handleAdmin(ctx);
}
