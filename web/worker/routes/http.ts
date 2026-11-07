// 共通の応答と本文の読み込み（プロトコル §2「共通の応答とエラー」）。

// 201 でトークンを返すため、すべての応答に no-store を付ける。
const BASE_HEADERS = {
  "content-type": "application/json; charset=utf-8",
  "cache-control": "no-store",
} as const;

export function json(
  body: unknown,
  status: number,
  extraHeaders?: Readonly<Record<string, string>>,
): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { ...BASE_HEADERS, ...extraHeaders },
  });
}

// message は短い英語にし、例外のメッセージやクエリ文字列などの内部の詳細を入れない。
export function errorResponse(
  status: number,
  code: string,
  message: string,
  extraHeaders?: Readonly<Record<string, string>>,
): Response {
  return json({ code, message }, status, extraHeaders);
}

export type JsonBodyResult =
  { readonly ok: true; readonly value: unknown } | { readonly ok: false };

const REJECTED: JsonBodyResult = { ok: false };

// Content-Type が application/json で、maxBytes 以下の UTF-8 の JSON の本文を読む。
// Content-Length を信用せず、ストリームを読みながら上限を超えた時点で読むのをやめる。
export async function readJsonBody(request: Request, maxBytes: number): Promise<JsonBodyResult> {
  const mime = (request.headers.get("content-type") ?? "").split(";", 1)[0]?.trim().toLowerCase();
  if (mime !== "application/json") return REJECTED;

  const contentLength = request.headers.get("content-length");
  if (contentLength !== null) {
    const declared = Number(contentLength);
    if (Number.isFinite(declared) && declared > maxBytes) return REJECTED;
  }

  if (request.body === null) return REJECTED;
  const reader = request.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > maxBytes) {
      reader.cancel().catch(() => undefined);
      return REJECTED;
    }
    chunks.push(value);
  }
  if (total === 0) return REJECTED;

  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }

  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return REJECTED;
  }
  try {
    return { ok: true, value: JSON.parse(text) as unknown };
  } catch {
    return REJECTED;
  }
}
