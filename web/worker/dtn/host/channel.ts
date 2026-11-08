// ActorHost → Actor（Container）の WebSocket の要求と応答（DTN-12・docs/dtn/actor-protocol.md §1・§2）。
//
// 1 フレーム = 1 つの JSON。同時に待つ要求は 1 つだけ（Actor の状態を変える要求は Actor ごとに直列。
// 直列化は ActorHostCore のロックが行う）。待っている間に届いた次のフレームを、その要求の応答として返す
// （epoch・actor・eventSeq の照合は ActorHostCore が行う。persistence-and-recovery.md §7.2）。
// ソケットが閉じた・応答の上限時間を過ぎた・JSON でないフレームなら、待っている要求を ChannelLost で失敗させ、
// onLost を 1 回だけ呼ぶ（Host は Container が止まったとみなして復元する）。待っていないときのフレームは捨てる。

import { safeClose, safeSend } from "../container-link.ts";
import type { WebSocketLike } from "../cf-types.ts";

export class ChannelLost extends Error {
  readonly cause: string;
  constructor(cause: string) {
    super(`channel lost: ${cause}`);
    this.name = "ChannelLost";
    this.cause = cause;
  }
}

interface Pending {
  readonly eventSeq: number;
  readonly resolve: (response: unknown) => void;
  readonly reject: (error: Error) => void;
  readonly cancelTimer: () => void;
}

export type SetTimer = (callback: () => void, ms: number) => () => void;

export class ActorChannel {
  readonly epoch: number;
  readonly #socket: WebSocketLike;
  readonly #setTimer: SetTimer;
  readonly #timeoutMs: number;
  readonly #onLost: (cause: string) => void;
  readonly #maxBytes: number;
  #pending: Pending | null = null;
  #closed = false;

  constructor(
    socket: WebSocketLike,
    epoch: number,
    options: {
      setTimer: SetTimer;
      timeoutMs: number;
      onLost: (cause: string) => void;
      maxBytes?: number;
    },
  ) {
    this.#socket = socket;
    this.epoch = epoch;
    this.#setTimer = options.setTimer;
    this.#timeoutMs = options.timeoutMs;
    this.#onLost = options.onLost;
    this.#maxBytes = options.maxBytes ?? 2 * 1024 * 1024;
    socket.addEventListener("message", (event) => this.#onMessage(event.data));
    socket.addEventListener("close", () => this.#lose("socket_closed"));
    socket.addEventListener("error", () => this.#lose("socket_error"));
  }

  get closed(): boolean {
    return this.#closed;
  }

  request(
    request: Readonly<Record<string, unknown>> & { readonly eventSeq: number },
  ): Promise<unknown> {
    if (this.#closed) return Promise.reject(new ChannelLost("closed"));
    if (this.#pending !== null) return Promise.reject(new Error("channel busy"));
    return new Promise<unknown>((resolve, reject) => {
      const cancelTimer = this.#setTimer(() => this.#lose("timeout"), this.#timeoutMs);
      this.#pending = { eventSeq: request.eventSeq, resolve, reject, cancelTimer };
      if (!safeSend(this.#socket, JSON.stringify(request))) this.#lose("send_failed");
    });
  }

  // 自分で閉じる（止める・作り直す）。onLost は呼ばない。
  close(reason: string): void {
    if (this.#closed) return;
    this.#closed = true;
    this.#failPending(new ChannelLost(reason));
    safeClose(this.#socket, 1000, reason);
  }

  #failPending(error: Error): void {
    const pending = this.#pending;
    this.#pending = null;
    if (pending === null) return;
    pending.cancelTimer();
    pending.reject(error);
  }

  #lose(cause: string): void {
    if (this.#closed) return;
    this.#closed = true;
    this.#failPending(new ChannelLost(cause));
    safeClose(this.#socket, 1011, cause);
    this.#onLost(cause);
  }

  #onMessage(data: unknown): void {
    if (this.#closed) return;
    const pending = this.#pending;
    if (pending === null) return; // 待っていない（古い要求の遅れた応答など）
    if (typeof data !== "string" || data.length > this.#maxBytes) {
      this.#lose("bad_frame");
      return;
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(data) as unknown;
    } catch {
      this.#lose("bad_frame");
      return;
    }
    this.#pending = null;
    pending.cancelTimer();
    pending.resolve(parsed);
  }
}
