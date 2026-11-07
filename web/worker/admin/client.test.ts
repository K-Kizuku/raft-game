import { describe, expect, it } from "vitest";

import type { VerifiedSessionId } from "../routes/ports.ts";
import type { DurableObjectNamespaceLike } from "./cf-types.ts";
import { createCostGuardClient, getCostGuardClient } from "./client.ts";

const ID = "s_a.b" as VerifiedSessionId;

function namespaceReturning(make: () => Response | Promise<Response>) {
  const requests: Request[] = [];
  const namespace: DurableObjectNamespaceLike = {
    idFromName: (name) => name,
    get: () => ({
      fetch: async (request) => {
        requests.push(request);
        return make();
      },
    }),
  };
  return { namespace, requests };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status });
}

describe("CostGuardClient", () => {
  it("内部の API の形で呼ぶ", async () => {
    const { namespace, requests } = namespaceReturning(() => jsonResponse({ result: "ok" }));
    expect(await createCostGuardClient(namespace).reserve(ID, "lite")).toBe("ok");
    const [request] = requests;
    expect(request?.method).toBe("POST");
    expect(request?.url).toBe("https://cost-guard.internal/reserve");
    expect(await request?.json()).toEqual({ sessionId: ID, instance: "lite" });
  });

  it.each([
    ["500", () => jsonResponse({ result: "ok" }, 500)],
    ["知らない結果", () => jsonResponse({ result: "maybe" })],
    ["JSON でない", () => new Response("oops")],
  ])("reserve の想定外の応答（%s）は例外", async (_name, make) => {
    const client = createCostGuardClient(namespaceReturning(make).namespace);
    await expect(client.reserve(ID, "lite")).rejects.toThrow(Error);
  });

  it("cancel・settle は 200 以外で例外", async () => {
    const client = createCostGuardClient(
      namespaceReturning(() => jsonResponse({ code: "bad_request" }, 400)).namespace,
    );
    await expect(client.cancel(ID)).rejects.toThrow(Error);
    await expect(client.settle(ID, 1)).rejects.toThrow(Error);
  });

  it.each([-1, 1.5, 86401])("settle の runSeconds %d は呼ぶ前に例外", async (runSeconds) => {
    const { namespace, requests } = namespaceReturning(() => jsonResponse({}));
    await expect(createCostGuardClient(namespace).settle(ID, runSeconds)).rejects.toThrow(
      RangeError,
    );
    expect(requests).toEqual([]);
  });

  it("view の形が違えば例外、503 guards_invalid は guards_invalid", async () => {
    await expect(
      createCostGuardClient(
        namespaceReturning(() => jsonResponse({ guards: {} })).namespace,
      ).view(),
    ).rejects.toThrow(Error);
    expect(
      await createCostGuardClient(
        namespaceReturning(() => jsonResponse({ code: "guards_invalid" }, 503)).namespace,
      ).view(),
    ).toBe("guards_invalid");
  });

  it("patch の 400・503 は結果として返し、それ以外は例外", async () => {
    const patchWith = (body: unknown, status: number) =>
      createCostGuardClient(namespaceReturning(() => jsonResponse(body, status)).namespace).patch(
        { acceptGames: false },
        "x",
        "admin",
      );
    expect(await patchWith({ code: "locked" }, 400)).toEqual({
      ok: false,
      status: 400,
      code: "locked",
    });
    expect(await patchWith({ code: "guards_invalid" }, 503)).toEqual({
      ok: false,
      status: 503,
      code: "guards_invalid",
    });
    await expect(patchWith({ code: "other" }, 400)).rejects.toThrow(Error);
    await expect(patchWith({}, 500)).rejects.toThrow(Error);
  });

  it("getCostGuardClient は COST_GUARD がなければ undefined", () => {
    expect(getCostGuardClient({})).toBeUndefined();
    expect(
      getCostGuardClient({ COST_GUARD: namespaceReturning(() => jsonResponse({})).namespace }),
    ).toBeDefined();
  });
});
