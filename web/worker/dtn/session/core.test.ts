// DtnSession のテスト（DTN-12 AC-2）。偽の Coordinator の Container・偽の ActorHost（callHost）・node:sqlite。

import { describe, expect, it } from "vitest";

import { DtnSession } from "../../dtn-session.ts";
import type { CostGuardClient } from "../../admin/client.ts";
import { DTN_STAGE_INFO } from "../catalog.ts";
import type { CallResult } from "../cf-types.ts";
import { createContactSigner, parseContact } from "../contact.ts";
import { createDtnState, FakeDtnContainer, flush, type FakeDtnStorage } from "../test-fakes.ts";

type Obj = Record<string, unknown>;

const T0 = 1_700_000_000_000;
const SESSION_ID = "s_dtn_test";
const CONTACT_KEY = "session-test-contact-key-0123456789abcdef";

interface HostCall {
  readonly actor: string;
  readonly path: string;
  readonly body: Obj;
}

class FakeHosts {
  readonly calls: HostCall[] = [];
  readonly events: string[];
  failStart = new Set<string>();
  failShutdown = new Map<string, number>(); // actor → 残りの失敗の回数
  dropHost = 0; // /host の応答を失う回数
  constructor(events: string[]) {
    this.events = events;
  }
  readonly call = async (
    sessionId: string,
    actor: string,
    path: string,
    body: unknown,
  ): Promise<CallResult> => {
    expect(sessionId).toBe(SESSION_ID);
    this.calls.push({ actor, path, body: body as Obj });
    this.events.push(`${actor}${path}`);
    if (path === "/start") {
      if (this.failStart.has(actor)) return { status: 503, body: { code: "start_failed" } };
      return { status: 200, body: { epoch: 1 } };
    }
    if (path === "/shutdown") {
      const left = this.failShutdown.get(actor) ?? 0;
      if (left > 0) {
        this.failShutdown.set(actor, left - 1);
        throw new Error("shutdown failed");
      }
      return { status: 200, body: {} };
    }
    if (path === "/host") {
      if (this.dropHost > 0) {
        this.dropHost -= 1;
        throw new Error("response lost");
      }
      const env = body as Obj;
      return {
        status: 200,
        body: {
          v: 1,
          actor,
          epoch: 1,
          eventSeq: env["eventSeq"],
          status: "ok",
          outputs: [],
          error: null,
        },
      };
    }
    return { status: 404, body: {} };
  };
  started(): string[] {
    return this.calls.filter((c) => c.path === "/start").map((c) => c.actor);
  }
  shutdowns(): string[] {
    return this.calls.filter((c) => c.path === "/shutdown").map((c) => c.actor);
  }
}

class FakeLedger implements Pick<CostGuardClient, "settle"> {
  readonly settles: { sessionId: string; runSeconds: number }[] = [];
  async settle(sessionId: string, runSeconds: number): Promise<void> {
    this.settles.push({ sessionId, runSeconds });
  }
}

interface Harness {
  readonly session: DtnSession;
  readonly container: FakeDtnContainer;
  readonly storage: FakeDtnStorage;
  readonly hosts: FakeHosts;
  readonly ledger: FakeLedger;
  readonly events: string[];
  readonly clock: { t: number };
}

async function setup(): Promise<Harness> {
  const events: string[] = [];
  const container = new FakeDtnContainer("coordinator", events);
  const { state, storage } = await createDtnState(container, events);
  const hosts = new FakeHosts(events);
  const ledger = new FakeLedger();
  const clock = { t: T0 };
  const session = new DtnSession(
    state,
    {},
    {
      now: () => clock.t,
      sleep: async () => undefined,
      log: () => undefined,
      timeoutSignal: () => undefined,
      instance: "lite",
      contactKey: CONTACT_KEY,
      startTimeoutMs: 90_000,
      coordinatorStartTimeoutMs: 60_000,
      callHost: hosts.call,
      costGuard: ledger as unknown as CostGuardClient,
      catalog: DTN_STAGE_INFO,
    },
  );
  return { session, container, storage, hosts, ledger, events, clock };
}

