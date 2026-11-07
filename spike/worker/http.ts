// Worker と Durable Object の応答の共通の形。すべて JSON・cache-control: no-store。

/** Worker が Durable Object に渡す、Worker の colo（M-14） */
export const COLO_HEADER = "x-spike-colo";

export const JSON_HEADERS: Readonly<Record<string, string>> = {
  "content-type": "application/json",
  "cache-control": "no-store",
};

export function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: JSON_HEADERS });
}

export function errorResponse(status: number, error: string, message: string, extra: object = {}): Response {
  return json(status, { error, message, ...extra });
}

export function messageOf(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
