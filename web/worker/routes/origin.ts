// Origin の許可リスト（NFR-07・DesignDoc §16）。

// ALLOWED_ORIGINS（カンマ区切り）から、許可するオリジンの集合を作る。
// 有効な要素が 1 つもなければ、リクエスト自身のオリジンだけを許す（同じオリジンだけ）。
export function parseAllowedOrigins(raw: string | undefined, requestUrl: URL): ReadonlySet<string> {
  const allowed = new Set<string>();
  for (const part of (raw ?? "").split(",")) {
    const item = part.trim();
    if (item === "") continue;
    let url: URL;
    try {
      url = new URL(item);
    } catch {
      continue;
    }
    if (url.protocol !== "http:" && url.protocol !== "https:") continue;
    allowed.add(url.origin);
  }
  if (allowed.size === 0) allowed.add(requestUrl.origin);
  return allowed;
}

// 文字列の完全一致で照合する（"null"・複数の値・大文字の違いは一致しない）。
export function isOriginAllowed(origin: string, allowed: ReadonlySet<string>): boolean {
  return allowed.has(origin);
}
