// Durable Object ControlStore（SQLite、DurableObject を継承する）。PlainStore と比べるための対照（M-8）。
// cloudflare:workers を import するので、テスト（Node）からは import しない。
import { DurableObject } from "cloudflare:workers";
import { json } from "./http.ts";
import { bumpCounter, initCounter } from "./stores.ts";

export class ControlStore extends DurableObject {
  override async fetch(_request: Request): Promise<Response> {
    const sql = this.ctx.storage.sql;
    initCounter(sql);
    const n = bumpCounter(sql);
    return json(200, { class: "ControlStore", n, extendsDurableObject: true });
  }
}
