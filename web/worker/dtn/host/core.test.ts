// ActorHost のテスト（契約の fixture にない部分: 期限のアラーム・片付けの再試行・上限・起動の失敗・応答の上限時間）。

import { describe, expect, it, vi } from "vitest";

import { ActorHost } from "../../actor-host.ts";
import { encodeBase64 } from "../codec.ts";
import {
  createDtnState,
  FakeDtnContainer,
  flush,
  ManualTimers,
  type FakeDtnStorage,
} from "../test-fakes.ts";

type Obj = Record<string, unknown>;

const T0 = 1_700_000_000_000;
const SESSION_ID = "s_host_test";
const DEADLINE = T0 + 16 * 60_000;

interface Harness {
  readonly host: ActorHost;
  readonly container: FakeDtnContainer;
  readonly storage: FakeDtnStorage;
  readonly timers: ManualTimers;
  readonly clock: { t: number };
  readonly permits: Obj[];
}

async function setup(options: { permit?: boolean } = {}): Promise<Harness> {
  const container = new FakeDtnContainer("actor");
  const { state, storage } = await createDtnState(container);
  const timers = new ManualTimers();
  const clock = { t: T0 };
  const permits: Obj[] = [];
  container.responder = (request) => ({
    v: 1,
    actor: request["actor"],
    epoch: request["epoch"],
    eventSeq: request["eventSeq"],
    status: "ok",
    outputs: [],
    error: null,
  });
  const host = new ActorHost(
    state,
    {},
    {
      now: () => clock.t,
      sleep: async (ms) => {
        clock.t += ms;
      },
      log: () => undefined,
      timeoutSignal: () => undefined,
      setTimer: timers.setTimer,
      contactKey: undefined,
      instance: "lite",
      startTimeoutMs: 1000,
      responseTimeoutMs: 10_000,
      callHost: async () => {
        throw new Error("not used");
      },
      callSession: async (_id, _path, body) => {
        permits.push(body as Obj);
        return { status: options.permit === false ? 409 : 200, body: {} };
      },
    },
  );
  return { host, container, storage, timers, clock, permits };
}

