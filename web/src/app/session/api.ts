// セッションの作成（T-413・プロトコル §2 の POST /api/sessions）。
// トークン（Turnstile とセッションの両方）は本文と戻り値だけに置き、保存・ログ・例外のメッセージに出さない。
import type { SessionHandle } from "../types.ts";
import { SessionCreateError, sessionErrorKindOf } from "./errors.ts";

export type SessionRequest = {
  stageId: string;
  seed: number;
  demo: boolean;
  turnstileToken: string | null;
};

const SESSIONS_PATH = "/api/sessions";
const WS_PATH_PREFIX = "/ws/sessions/";
const SESSION_ID_PATTERN = /^[A-Za-z0-9._-]{1,256}$/;
const SESSION_TOKEN_PATTERN = /^[A-Za-z0-9_-]{22}$/;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// キーの順は stageId・seed・demo・turnstileToken。demo は true のときだけ、トークンは null でないときだけ。
function requestBody(request: SessionRequest): string {
  const body: Record<string, unknown> = { stageId: request.stageId, seed: request.seed };
  if (request.demo) {
    body.demo = true;
  }
  if (request.turnstileToken !== null) {
    body.turnstileToken = request.turnstileToken;
  }
  return JSON.stringify(body);
}

function toSessionHandle(value: unknown): SessionHandle | null {
  if (!isRecord(value)) {
    return null;
  }
  const { sessionId, token, wsPath } = value;
  if (
    typeof sessionId !== "string" ||
    !SESSION_ID_PATTERN.test(sessionId) ||
    typeof token !== "string" ||
    !SESSION_TOKEN_PATTERN.test(token) ||
    wsPath !== WS_PATH_PREFIX + sessionId
  ) {
    return null;
  }
  return { sessionId, token, wsPath };
}

async function readJson(response: Response): Promise<{ ok: true; value: unknown } | { ok: false }> {
  try {
    return { ok: true, value: await response.json() };
  } catch {
    return { ok: false };
  }
}

export async function createSession(
  request: SessionRequest,
  options?: { fetch?: typeof fetch },
): Promise<SessionHandle> {
  const doFetch = options?.fetch ?? fetch;
  let response: Response;
  try {
    // 中断の signal は渡さない（送った後に中断しても、Worker では作られることがあるため）。
    response = await doFetch(SESSIONS_PATH, {
      method: "POST",
      headers: { "content-type": "application/json", accept: "application/json" },
      body: requestBody(request),
    });
  } catch {
    throw new SessionCreateError("unavailable");
  }
  const body = await readJson(response);
  if (response.status === 201) {
    const handle = body.ok ? toSessionHandle(body.value) : null;
    if (handle === null) {
      throw new SessionCreateError("unavailable");
    }
    return handle;
  }
  const code =
    body.ok && isRecord(body.value) && typeof body.value.code === "string"
      ? body.value.code
      : undefined;
  throw new SessionCreateError(sessionErrorKindOf(response.status, code));
}
