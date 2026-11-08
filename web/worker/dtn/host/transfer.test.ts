// ActorHost の Handoff・Contact・復元の場面（DTN-12。TS だけ）。fixtures/transfer-scenarios.json の各場面を、
// 偽の Container・偽の相手の Host・node:sqlite の上の ActorHost に通す。共通の契約（Go と同じ fixture）は contract.test.ts。

import { describe, expect, it } from "vitest";

import { ActorHost } from "../../actor-host.ts";
import type { CallResult } from "../cf-types.ts";
import { createContactSigner, parseContact } from "../contact.ts";
import { createDtnState, FakeDtnContainer, flush, ManualTimers } from "../test-fakes.ts";
import fixture from "./fixtures/transfer-scenarios.json";

type Obj = Record<string, unknown>;

interface Step {
  readonly op: string;
  readonly request?: Obj;
  readonly container?: Obj[];
  readonly peer?: Obj[];
  readonly permit?: boolean;
  readonly expect?: Obj;
}

const SESSION_ID = fixture.sessionId;
const signer = createContactSigner(fixture.contactKey);

function isObj(value: unknown): value is Obj {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// "sig": "$sign" の Contact に署名する（ほかの値はそのまま）。
async function signContacts(value: unknown): Promise<unknown> {
  if (Array.isArray(value)) return Promise.all(value.map(signContacts));
  if (!isObj(value)) return value;
  if (value["sig"] === "$sign") return signer.sign(parseContact({ ...value, sig: undefined }));
  const out: Obj = {};
  for (const [key, item] of Object.entries(value)) out[key] = await signContacts(item);
  return out;
}

// expected の値がすべて actual にある（配列は要素ごとに同じ長さで照合）。
function expectSubset(actual: unknown, expected: unknown, path: string): void {
  if (Array.isArray(expected)) {
    expect(Array.isArray(actual), path).toBe(true);
    const list = actual as unknown[];
    expect(list.length, `${path}.length`).toBe(expected.length);
    expected.forEach((item, i) => expectSubset(list[i], item, `${path}[${i}]`));
    return;
  }
  if (isObj(expected)) {
    expect(isObj(actual), path).toBe(true);
    for (const [key, item] of Object.entries(expected))
      expectSubset((actual as Obj)[key], item, `${path}.${key}`);
    return;
  }
  expect(actual, path).toEqual(expected);
}

const HOST_URL = "https://actor-host.internal";

// Host だけの種類の結果（host.status は outputs[0].data。persistence-and-recovery.md §7.2）。
function resultOf(body: Obj): unknown {
  if (body["result"] !== undefined) return body["result"];
  const outputs = body["outputs"];
  const first: unknown = Array.isArray(outputs) ? outputs[0] : undefined;
  return isObj(first) ? first["data"] : undefined;
}

async function post(
  host: ActorHost,
  path: string,
  body: unknown,
): Promise<{ status: number; body: Obj }> {
  const response = await host.fetch(
    new Request(`${HOST_URL}${path}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
  return { status: response.status, body: (await response.json()) as Obj };
}

const SNAKE: Readonly<Record<string, string>> = {
  letterId: "letter_id",
  transferId: "transfer_id",
  eventSeq: "event_seq",
};

function rowsOf(
  storageSql: { exec: (q: string) => { toArray(): Obj[] } },
  table: string,
  key: string,
): Obj[] {
  return storageSql.exec(`SELECT * FROM ${table} ORDER BY ${key}`).toArray();
}

function camelRow(row: Obj): Obj {
  const out: Obj = {};
  for (const [key, value] of Object.entries(row)) {
    const camel = key.replace(/_([a-z])/g, (_m, c: string) => c.toUpperCase());
    out[camel] = value;
  }
  return out;
}

async function runScenario(scenario: { actor: string; steps: readonly Step[] }): Promise<void> {
  const container = new FakeDtnContainer();
  const { state, storage } = await createDtnState(container);
  const timers = new ManualTimers();
  let replies: Obj[] = [];
  let peerReplies: Obj[] = [];
  let permit = true;
  let permits = 0;
  const peerCalls: Obj[] = [];
  container.responder = (request) => {
    const reply = replies.shift();
    if (reply === undefined)
      throw new Error(`unexpected container request ${String(request["kind"])}`);
    const { expectKind, expectPayload, expectReplay, ...body } = reply;
    if (expectKind !== undefined) expect(request["kind"]).toBe(expectKind);
    if (expectPayload !== undefined)
      expectSubset(request["payload"], expectPayload, "container.payload");
    if (expectReplay !== undefined) expect(request["replay"] === true).toBe(expectReplay);
    return {
      v: 1,
      actor: request["actor"],
      epoch: request["epoch"],
      eventSeq: request["eventSeq"],
      status: "ok",
      outputs: [],
      error: null,
      ...body,
    };
  };
  const host = new ActorHost(
    state,
    {},
    {
      now: () => 1_700_000_000_000,
      sleep: async () => undefined,
      log: () => undefined,
      timeoutSignal: () => undefined,
      setTimer: timers.setTimer,
      contactKey: fixture.contactKey,
      instance: "lite",
      startTimeoutMs: 1000,
      responseTimeoutMs: 1000,
      callHost: async (sessionId, actor, path, body): Promise<CallResult> => {
        expect(sessionId).toBe(SESSION_ID);
        expect(path).toBe("/cla/accept");
        peerCalls.push({ actor, body });
        const reply = peerReplies.shift();
        if (reply === undefined) throw new Error("unexpected peer call");
        if (reply["expectPayload"] !== undefined) {
          expectSubset((body as Obj)["payload"], reply["expectPayload"], "peer.payload");
        }
        return { status: Number(reply["status"]), body: reply["body"] };
      },
      callSession: async (_id, path) => {
        expect(path).toBe("/restore-permit");
        permits += 1;
        return { status: permit ? 200 : 409, body: {} };
      },
    },
  );

  const started = await post(host, "/start", {
    sessionId: SESSION_ID,
    actor: scenario.actor,
    deadlineAt: 1_700_000_900_000,
    maxAlarms: 20,
  });
  expect(started).toEqual({ status: 200, body: { epoch: 1 } });
  expect(container.starts[0]?.env).toEqual({ ROLE: scenario.actor.split(":")[0], PORT: "8080" });

  for (const [index, step] of scenario.steps.entries()) {
    const label = `step ${index} (${step.op})`;
    const want = step.expect ?? {};
    replies = [...(step.container ?? [])];
    peerReplies = [...(step.peer ?? [])];
    const before = container.requests.length;
    if (step.op === "host" || step.op === "accept") {
      const request = (await signContacts({
        v: 1,
        sessionId: SESSION_ID,
        actor: scenario.actor,
        epoch: 1,
        coordEpoch: 1,
        ...step.request,
      })) as Obj;
      const res = await post(host, step.op === "host" ? "/host" : "/cla/accept", request);
      expect(res.status, label).toBe(200);
      const body = res.body;
      expect(body["status"], `${label} ${JSON.stringify(body["error"])}`).toBe(want["status"]);
      if (want["code"] !== undefined)
        expect((body["error"] as Obj | null)?.["code"], label).toBe(want["code"]);
      if (want["outputs"] !== undefined)
        expect((body["outputs"] as unknown[]).length, label).toBe(want["outputs"]);
      if (want["result"] !== undefined)
        expectSubset(resultOf(body), want["result"], `${label}.result`);
      if (want["noBundleBytes"] === true)
        expect(JSON.stringify(body), label).not.toMatch(/"bundle"/);
      if (want["containerRequests"] !== undefined) {
        expect(container.requests.length - before, label).toBe(want["containerRequests"]);
      }
    } else if (step.op === "crash") {
      permit = step.permit ?? true;
      container.crash();
      await flush();
      await host.drain();
      const status = await post(host, "/host", {
        v: 1,
        sessionId: SESSION_ID,
        actor: scenario.actor,
        epoch: 0,
        coordEpoch: 1,
        eventSeq: 0,
        kind: "host.status",
        payload: {},
      });
      const data = resultOf(status.body) as Obj;
      expect(data["container"], label).toBe(want["state"] === "running" ? "running" : "lost");
      expectSubset(data["epoch"], want["epoch"], label);
      if (want["permits"] !== undefined) expect(permits, label).toBe(want["permits"]);
    } else if (step.op === "db") {
      const sql = storage.sql as unknown as { exec: (q: string) => { toArray(): Obj[] } };
      for (const [table, key] of [
        ["journal", "event_seq"],
        ["bundles", "letter_id"],
        ["transfers", "transfer_id"],
      ] as const) {
        const expected = want[table];
        if (expected === undefined) continue;
        const rows = rowsOf(sql, table, key).map(camelRow);
        expectSubset(rows, expected, `${label}.${table}`);
      }
    } else if (step.op === "shutdown") {
      const res = await post(host, "/shutdown", { sessionId: SESSION_ID });
      expect(res.status, label).toBe(want["httpStatus"]);
    } else {
      throw new Error(`unknown op ${step.op}`);
    }
    expect(replies, `${label}: unused container replies`).toEqual([]);
    expect(peerReplies, `${label}: unused peer replies`).toEqual([]);
  }
  void SNAKE;
  void peerCalls;
}

describe("ActorHost の Handoff・Contact・復元（fixtures/transfer-scenarios.json）", () => {
  it("fixture の形", () => {
    expect(fixture.format).toBe(1);
    expect(fixture.scenarios.length).toBeGreaterThan(0);
  });

  it.each(fixture.scenarios.map((s) => [s.name, s] as const))("%s", async (_name, scenario) => {
    await runScenario(scenario as unknown as { actor: string; steps: readonly Step[] });
  });
});