async function post(
  session: DtnSession,
  path: string,
  body: unknown,
): Promise<{ status: number; body: Obj }> {
  const response = await session.fetch(
    new Request(`https://dtn-session.internal${path}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
  return { status: response.status, body: (await response.json()) as Obj };
}

function initBody(stageId: string, extra: Obj = {}): Obj {
  return {
    sessionId: SESSION_ID,
    stageId,
    seed: 7,
    demo: false,
    maxMinutes: 15,
    maxAlarms: 20,
    ...extra,
  };
}

// Coordinator に送られた ctl の電文（kind の列）
function ctlSent(container: FakeDtnContainer): Obj[] {
  return container.sockets.flatMap((socket) =>
    socket.sent.map((text) => (JSON.parse(text) as { msg: Obj }).msg),
  );
}

function fromCoordinator(container: FakeDtnContainer, kind: string, payload: unknown): void {
  container.socket?.receive(JSON.stringify({ ch: "ctl", msg: { kind, payload } }));
}

async function settle(h: Harness): Promise<void> {
  await flush();
  await h.session.drain();
  await flush();
}

async function startReady(stageId = "stage1"): Promise<Harness> {
  const h = await setup();
  expect((await post(h.session, "/init", initBody(stageId))).status).toBe(201);
  await settle(h);
  fromCoordinator(h.container, "ready", {});
  await settle(h);
  return h;
}

async function probe(session: DtnSession): Promise<Obj> {
  const response = await session.fetch(new Request("https://dtn-session.internal/probe"));
  return (await response.json()) as Obj;
}

function expectFullStop(h: Harness, reason: string, actors: readonly string[]): Promise<void> {
  return (async () => {
    const view = await probe(h.session);
    expect(view["phase"]).toBe("ended");
    expect(view["endReason"]).toBe(reason);
    expect([...h.hosts.shutdowns()].sort()).toEqual([...actors].sort());
    expect(h.container.running).toBe(false);
    expect(h.ledger.settles).toHaveLength(1);
    expect(h.storage.alarms.at(-1)).toBe("delete");
  })();
}

const STAGE1 = DTN_STAGE_INFO.get("stage1")?.actors ?? [];
const IZU = DTN_STAGE_INFO.get("demo-izu")?.actors ?? [];

describe("DtnSession の起動", () => {
  it("アラームを設定してから、Coordinator と全 ActorHost を起動し、init を送る。ready で ready", async () => {
    const h = await setup();
    const res = await post(h.session, "/init", initBody("stage1"));
    expect(res.status).toBe(201);
    await settle(h);
    expect(h.events.indexOf(`setAlarm(${T0 + 90_000})`)).toBeLessThan(
      h.events.indexOf("coordinator.start"),
    );
    expect(h.container.starts[0]).toMatchObject({
      image: "registry.example/dtnactor:test",
      instance: "lite",
      enableInternet: false,
      env: { ROLE: "coordinator", PORT: "8080" },
    });
    expect(h.hosts.started()).toEqual(STAGE1);
    const start = h.hosts.calls.find((c) => c.path === "/start");
    expect(start?.body).toMatchObject({
      sessionId: SESSION_ID,
      deadlineAt: T0 + 15 * 60_000 + 60_000,
      maxAlarms: 20,
    });
    const [init] = ctlSent(h.container);
    expect(init).toMatchObject({
      kind: "init",
      payload: { stage: "stage1", seed: 7, sessionId: SESSION_ID, coordEpoch: 1 },
    });
    expect((init?.["payload"] as Obj)["actors"]).toEqual(
      STAGE1.map((actor) => ({ actor, epoch: 1 })),
    );
    expect((await probe(h.session))["phase"]).toBe("starting");

    fromCoordinator(h.container, "ready", {});
    await settle(h);
    expect((await probe(h.session))["phase"]).toBe("ready");
    // 起動の上限のアラームが鳴ったら、ゲームの期限に置き直す
    expect(h.storage.alarms.at(-1)).toBe(T0 + 90_000);
    h.clock.t = T0 + 90_000;
    await h.session.alarm();
    expect(h.storage.alarms.at(-1)).toBe(T0 + 15 * 60_000);
  });

  it("同じ /init は 200、違う /init は 409。対象外のステージ（stage3）は 400 dtn_unsupported_stage", async () => {
    const h = await setup();
    expect((await post(h.session, "/init", initBody("stage3"))).body).toEqual({
      code: "dtn_unsupported_stage",
    });
    expect(h.container.starts).toHaveLength(0);
    expect((await post(h.session, "/init", initBody("stage1"))).status).toBe(201);
    expect((await post(h.session, "/init", initBody("stage1"))).status).toBe(200);
    expect((await post(h.session, "/init", initBody("stage1", { seed: 8 }))).status).toBe(409);
    await settle(h);
  });

  it("probe の Coordinator には BOT・SPEED を渡す", async () => {
    const h = await setup();
    await post(h.session, "/init", initBody("stage1", { probe: { bot: "greedy", speed: 2 } }));
    await settle(h);
    expect(h.container.starts[0]?.env).toEqual({
      ROLE: "coordinator",
      PORT: "8080",
      BOT: "greedy",
      SPEED: "2",
    });
  });

  it("demo-izu（13 個）のうち 1 つの起動の失敗で、起動した分をすべて止めて精算は 1 回（start_timeout）", async () => {
    const h = await setup();
    h.hosts.failStart.add("ferry:f4");
    await post(h.session, "/init", initBody("demo-izu"));
    await settle(h);
    expect(h.hosts.started()).toHaveLength(12);
    await expectFullStop(h, "start_timeout", IZU);
    expect(ctlSent(h.container).some((m) => m["kind"] === "init")).toBe(false);
  });

  it("Coordinator が起動しない（images がない）→ 全停止", async () => {
    const h = await setup();
    h.container.images = {};
    await post(h.session, "/init", initBody("stage1"));
    await settle(h);
    const view = await probe(h.session);
    expect(view["endReason"]).toBe("start_timeout");
    expect(h.ledger.settles).toHaveLength(1);
  });

  it("起動の上限までに ready がなければ、アラームで start_timeout", async () => {
    const h = await setup();
    await post(h.session, "/init", initBody("stage1"));
    await settle(h);
    h.clock.t = T0 + 90_000;
    await h.session.alarm();
    await settle(h);
    await expectFullStop(h, "start_timeout", STAGE1);
  });
});

describe("DtnSession の終わり", () => {
  it("round_ended（Coordinator の end）で全停止。結果を保存する", async () => {
    const h = await startReady();
    fromCoordinator(h.container, "end", { reason: "round_ended", result: { score: 3 } });
    await settle(h);
    await expectFullStop(h, "round_ended", STAGE1);
    expect((await probe(h.session))["result"]).toEqual({ score: 3 });
  });

  it.each(["dtn_restore_failed", "dtn_limit", "dtn_actor_lost"])(
    "Coordinator の end（%s）で全停止",
    async (reason) => {
      const h = await startReady();
      fromCoordinator(h.container, "end", { reason });
      await settle(h);
      await expectFullStop(h, reason, STAGE1);
    },
  );

  it("知らない理由の end は dtn_internal", async () => {
    const h = await startReady();
    fromCoordinator(h.container, "end", { reason: "whatever" });
    await settle(h);
    await expectFullStop(h, "dtn_internal", STAGE1);
  });

  it("time_limit（期限のアラーム）で全停止", async () => {
    const h = await startReady();
    h.clock.t = T0 + 15 * 60_000;
    await h.session.alarm();
    await settle(h);
    await expectFullStop(h, "time_limit", STAGE1);
    expect(h.ledger.settles[0]).toEqual({ sessionId: SESSION_ID, runSeconds: 900 });
  });

  it("壊れた電文は dtn_internal、1 MiB を超える電文は dtn_limit", async () => {
    const h = await startReady();
    h.container.socket?.receive("{not json");
    await settle(h);
    await expectFullStop(h, "dtn_internal", STAGE1);

    const h2 = await startReady();
    h2.container.socket?.receive("x".repeat(1024 * 1024 + 1));
    await settle(h2);
    await expectFullStop(h2, "dtn_limit", STAGE1);
  });

  it("alarm_cap: アラームの回数を使い切ったら終える", async () => {
    const h = await setup();
    await post(h.session, "/init", initBody("stage1", { maxAlarms: 1 }));
    await settle(h);
    fromCoordinator(h.container, "ready", {});
    await settle(h);
    h.clock.t = T0 + 90_000;
    await h.session.alarm(); // 期限のアラームを置き直せない
    await settle(h);
    await expectFullStop(h, "alarm_cap", STAGE1);
  });

  it("destroy() の失敗はアラームで 30 秒後に再試行し、止まったら精算は 1 回", async () => {
    const h = await startReady();
    h.container.destroyErrors = [new Error("busy")];
    h.hosts.failShutdown.set("island:2", 1);
    fromCoordinator(h.container, "end", { reason: "round_ended" });
    await settle(h);
    let view = await probe(h.session);
    expect(view["phase"]).toBe("ending");
    expect(h.ledger.settles).toHaveLength(0);
    expect(h.storage.alarms.at(-1)).toBe(T0 + 30_000);

    h.clock.t = T0 + 30_000;
    await h.session.alarm();
    await settle(h);
    view = await probe(h.session);
    expect(view["phase"]).toBe("ended");
    expect(h.ledger.settles).toHaveLength(1);
    // 2 回目は止まっていない分だけ（止まった Actor に /shutdown を送り直さない）
    expect(h.hosts.shutdowns().filter((a) => a === "island:2")).toHaveLength(2);
    expect(h.hosts.shutdowns().filter((a) => a === "island:1")).toHaveLength(1);
    expect(h.container.running).toBe(false);

    await h.session.alarm(); // 終わった後のアラームは何もしない
    expect(h.ledger.settles).toHaveLength(1);
  });

  it("再試行のアラームを使い切ったら、予約のまま残す（精算しない）", async () => {
    const h = await setup();
    await post(h.session, "/init", initBody("stage1", { maxAlarms: 3 }));
    await settle(h);
    fromCoordinator(h.container, "ready", {});
    await settle(h);
    h.container.destroyError = new Error("stuck");
    fromCoordinator(h.container, "end", { reason: "round_ended" });
    await settle(h);
    for (let i = 0; i < 5; i += 1) {
      h.clock.t += 30_000;
      await h.session.alarm();
      await settle(h);
    }
    expect((await probe(h.session))["phase"]).toBe("ending");
    expect(h.ledger.settles).toHaveLength(0);
  });
});

describe("DtnSession の復元の許可", () => {
  it("1 ゲームの合計が 6 回を超える 7 回目を断り、dtn_actor_lost で終える", async () => {
    const h = await startReady();
    for (let i = 1; i <= 6; i += 1) {
      const res = await post(h.session, "/restore-permit", {
        actor: STAGE1[i % STAGE1.length],
        starts: 2,
      });
      expect(res.status, `permit ${i}`).toBe(200);
    }
    const seventh = await post(h.session, "/restore-permit", { actor: "island:1", starts: 2 });
    expect(seventh).toEqual({ status: 409, body: { code: "dtn_actor_lost" } });
    await settle(h);
    await expectFullStop(h, "dtn_actor_lost", STAGE1);
  });

  it("1 Actor の起動が 4 回を超えるなら断る。知らない Actor は 409", async () => {
    const h = await startReady();
    expect(
      (await post(h.session, "/restore-permit", { actor: "island:9", starts: 2 })).status,
    ).toBe(409);
    expect(
      (await post(h.session, "/restore-permit", { actor: "island:1", starts: 5 })).body,
    ).toEqual({
      code: "dtn_actor_lost",
    });
    await settle(h);
  });
});

describe("DtnSession の dispatch", () => {
  it("各 ActorHost の /host を呼び、coordEpoch を今の値にして、Contact に署名し、順に結果を返す", async () => {
    const h = await startReady();
    const contact = {
      contactId: "k3",
      sessionId: SESSION_ID,
      island: 1,
      ferry: "f1",
      islandEpoch: 1,
      ferryEpoch: 1,
      openedAt: 0,
    };
    const envelope = (actor: string, eventSeq: number, kind: string, payload: Obj = {}) => ({
      actor,
      envelope: {
        v: 1,
        sessionId: SESSION_ID,
        actor,
        epoch: 1,
        coordEpoch: 99,
        eventSeq,
        kind,
        payload,
      },
    });
    fromCoordinator(h.container, "dispatch", {
      id: 5,
      calls: [
        envelope("island:1", 11, "contact.open", { contact }),
        envelope("ferry:f1", 12, "contact.open", { contact }),
        envelope("island:9", 13, "advance"),
        envelope("island:2", 15, "advance"),
        envelope("island:2", 14, "advance"),
      ],
    });
    await settle(h);
    const hostCalls = h.hosts.calls.filter((c) => c.path === "/host");
    expect(hostCalls.map((c) => `${c.actor}:${String(c.body["eventSeq"])}`).sort()).toEqual(
      ["ferry:f1:12", "island:1:11", "island:2:14", "island:2:15"].sort(),
    );
    // 同じ Actor は eventSeq の順
    const island2 = hostCalls.filter((c) => c.actor === "island:2").map((c) => c.body["eventSeq"]);
    expect(island2).toEqual([14, 15]);
    expect(hostCalls.every((c) => c.body["coordEpoch"] === 1)).toBe(true);
    const signed = parseContact((hostCalls[0]?.body["payload"] as Obj)["contact"]);
    expect(await createContactSigner(CONTACT_KEY).verify(signed)).toBe(true);

    const result = ctlSent(h.container).find((m) => m["kind"] === "dispatch.result");
    const results = (result?.["payload"] as { id: number; results: Obj[] }).results;
    expect((result?.["payload"] as Obj)["id"]).toBe(5);
    expect(results.map((r) => r["eventSeq"])).toEqual([11, 12, 13, 15, 14]);
    expect((results[2]?.["error"] as Obj)["code"]).toBe("unknown_actor");
    expect(results[3]?.["status"]).toBe("ok");
  });

  it("別のセッションの Contact には署名しない（contact_mismatch）", async () => {
    const h = await startReady();
    fromCoordinator(h.container, "dispatch", {
      id: 1,
      calls: [
        {
          actor: "island:1",
          envelope: {
            v: 1,
            sessionId: SESSION_ID,
            actor: "island:1",
            epoch: 1,
            coordEpoch: 1,
            eventSeq: 3,
            kind: "contact.open",
            payload: {
              contact: {
                contactId: "k3",
                sessionId: "s_other",
                island: 1,
                ferry: "f1",
                islandEpoch: 1,
                ferryEpoch: 1,
                openedAt: 0,
              },
            },
          },
        },
      ],
    });
    await settle(h);
    expect(h.hosts.calls.filter((c) => c.path === "/host")).toHaveLength(0);
    const result = ctlSent(h.container).find((m) => m["kind"] === "dispatch.result");
    expect(((result?.["payload"] as { results: Obj[] }).results[0]?.["error"] as Obj)["code"]).toBe(
      "contact_mismatch",
    );
  });

  it("応答が失われたら同じ要求を送り直す", async () => {
    const h = await startReady();
    h.hosts.dropHost = 2;
    fromCoordinator(h.container, "dispatch", {
      id: 1,
      calls: [
        {
          actor: "island:1",
          envelope: {
            v: 1,
            sessionId: SESSION_ID,
            actor: "island:1",
            epoch: 1,
            coordEpoch: 1,
            eventSeq: 3,
            kind: "advance",
            payload: {},
          },
        },
      ],
    });
    await settle(h);
    const hostCalls = h.hosts.calls.filter((c) => c.path === "/host");
    expect(hostCalls).toHaveLength(3);
    expect(new Set(hostCalls.map((c) => JSON.stringify(c.body))).size).toBe(1);
    const result = ctlSent(h.container).find((m) => m["kind"] === "dispatch.result");
    expect((result?.["payload"] as { results: Obj[] }).results[0]?.["status"]).toBe("ok");
  });

  it("Coordinator が止まったら coordEpoch を 1 増やして作り直し、journal を restore.* で送る", async () => {
    const h = await startReady();
    fromCoordinator(h.container, "dispatch", {
      id: 1,
      calls: [
        {
          actor: "island:1",
          envelope: {
            v: 1,
            sessionId: SESSION_ID,
            actor: "island:1",
            epoch: 1,
            coordEpoch: 1,
            eventSeq: 3,
            kind: "advance",
            payload: {},
          },
        },
      ],
    });
    await settle(h);
    fromCoordinator(h.container, "journal", { entry: { now: 500 } });
    await settle(h);
    h.container.crash();
    await settle(h);
    expect(h.container.starts).toHaveLength(2);
    const sent = h.container.sockets[1]?.sent.map((t) => (JSON.parse(t) as { msg: Obj }).msg) ?? [];
    expect(sent.map((m) => m["kind"])).toEqual(["restore.begin", "restore.end"]);
    const begin = sent[0]?.["payload"] as Obj;
    expect(begin).toMatchObject({ stage: "stage1", seed: 7, sessionId: SESSION_ID, coordEpoch: 2 });
    expect((begin["entries"] as Obj[]).map((e) => e["type"])).toEqual([
      "dispatch",
      "dispatch.result",
      "note",
    ]);
    expect((await probe(h.session))["phase"]).toBe("ready");
  });

  it("Coordinator の作り直しは 3 回まで。4 回目に止まったら dtn_actor_lost", async () => {
    const h = await startReady();
    for (let i = 0; i < 3; i += 1) {
      h.container.crash();
      await settle(h);
    }
    expect((await probe(h.session))["phase"]).toBe("ready");
    h.container.crash();
    await settle(h);
    await expectFullStop(h, "dtn_actor_lost", STAGE1);
  });
});

describe("DtnSession の計測", () => {
  it("GET /probe は起動の時間・dispatch の往復・保存の量を返す", async () => {
    const h = await startReady();
    fromCoordinator(h.container, "dispatch", {
      id: 1,
      calls: [
        {
          actor: "island:1",
          envelope: {
            v: 1,
            sessionId: SESSION_ID,
            actor: "island:1",
            epoch: 1,
            coordEpoch: 1,
            eventSeq: 3,
            kind: "advance",
            payload: {},
          },
        },
      ],
    });
    await settle(h);
    const view = await probe(h.session);
    expect(view).toMatchObject({
      sessionId: SESSION_ID,
      stageId: "stage1",
      phase: "ready",
      startup: { readyMs: 0, hosts: { count: 4 } },
      dispatch: { count: 1, p50Ms: 0, p95Ms: 0 },
    });
    expect((view["storage"] as Obj)["coordJournalRows"]).toBe(2);
    expect(((view["storage"] as Obj)["hosts"] as Obj[]).map((h0) => h0["actor"])).toEqual(STAGE1);
  });
});
