// Container の起動・準備の確認・WebSocket の接続（DTN-12）。DtnSession と ActorHost が共有する。
// GameSession（session/core.ts）と同じ手順: start → /healthz が 200 になるまで待つ → /ws に Upgrade でつなぐ。

import type { ContainerInstance } from "../admin/guards.ts";
import { webSocketOf, type ContainerLike, type WebSocketLike } from "../session/cf-types.ts";
import { CONTAINER_PORT, IMAGE_NAME } from "./protocol.ts";

const CONTAINER_ORIGIN = "http://container";
export const READY_POLL_INTERVAL_MS = 100;
export const READY_POLL_REQUEST_MS = 2000;

export interface LinkDeps {
  readonly now: () => number;
  readonly sleep: (ms: number) => Promise<void>;
  readonly timeoutSignal: (ms: number) => AbortSignal | undefined;
}

// ctx.container.start（外部への通信は禁止）。image がない・start が例外なら false。
export function startContainer(
  container: ContainerLike | undefined,
  instance: ContainerInstance,
  env: Readonly<Record<string, string>>,
): boolean {
  const image = container?.images[IMAGE_NAME];
  if (container === undefined || image === undefined) return false;
  try {
    container.start({ image, instance, enableInternet: false, env });
    return true;
  } catch {
    return false;
  }
}

// /healthz が 200 を返すまで待つ。stale() が true になったら "stale"。
export async function waitHealthy(
  container: ContainerLike,
  deadline: number,
  deps: LinkDeps,
  stale: () => boolean,
): Promise<"ready" | "timeout" | "stale"> {
  const port = container.getTcpPort(CONTAINER_PORT);
  for (;;) {
    const left = deadline - deps.now();
    const signal = deps.timeoutSignal(Math.max(1, Math.min(left, READY_POLL_REQUEST_MS)));
    let ok = false;
    try {
      const response = await port.fetch(
        `${CONTAINER_ORIGIN}/healthz`,
        signal === undefined ? {} : { signal },
      );
      ok = response.status === 200;
      await response.body?.cancel();
    } catch {
      // 失敗として続ける
    }
    if (stale()) return "stale";
    if (ok) return "ready";
    if (deps.now() >= deadline) return "timeout";
    await deps.sleep(READY_POLL_INTERVAL_MS);
    if (stale()) return "stale";
  }
}

// Container の /ws に WebSocket をつなぐ（accept まで行う）。失敗したら null。
export async function connectSocket(container: ContainerLike): Promise<WebSocketLike | null> {
  try {
    const response = await container
      .getTcpPort(CONTAINER_PORT)
      .fetch(`${CONTAINER_ORIGIN}/ws`, { headers: { Upgrade: "websocket" } });
    const socket = webSocketOf(response);
    if (socket === null) return null;
    socket.accept();
    return socket;
  } catch {
    return null;
  }
}

export function safeSend(socket: WebSocketLike, text: string): boolean {
  try {
    socket.send(text);
    return true;
  } catch {
    return false;
  }
}

export function safeClose(socket: WebSocketLike, code: number, reason: string): void {
  try {
    socket.close(code, reason);
  } catch {
    // 閉じている
  }
}

export function errorName(error: unknown): string {
  return error instanceof Error ? error.name : "unknown";
}
