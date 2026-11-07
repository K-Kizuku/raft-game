// GET /api/sessions/{id}/input-log（T-714・FR-LOG-03・プロトコル §2）。
//
// sessionId の署名を確かめる前に Durable Object を呼ばない（G3）。inputLog は verifySessionId が
// null でない値を返し、トークン（X-Session-Token）の形が合うときだけ呼ぶ。トークンは URL に入れない
// （ログに残らないように）。id が不正・部品がない・検証に失敗・トークンがない・形が違う、のどれも
// 同じ 404 にする（手がかりを返さない）。DO の応答（200 の添付ファイル・404）は包み直さずに返す。

import { SESSION_TOKEN_PATTERN } from "../security/token.ts";
import { errorResponse } from "./http.ts";
import type { RequestContext, WorkerDeps } from "./ports.ts";

// ws.ts と同じ範囲（T-310 の sessionId は s_<base64url>.<base64url>）。
const SESSION_ID_PATTERN = /^[A-Za-z0-9._-]{1,256}$/;

function notFound(): Response {
  return errorResponse(404, "not_found", "Not found");
}

export async function handleInputLog(
  ctx: RequestContext,
  deps: WorkerDeps,
  rawId: string,
): Promise<Response> {
  if (!SESSION_ID_PATTERN.test(rawId)) return notFound();

  const { security, session } = deps;
  if (security === undefined || session?.inputLog === undefined) return notFound();

  const verified = await security.verifySessionId(rawId, ctx);
  if (verified === null) return notFound();

  const token = ctx.request.headers.get("x-session-token");
  if (token === null || !SESSION_TOKEN_PATTERN.test(token)) return notFound();

  return session.inputLog(verified, ctx);
}
