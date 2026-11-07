// 入力ログの取得（T-714・FR-LOG-03・プロトコル §2 の GET /api/sessions/{id}/input-log）。
// トークンは URL に入れず X-Session-Token で送る。トークン・本文を console・例外のメッセージに出さない。
import type { SessionHandle } from "../types.ts";

export type InputLogFetchResult =
  | { ok: true; blob: Blob; filename: string }
  | { ok: false; reason: "not_found" | "network" | "bad_response" };

const DEFAULT_FILENAME = "raft-game-input-log.json";
// サーバーが付ける形（attachment; filename="raft-game-<stageId>-<seed>.json"）だけを読む。
const FILENAME_PATTERN = /filename="([A-Za-z0-9._-]{1,128})"/;

function filenameOf(disposition: string | null): string {
  const name = disposition === null ? undefined : FILENAME_PATTERN.exec(disposition)?.[1];
  return name ?? DEFAULT_FILENAME;
}

function isFormat1(value: unknown): boolean {
  return (
    typeof value === "object" &&
    value !== null &&
    !Array.isArray(value) &&
    (value as Record<string, unknown>).format === 1
  );
}

export async function fetchInputLog(
  session: SessionHandle,
  fetchFn: typeof fetch = fetch,
): Promise<InputLogFetchResult> {
  let response: Response;
  try {
    response = await fetchFn(`/api/sessions/${encodeURIComponent(session.sessionId)}/input-log`, {
      method: "GET",
      headers: { Accept: "application/json", "X-Session-Token": session.token },
      cache: "no-store",
    });
  } catch {
    return { ok: false, reason: "network" };
  }
  if (response.status === 404) {
    return { ok: false, reason: "not_found" };
  }
  if (response.status !== 200) {
    return { ok: false, reason: "bad_response" };
  }
  let text: string;
  try {
    text = await response.text();
  } catch {
    return { ok: false, reason: "network" };
  }
  let body: unknown;
  try {
    body = JSON.parse(text);
  } catch {
    return { ok: false, reason: "bad_response" };
  }
  if (!isFormat1(body)) {
    return { ok: false, reason: "bad_response" };
  }
  // 受け取った文字列をそのまま保存する（読み直して書き直さない）。
  return {
    ok: true,
    blob: new Blob([text], { type: "application/json" }),
    filename: filenameOf(response.headers.get("content-disposition")),
  };
}
