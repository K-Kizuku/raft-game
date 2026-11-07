import { afterEach, describe, expect, it } from "vitest";
import { PlainStore } from "./stores.ts";
import { openTestSql, type TestSql } from "./test-sqlite.ts";
import type { DurableObjectStateLike } from "./types.ts";

const opened: TestSql[] = [];
afterEach(() => {
  for (const db of opened.splice(0)) db.close();
});

describe("PlainStore", () => {
  it("P-1: fetch ごとに n が増え、同じ DB で作り直しても続き、DurableObject を継承しない", async () => {
    const db = await openTestSql();
    opened.push(db);
    const ctx = { storage: { sql: db.sql, setAlarm: async () => {} } } satisfies DurableObjectStateLike;
    const req = () => new Request("https://spike.internal/plain", { method: "POST" });
    const store = new PlainStore(ctx, {});
    const r1 = await store.fetch(req());
    expect(r1.headers.get("cache-control")).toBe("no-store");
    expect(await r1.json()).toEqual({ class: "PlainStore", n: 1, extendsDurableObject: false });
    expect(await (await store.fetch(req())).json()).toMatchObject({ n: 2 });
    const again = new PlainStore(ctx, {});
    expect(await (await again.fetch(req())).json()).toMatchObject({ n: 3 });
    expect(Object.getPrototypeOf(PlainStore.prototype)).toBe(Object.prototype);
  });
});
