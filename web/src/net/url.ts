// WS の URL を作る（T-402 / プロトコル §2・§10.1）。同じオリジンの wsPath にだけ接続する（NFR-07）。

export const WS_PATH_PATTERN: RegExp = /^\/ws\/sessions\/[A-Za-z0-9._-]{1,256}$/;

// buildWsUrl は `ws(s)://<host><wsPath>?token=<token>` を返す。
// 例外のメッセージに token と URL を入れない（トークンをログ・画面に出さない）。
export function buildWsUrl(
  session: { wsPath: string; token: string },
  location: { protocol: string; host: string },
): string {
  let wsScheme: string;
  if (location.protocol === "https:") {
    wsScheme = "wss:";
  } else if (location.protocol === "http:") {
    wsScheme = "ws:";
  } else {
    throw new Error("buildWsUrl: unsupported page protocol");
  }
  if (!WS_PATH_PATTERN.test(session.wsPath)) {
    throw new Error("buildWsUrl: invalid wsPath");
  }
  if (session.token === "") {
    throw new Error("buildWsUrl: empty token");
  }
  return `${wsScheme}//${location.host}${session.wsPath}?token=${encodeURIComponent(session.token)}`;
}
