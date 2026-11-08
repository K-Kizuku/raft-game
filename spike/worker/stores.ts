// Durable Object PlainStore（SQLite、DurableObject を継承しない。M-8）。
// ControlStore（control.ts、継承する）と同じ数え方をして、違いだけを比べる。
import { json } from "./http.ts";
import type { DurableObjectStateLike, SqlStorageLike } from "./types.ts";

export function initCounter(sql: SqlStorageLike): void {
  sql.exec("CREATE TABLE IF NOT EXISTS counter (id INTEGER PRIMARY KEY CHECK (id = 1), n INTEGER NOT NULL)");
}

/** counter の n を 1 増やして返す */
export function bumpCounter(sql: SqlStorageLike): number {
  sql.exec("INSERT INTO counter (id, n) VALUES (1, 1) ON CONFLICT(id) DO UPDATE SET n = n + 1");
  const row = sql.exec("SELECT n FROM counter WHERE id = 1").toArray()[0];
  return Number(row?.["n"] ?? 0);
}

export class PlainStore {
  private readonly sql: SqlStorageLike;

  constructor(ctx: DurableObjectStateLike, _env: unknown) {
    this.sql = ctx.storage.sql;
    initCounter(this.sql);
  }

  async fetch(_request: Request): Promise<Response> {
    const n = bumpCounter(this.sql);
    return json(200, { class: "PlainStore", n, extendsDurableObject: false });
  }
}
