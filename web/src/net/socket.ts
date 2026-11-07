// WebSocket の差し込み口（T-402）。globalThis.WebSocket に触れるのは createBrowserSocketFactory だけ。

export type SocketHandlers = {
  onOpen(): void;
  onMessage(data: unknown): void;
  onClose(code: number, reason: string): void;
  onError(): void;
};

export type SocketLike = { send(data: string): void; close(code?: number, reason?: string): void };

export type SocketFactory = (url: string, handlers: SocketHandlers) => SocketLike;

type WebSocketConstructorLike = new (url: string) => {
  onopen: ((ev: Event) => unknown) | null;
  onmessage: ((ev: MessageEvent) => unknown) | null;
  onclose: ((ev: CloseEvent) => unknown) | null;
  onerror: ((ev: Event) => unknown) | null;
  send(data: string): void;
  close(code?: number, reason?: string): void;
};

// createBrowserSocketFactory はブラウザの WebSocket を SocketFactory にする。
// 既定の ctor は呼んだときに読む（モジュールの import では globalThis.WebSocket に触れない）。
export function createBrowserSocketFactory(
  ctor: WebSocketConstructorLike | undefined = globalThis.WebSocket,
): SocketFactory {
  if (typeof ctor !== "function") {
    throw new Error("createBrowserSocketFactory: WebSocket is not available");
  }
  const Ctor = ctor;
  return (url, handlers) => {
    const ws = new Ctor(url);
    ws.onopen = () => handlers.onOpen();
    ws.onmessage = (ev) => handlers.onMessage(ev.data);
    ws.onclose = (ev) => handlers.onClose(ev.code, ev.reason);
    ws.onerror = () => handlers.onError();
    return {
      send: (data) => ws.send(data),
      close: (code, reason) => {
        try {
          ws.close(code, reason);
        } catch {
          // すでに閉じている・閉じかけている
        }
      },
    };
  };
}
