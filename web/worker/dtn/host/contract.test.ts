// ActorHost の契約テスト（DTN-12 AC-3）。Go の SQLite adapter（DTN-07）と同じ fixture
// server/internal/dtn/host/testdata/contract/*.json をリポジトリから読み、同じ規則（persistence-and-recovery.md §7.3）で
// 偽の Container と node:sqlite の上の ActorHost に通す。fixture を写さない（ずれないように、正本を直接読む）。

import { describe, expect, it } from "vitest";

import { ActorHost } from "../../actor-host.ts";
import type { HostLimits } from "../protocol.ts";
import { createDtnState, FakeDtnContainer } from "../test-fakes.ts";
import { HostStopped, type HostPoint } from "./core.ts";

type Obj = Record<string, unknown>;

interface Step {
  readonly note?: string;
  readonly request?: Obj;
  readonly requestText?: string;
  readonly actorReply?: Obj | "timeout" | "disconnect" | null;
  readonly crashAt?: HostPoint | null;
  readonly expect?: {
    readonly response?: unknown;
    readonly actorCalled?: boolean;
    readonly actorRequest?: unknown;
    readonly rows?: Readonly<Record<string, unknown>>;
    readonly crashed?: boolean;
    readonly destroyed?: boolean;
  };
}

interface Fixture {
  readonly name: string;
  readonly meta: { readonly sessionId: string; readonly actor: string; readonly epoch: number };
  readonly limits?: Partial<HostLimits>;
  readonly actorTimeoutMs?: number;
  readonly defaults?: Obj;
  readonly steps: readonly Step[];
}

interface NodeFs {
  readdirSync(path: string): string[];
  readFileSync(path: string, encoding: "utf8"): string;
}

// types: [] の typecheck が通るよう、node:fs は動的に読む（admin/test-sqlite.ts と同じ）。
async function loadFixtures(): Promise<Fixture[]> {
  const spec: string = "node:fs";
  const fs = (await import(/* @vite-ignore */ spec)) as NodeFs;
  const dir = new URL("../../../../server/internal/dtn/host/testdata/contract/", import.meta.url);
  return fs
    .readdirSync(dir.pathname)
    .filter((name) => name.endsWith(".json"))
    .sort()
    .map((name) => JSON.parse(fs.readFileSync(new URL(name, dir).pathname, "utf8")) as Fixture);
}

const FIXTURES = await loadFixtures();

