// ack を待つコマンド（T-308b・DesignDoc §12.3「入力ログと復元」）。時刻は引数で渡す。
//
// DO に 1 つ（接続ごとではない）。Go は ack を「いまの接続」に送るので、古い接続から送ったコマンドの
// ack が新しい接続に届くこともあるため。

import { PENDING_COMMAND_TTL_MS } from "./limits.ts";

export interface PendingEntry {
  readonly cmd: string; // {"type":…,"payload":…} の JSON
  readonly bytes: number; // cmd の UTF-8 のバイト数
}

interface Stored extends PendingEntry {
  readonly at: number;
}

export class PendingCommands {
  readonly #entries = new Map<string, Stored>();
  #bytes = 0;

  has(cmdId: string): boolean {
    return this.#entries.has(cmdId);
  }

  add(cmdId: string, entry: PendingEntry, now: number): void {
    this.take(cmdId);
    this.#entries.set(cmdId, { cmd: entry.cmd, bytes: entry.bytes, at: now });
    this.#bytes += entry.bytes;
  }

  take(cmdId: string): PendingEntry | undefined {
    const stored = this.#entries.get(cmdId);
    if (stored === undefined) return undefined;
    this.#entries.delete(cmdId);
    this.#bytes -= stored.bytes;
    return { cmd: stored.cmd, bytes: stored.bytes };
  }

  // PENDING_COMMAND_TTL_MS を過ぎたものを捨てる。
  purge(now: number): void {
    for (const [cmdId, stored] of [...this.#entries]) {
      if (now - stored.at >= PENDING_COMMAND_TTL_MS) this.take(cmdId);
    }
  }

  clear(): void {
    this.#entries.clear();
    this.#bytes = 0;
  }

  get size(): number {
    return this.#entries.size;
  }

  // 待っているもののバイト数の合計
  get bytes(): number {
    return this.#bytes;
  }
}
