// GET /ws/sessions/{id}（プロトコル §2）。
//
// sessionId の署名を確かめる前に Durable Object を呼ばない（G3）。connect は
// verifySessionId が null でない値を返したときだけ呼ぶ。Worker は WebSocket の電文を読まない。

import { errorResponse } from "./http.ts";
import type { RequestContext, WorkerDeps } from "./ports.ts";

// T-310 の sessionId（s_<base64url>.<base64url>）はこの範囲に収める。
const SESSION_ID_PATTERN = /^[A-Za-z0-9._-]{1,256}$/;

// id が不正・部品がない・検証に失敗、のどれも同じ 404 にする（手がかりを返さない）。
function notFound(): Response {
  return errorResponse(404, "not_found", "Not found");
}

export async function handleWebSocket(
  ctx: RequestContext,
  deps: WorkerDeps,
  rawId: string,
): Promise<Response> {
  if ((ctx.request.headers.get("upgrade") ?? "").toLowerCase() !== "websocket") {
    return errorResponse(426, "upgrade_required", "WebSocket upgrade required");
  }
  if (!SESSION_ID_PATTERN.test(rawId)) return notFound();

  const { security, session } = deps;
  if (security === undefined || session === undefined) return notFound();

  const verified = await security.verifySessionId(rawId, ctx);
  if (verified === null) return notFound();

  // 101 の応答は webSocket を持ち、作り直せないので包み直さない。
  return session.connect(verified, ctx);
}