function isObj(value: unknown): value is Obj {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// §7.3 の部分一致: オブジェクトは期待のキーだけ（期待が null ならキーが無くてもよい・"$absent" ならキーが無いこと）、
// 配列は同じ長さで要素ごと、ほかは等しいこと。
function expectSubset(actual: unknown, expected: unknown, path: string): void {
  if (Array.isArray(expected)) {
    expect(Array.isArray(actual), `${path} is not an array: ${JSON.stringify(actual)}`).toBe(true);
    const list = actual as unknown[];
    expect(list.length, `${path}.length`).toBe(expected.length);
    expected.forEach((item, i) => expectSubset(list[i], item, `${path}[${i}]`));
    return;
  }
  if (isObj(expected)) {
    expect(isObj(actual), `${path} is not an object: ${JSON.stringify(actual)}`).toBe(true);
    const obj = actual as Obj;
    for (const [key, item] of Object.entries(expected)) {
      if (item === "$absent") {
        expect(
          Object.hasOwn(obj, key) && obj[key] !== undefined,
          `${path}.${key} must be absent`,
        ).toBe(false);
      } else if (item === null) {
        expect(obj[key] ?? null, `${path}.${key}`).toBeNull();
      } else {
        expectSubset(obj[key], item, `${path}.${key}`);
      }
    }
    return;
  }
  expect(actual, path).toEqual(expected);
}

const JSON_COLUMNS: Readonly<Record<string, readonly string[]>> = {
  journal: ["payload", "response"],
  transfers: ["letter_ids", "digests", "ack"],
  contacts: ["body"],
};
const PRIMARY_KEYS: Readonly<Record<string, string>> = {
  journal: "event_seq",
  bundles: "letter_id",
  transfers: "transfer_id",
  contacts: "contact_id",
};

async function runFixture(fx: Fixture): Promise<void> {
  const container = new FakeDtnContainer("actor");
  const { state, storage } = await createDtnState(container);
  let step: Step = { expect: {} };
  let calls: Obj[] = [];
  container.responder = (request, socket) => {
    calls.push(request);
    const reply = step.actorReply;
    if (reply === undefined || reply === null || reply === "disconnect") {
      // 呼ばれない台本で呼ばれた（接続の誤り）・切れる
      queueMicrotask(() => socket.emit("close", { code: 1006, reason: "disconnect" }));
      return null;
    }
    if (reply === "timeout") return null;
    return {
      ...reply,
      v: reply["v"] ?? 1,
      actor: reply["actor"] ?? request["actor"],
      epoch: reply["epoch"] ?? request["epoch"],
      eventSeq: reply["eventSeq"] ?? request["eventSeq"],
      status: reply["status"] ?? "ok",
      outputs: reply["outputs"] ?? [],
      cursor: reply["cursor"] ?? null,
      error: reply["error"] ?? null,
    };
  };
  const makeHost = () =>
    new ActorHost(
      state,
      {},
      {
        now: () => 1_700_000_000_000,
        sleep: async () => undefined,
        log: () => undefined,
        timeoutSignal: () => undefined,
        contactKey: undefined,
        instance: "lite",
        startTimeoutMs: 1000,
        responseTimeoutMs: fx.actorTimeoutMs ?? 10_000,
        ...(fx.limits === undefined ? {} : { limits: fx.limits }),
        at: (point) => {
          if (step.crashAt === point) throw new HostStopped(point);
        },
        callHost: async () => {
          throw new Error("no peer in the contract tests");
        },
        // DTN-07 の Host は失った Container を自分で作り直さない（DTN-10）。復元の許可を断って同じにする
        callSession: async () => ({ status: 409, body: { code: "dtn_actor_lost" } }),
      },
    );
  let host = makeHost();
  const post = async (path: string, body: string) => {
    const response = await host.fetch(
      new Request(`https://actor-host.internal${path}`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body,
      }),
    );
    return { status: response.status, body: (await response.json()) as Obj };
  };
  const started = await post(
    "/start",
    JSON.stringify({
      sessionId: fx.meta.sessionId,
      actor: fx.meta.actor,
      deadlineAt: 1_700_000_900_000,
      maxAlarms: 20,
      initialEpoch: fx.meta.epoch,
    }),
  );
  expect(started.body).toEqual({ epoch: fx.meta.epoch });

  for (const [index, current] of fx.steps.entries()) {
    step = current;
    calls = [];
    const label = `${fx.name} step ${index}${current.note === undefined ? "" : ` (${current.note})`}`;
    const want = current.expect ?? {};
    const text =
      current.requestText ?? JSON.stringify({ ...(fx.defaults ?? {}), ...(current.request ?? {}) });
    let crashed = false;
    let response: Obj | null = null;
    try {
      const res = await post("/host", text);
      expect(res.status, label).toBe(200);
      response = res.body;
    } catch (error) {
      if (!(error instanceof HostStopped)) throw error;
      crashed = true;
    }
    await host.drain();
    expect(crashed, `${label}: crashed`).toBe(want.crashed ?? false);
    if (crashed) host = makeHost(); // Store を開き直して新しい Host で続ける
    if (want.response !== undefined) expectSubset(response, want.response, `${label}: response`);
    const called =
      want.actorCalled ?? (current.actorReply !== undefined && current.actorReply !== null);
    expect(
      calls.length,
      `${label}: actor calls ${JSON.stringify(calls.map((c) => c["kind"]))}`,
    ).toBe(called ? 1 : 0);
    if (want.actorRequest !== undefined)
      expectSubset(calls[0], want.actorRequest, `${label}: actorRequest`);
    if (want.destroyed === true) {
      expect(storage.deleted, `${label}: destroyed`).toBeGreaterThan(0);
      const rows = storage.sql.exec("SELECT COUNT(*) AS n FROM journal").toArray()[0]?.["n"];
      expect(rows, `${label}: journal after destroy`).toBe(0);
    }
    for (const [table, expected] of Object.entries(want.rows ?? {})) {
      if (table === "meta") {
        const meta: Obj = {};
        for (const row of storage.sql.exec("SELECT k, v FROM meta").toArray())
          meta[String(row["k"])] = row["v"];
        expectSubset(meta, expected, `${label}: rows.meta`);
        continue;
      }
      const key = PRIMARY_KEYS[table];
      if (key === undefined) throw new Error(`unknown table ${table}`);
      const rows = storage.sql
        .exec(`SELECT * FROM ${table} ORDER BY ${key}`)
        .toArray()
        .map((row) => {
          const out: Obj = { ...row };
          for (const column of JSON_COLUMNS[table] ?? []) {
            const value = out[column];
            if (typeof value === "string") out[column] = JSON.parse(value) as unknown;
          }
          return out;
        });
      expectSubset(rows, expected, `${label}: rows.${table}`);
    }
  }
}

describe("ActorHost の契約（server/internal/dtn/host/testdata/contract）", () => {
  it("fixture を読める", () => {
    expect(FIXTURES.length).toBeGreaterThanOrEqual(21);
  });

  it.each(FIXTURES.map((fx) => [fx.name, fx] as const))("%s", async (_name, fx) => {
    await runFixture(fx);
  });
});
