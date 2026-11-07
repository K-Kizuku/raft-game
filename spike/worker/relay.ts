// WS の中継（ブラウザ ⇄ Durable Object ⇄ コンテナ。M-5）。Hibernation API は使わない。
// テキストの電文を両方向に中身を変えずに渡す。ブラウザから届いた 16 KiB を超える電文とバイナリは捨てて数える。
// どちらかが閉じたら、もう片方を閉じる（ブラウザが閉じた → コンテナ側を 1000、コンテナが閉じた・落ちた → ブラウザ側を 1011）。
import { MAX_WS_MESSAGE_BYTES } from "./limits.ts";
import type { WebSocketLike } from "./types.ts";

export interface RelayStats {
  /** コンテナにつながり、まだ閉じていない */
  open: boolean;
  toContainer: number;
  toBrowser: number;
  droppedOversize: number;
  droppedBinary: number;
  /** 先に閉じた側（connect_failed はコンテナへの接続の失敗） */
  closedBy: "browser" | "container" | "connect_failed" | null;
  browserCloseCode: number | null;
  containerCloseCode: number | null;
}

export function newRelayStats(): RelayStats {
  return {
    open: false,
    toContainer: 0,
    toBrowser: 0,
    droppedOversize: 0,
    droppedBinary: 0,
    closedBy: null,
    browserCloseCode: null,
    containerCloseCode: null,
  };
}

const encoder = new TextEncoder();

/** UTF-8 のバイト数が max を超えるか（短い文字列は数えずに済ませる） */
export function exceedsBytes(s: string, max: number): boolean {
  if (s.length > max) return true; // 1 つの UTF-16 の単位は 1 バイト以上
  if (s.length * 3 <= max) return false; // 1 つの UTF-16 の単位は 3 バイト以下
  return encoder.encode(s).byteLength > max;
}

function safeClose(ws: WebSocketLike, code: number, reason: string): void {
  try {
    ws.close(code, reason);
  } catch {
    // すでに閉じている
  }
}

/**
 * browser（WebSocketPair の Durable Object の側）を accept し、connect() でコンテナにつなぎ、中継する。
 * コンテナへの接続を待つ間にブラウザから届いた電文は、つながった後に順に送る。
 * 戻り値の Promise は、コンテナへの接続を試し終えたら解決する（中継の終わりではない）。
 */
export async function startRelay(
  browser: WebSocketLike,
  connect: () => Promise<WebSocketLike | null>,
  stats: RelayStats,
  maxBytes: number = MAX_WS_MESSAGE_BYTES,
): Promise<void> {
  let upstream: WebSocketLike | null = null;
  let closed = false;
  const pending: string[] = [];

  const finish = (by: "browser" | "container", browserCode: number, containerCode: number): void => {
    if (closed) return;
    closed = true;
    stats.open = false;
    stats.closedBy = by;
    if (upstream !== null) safeClose(upstream, containerCode, by === "browser" ? "browser closed" : "closed");
    safeClose(browser, browserCode, by === "container" ? "container closed" : "closed");
  };

  browser.accept();
  browser.addEventListener("message", (event) => {
    if (closed) return;
    const data = event.data;
    if (typeof data !== "string") {
      stats.droppedBinary++;
      return;
    }
    if (exceedsBytes(data, maxBytes)) {
      stats.droppedOversize++;
      return;
    }
    if (upstream === null) {
      pending.push(data);
      return;
    }
    upstream.send(data);
    stats.toContainer++;
  });
  browser.addEventListener("close", (event) => {
    stats.browserCloseCode = event.code;
    finish("browser", 1000, 1000);
  });
  browser.addEventListener("error", () => finish("browser", 1000, 1000));

  let ws: WebSocketLike | null;
  try {
    ws = await connect();
  } catch {
    ws = null;
  }
  if (closed) {
    if (ws !== null) safeClose(ws, 1000, "browser closed");
    return;
  }
  if (ws === null) {
    closed = true;
    stats.closedBy = "connect_failed";
    safeClose(browser, 1011, "container connect failed");
    return;
  }

  ws.accept();
  upstream = ws;
  stats.open = true;
  ws.addEventListener("message", (event) => {
    if (closed) return;
    const data = event.data;
    if (typeof data !== "string") {
      stats.droppedBinary++;
      return;
    }
    browser.send(data);
    stats.toBrowser++;
  });
  ws.addEventListener("close", (event) => {
    stats.containerCloseCode = event.code;
    finish("container", 1011, 1000);
  });
  ws.addEventListener("error", () => finish("container", 1011, 1000));

  for (const m of pending) {
    ws.send(m);
    stats.toContainer++;
  }
  pending.length = 0;
}
