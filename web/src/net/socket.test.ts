import { afterEach, describe, expect, it, vi } from "vitest";

import { createBrowserSocketFactory } from "./socket.ts";

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  onopen: ((ev: Event) => unknown) | null = null;
  onmessage: ((ev: MessageEvent) => unknown) | null = null;
  onclose: ((ev: CloseEvent) => unknown) | null = null;
  onerror: ((ev: Event) => unknown) | null = null;
  readonly url: string;
  sent: string[] = [];
  closes: { code?: number; reason?: string }[] = [];
  closed = false;

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }
  send(data: string): void {
    this.sent.push(data);
  }
  close(code?: number, reason?: string): void {
    if (this.closed) {
      throw new Error("already closed");
    }
    this.closed = true;
    this.closes.push({ code, reason });
  }
}

afterEach(() => {
  vi.unstubAllGlobals();
  FakeWebSocket.instances = [];
});

describe("createBrowserSocketFactory", () => {
  it("SK-1: イベントを SocketHandlers につなぎ、send・close を本体に渡す", () => {
    const factory = createBrowserSocketFactory(FakeWebSocket);
    const log: string[] = [];
    const socket = factory("wss://raft.example/ws/sessions/a?token=t", {
      onOpen: () => log.push("open"),
      onMessage: (data) => log.push(`message:${String(data)}`),
      onClose: (code, reason) => log.push(`close:${code}:${reason}`),
      onError: () => log.push("error"),
    });
    const ws = FakeWebSocket.instances[0];
    if (ws === undefined) {
      throw new Error("no socket");
    }
    expect(ws.url).toBe("wss://raft.example/ws/sessions/a?token=t");
    ws.onopen?.({} as Event);
    ws.onmessage?.({ data: "hi" } as MessageEvent);
    ws.onerror?.({} as Event);
    ws.onclose?.({ code: 1006, reason: "gone" } as CloseEvent);
    expect(log).toEqual(["open", "message:hi", "error", "close:1006:gone"]);

    socket.send("payload");
    expect(ws.sent).toEqual(["payload"]);
    socket.close(1000, "bye");
    expect(ws.closes).toEqual([{ code: 1000, reason: "bye" }]);
    expect(() => socket.close(1000)).not.toThrow();
  });

  it("SK-2: globalThis.WebSocket が無ければ呼んだときに投げる", async () => {
    vi.stubGlobal("WebSocket", undefined);
    vi.resetModules();
    const mod = await import("./socket.ts"); // import だけでは投げない
    expect(() => mod.createBrowserSocketFactory()).toThrow(Error);
  });
});