async function post(
  host: ActorHost,
  path: string,
  body: unknown,
): Promise<{ status: number; body: Obj }> {
  const response = await host.fetch(
    new Request(`https://actor-host.internal${path}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: typeof body === "string" ? body : JSON.stringify(body),
    }),
  );
  return { status: response.status, body: (await response.json()) as Obj };
}

function start(h: Harness, actor = "island:1") {
  return post(h.host, "/start", {
    sessionId: SESSION_ID,
    actor,
    deadlineAt: DEADLINE,
    maxAlarms: 5,
  });
}

function env(eventSeq: number, kind: string, payload: Obj = {}, extra: Obj = {}): Obj {
  return {
    v: 1,
    sessionId: SESSION_ID,
    actor: "island:1",
    epoch: 1,
    coordEpoch: 1,
    eventSeq,
    kind,
    payload,
    ...extra,
  };
}

describe("ActorHost の起動", () => {
  it("期限のアラームを置いてから Container を ROLE で起動し、epoch 1 を返す。2 回目の /start は同じ epoch", async () => {
    const h = await setup();
    expect(await start(h)).toEqual({ status: 200, body: { epoch: 1 } });
    expect(h.storage.alarms).toEqual([DEADLINE]);
    expect(h.container.starts[0]).toMatchObject({
      instance: "lite",
      enableInternet: false,
      env: { ROLE: "island", PORT: "8080" },
    });
    expect(await start(h)).toEqual({ status: 200, body: { epoch: 1 } });
    expect(h.container.starts).toHaveLength(1);
  });

  it("船は ROLE=ferry", async () => {
    const h = await setup();
    await start(h, "ferry:f1");
    expect(h.container.starts[0]?.env).toEqual({ ROLE: "ferry", PORT: "8080" });
  });

  it("別のセッション・別の Actor の /start は 409 session_mismatch", async () => {
    const h = await setup();
    await start(h);
    const other = await post(h.host, "/start", {
      sessionId: "s_other",
      actor: "island:1",
      deadlineAt: DEADLINE,
      maxAlarms: 5,
    });
    expect(other).toEqual({ status: 409, body: { code: "session_mismatch" } });
    expect((await start(h, "island:2")).status).toBe(409);
  });

  it("/healthz が上限までに 200 にならなければ 503 start_failed", async () => {
    const h = await setup();
    h.container.healthy = false;
    expect(await start(h)).toEqual({ status: 503, body: { code: "start_failed" } });
  });

  it("image がなければ 503（Container を起動しない）", async () => {
    const h = await setup();
    h.container.images = {};
    expect((await start(h)).status).toBe(503);
    expect(h.container.starts).toHaveLength(0);
  });

  it("起動していない Host への /host は not_ready", async () => {
    const h = await setup();
    const res = await post(h.host, "/host", env(1, "init"));
    expect((res.body["error"] as Obj)["code"]).toBe("not_ready");
  });
});

describe("ActorHost の上限", () => {
  it("1 MiB を超える電文は too_large", async () => {
    const h = await setup();
    await start(h);
    const res = await post(h.host, "/host", env(1, "init", { pad: "x".repeat(1024 * 1024) }));
    expect((res.body["error"] as Obj)["code"]).toBe("too_large");
  });

  it("Actor の出力の 16 KiB を超える Bundle は invalid（確定しない）", async () => {
    const h = await setup();
    await start(h);
    const big = encodeBase64(new Uint8Array(16 * 1024 + 1));
    h.container.responder = (request) => ({
      v: 1,
      actor: "island:1",
      epoch: request["epoch"],
      eventSeq: request["eventSeq"],
      status: "ok",
      outputs: [
        {
          t: 0,
          class: 3,
          seq: 1,
          type: "letter.created",
          data: { letter: { id: 1000000001 } },
          bundle: big,
        },
      ],
      error: null,
    });
    const res = await post(h.host, "/host", env(1, "advance", { horizon: [1, 1] }));
    expect((res.body["error"] as Obj)["code"]).toBe("invalid");
    expect(h.storage.sql.exec("SELECT status FROM journal").toArray()).toEqual([
      { status: "intent" },
    ]);
  });

  it("journal が 20,000 行なら too_large", async () => {
    const h = await setup();
    await start(h);
    h.storage.transactionSync(() => {
      for (let i = 1; i <= 20_000; i += 1) {
        h.storage.sql.exec(
          "INSERT INTO journal (event_seq, kind, req_digest, payload, status, epoch) VALUES (?, 'advance', 'd', '{}', 'applied', 1)",
          i,
        );
      }
    });
    const res = await post(h.host, "/host", env(20_001, "advance", { horizon: [1, 1] }));
    expect((res.body["error"] as Obj)["code"]).toBe("too_large");
  });
});

describe("ActorHost の Container が止まったとき", () => {
  it("応答の上限時間を過ぎたら not_ready を返し、許可を得て epoch 2 で作り直す", async () => {
    const h = await setup();
    await start(h);
    h.container.responder = () => null; // 返事をしない
    const pending = post(h.host, "/host", env(1, "init", { stage: "stage1" }));
    await vi.waitFor(() => expect(h.timers.size).toBeGreaterThan(0), { timeout: 4000 });
    h.timers.fireAll();
    const res = await pending;
    expect((res.body["error"] as Obj)["code"]).toBe("not_ready");
    // 再生: intent の行は再生しない（Coordinator の送り直しで続ける）
    h.container.responder = (request) => ({
      v: 1,
      actor: "island:1",
      epoch: request["epoch"],
      eventSeq: request["eventSeq"],
      status: "ok",
      outputs: [],
      error: null,
    });
    await flush();
    await h.host.drain();
    expect(h.permits).toEqual([{ actor: "island:1", starts: 2 }]);
    expect(h.container.starts).toHaveLength(2);
    const again = await post(h.host, "/host", env(1, "init", { stage: "stage1" }, { epoch: 2 }));
    expect(again.body["status"]).toBe("ok");
    expect(again.body["epoch"]).toBe(2);
  });

  it("Actor の internal エラーは確定せずに返し、作り直す", async () => {
    const h = await setup();
    await start(h);
    h.container.responder = (request) => ({
      v: 1,
      actor: "island:1",
      epoch: request["epoch"],
      eventSeq: request["eventSeq"],
      status: "error",
      outputs: [],
      error: { code: "internal", message: "panic" },
    });
    const res = await post(h.host, "/host", env(1, "init"));
    expect((res.body["error"] as Obj)["code"]).toBe("internal");
    await flush();
    await h.host.drain();
    expect(h.container.starts).toHaveLength(2);
  });
});

describe("ActorHost の片付け", () => {
  it("期限のアラームで Container を止めて保存を消す（DtnSession が止まっても残さない）", async () => {
    const h = await setup();
    await start(h);
    await post(h.host, "/host", env(1, "init"));
    h.clock.t = DEADLINE;
    await h.host.alarm();
    expect(h.container.destroys).toBe(1);
    expect(h.container.running).toBe(false);
    expect(h.storage.deleted).toBe(1);
    expect(h.storage.sql.exec("SELECT COUNT(*) AS n FROM journal").toArray()[0]?.["n"]).toBe(0);
    await h.host.alarm(); // 消した後のアラームは何もしない
    expect(h.container.destroys).toBe(1);
  });

  it("期限の前に鳴ったアラームは、期限に置き直す", async () => {
    const h = await setup();
    await start(h);
    await h.host.alarm();
    expect(h.container.destroys).toBe(0);
    expect(h.storage.alarms.at(-1)).toBe(DEADLINE);
  });

  it("/shutdown の destroy() が失敗したら 500 で保存を残し、30 秒後のアラームで再試行する", async () => {
    const h = await setup();
    await start(h);
    h.container.destroyErrors = [new Error("busy")];
    expect(await post(h.host, "/shutdown", { sessionId: SESSION_ID })).toEqual({
      status: 500,
      body: { code: "destroy_failed" },
    });
    expect(h.storage.deleted).toBe(0);
    expect(h.storage.alarms.at(-1)).toBe(T0 + 30_000);
    const blocked = await post(h.host, "/host", env(1, "init"));
    expect((blocked.body["error"] as Obj)["code"]).toBe("not_ready");
    h.clock.t = T0 + 30_000;
    await h.host.alarm();
    expect(h.container.running).toBe(false);
    expect(h.storage.deleted).toBe(1);
    expect(await post(h.host, "/shutdown", { sessionId: SESSION_ID })).toEqual({
      status: 200,
      body: {},
    });
  });

  it("アラームの回数を使い切ったら、再試行をあきらめる（予約のまま）", async () => {
    const h = await setup();
    await start(h); // アラーム 1 回
    h.container.destroyError = new Error("stuck");
    for (let i = 0; i < 6; i += 1) {
      await post(h.host, "/shutdown", { sessionId: SESSION_ID });
    }
    expect(h.storage.alarms.filter((a) => a !== "delete")).toHaveLength(5);
    expect(h.storage.deleted).toBe(0);
  });
});

describe("ActorHost の Container の準備", () => {
  it("/healthz が 404（dtnactor は /ws だけを開く）でも、待ち受けているとみなしてつなぐ", async () => {
    const h = await setup();
    const fetchPort = h.container.getTcpPort.bind(h.container);
    h.container.getTcpPort = (port) => {
      const inner = fetchPort(port);
      return {
        fetch: async (input, init) =>
          new URL(input).pathname === "/healthz"
            ? new Response("not found", { status: 404 })
            : inner.fetch(input, init),
      };
    };
    expect(await start(h)).toEqual({ status: 200, body: { epoch: 1 } });
  });
});
