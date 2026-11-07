// 最小の Worker（T-004）。静的ファイルは assets が配り、/api/* と /ws/* だけがここに届く
// （cloudflare.config.ts の runWorkerFirst）。Durable Object などは T-307 以降で足す。

const JSON_HEADERS = { "content-type": "application/json; charset=utf-8" } as const;

function json(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), { status, headers: JSON_HEADERS });
}

export async function handleRequest(request: Request): Promise<Response> {
  const { pathname } = new URL(request.url);

  if (pathname === "/api/healthz" && request.method === "GET") {
    return json({ ok: true }, 200);
  }

  return json({ ok: false, error: "not_found" }, 404);
}

export default {
  fetch: handleRequest,
} satisfies { fetch(request: Request): Promise<Response> };
