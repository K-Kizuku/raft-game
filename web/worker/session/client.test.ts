import { describe, expect, it } from "vitest";

import type { DurableObjectNamespaceLike } from "../admin/cf-types.ts";
import type { VerifiedSessionId } from "../routes/ports.ts";
import { createGameSessionClient } from "./client.ts";

const ID = "s_abc.def" as VerifiedSessionId;
const TOKEN = "Tok_en-abcdefghijklmno"; // 22 文字

function fakeNamespace(respond: () => Response) {
  const calls: string[] = [];
  const requests: Request[] = [];
  const namespace: DurableObjectNamespaceLike = {
    idFromName: (name) => {
      calls.push(`idFromName(${name})`);
      return { name };
    },
    get: () => {
      calls.push("get");
      return {
        fetch: async (request) => {
          requests.push(request);
          return respond();
        },
      };
    },
  };
  return { namespace, calls, requests };
}

describe("CL-1 inputLog（T-714）", () => {
  it("GET /input-log に x-session-token を付けて送り、応答をそのまま返す", async () => {
    const response = new Response('{"format":1}', { status: 200 });
    const fake = fakeNamespace(() => response);
    const result = await createGameSessionClient(fake.namespace).inputLog(ID, TOKEN);

    expect(result).toBe(response);
    expect(fake.calls).toEqual([`idFromName(${ID})`, "get"]);
    expect(fake.requests).toHaveLength(1);
    const [request] = fake.requests;
    expect(request?.method).toBe("GET");
    expect(request?.url).toBe("https://game-session.internal/input-log");
    expect(request?.headers.get("x-session-token")).toBe(TOKEN);
  });

  it("404 も包み直さずに返す（例外にしない）", async () => {
    const response = new Response('{"code":"not_found"}', { status: 404 });
    const fake = fakeNamespace(() => response);

    await expect(createGameSessionClient(fake.namespace).inputLog(ID, TOKEN)).resolves.toBe(
      response,
    );
  });

  it("クライアントを作っただけでは DO に触れない", () => {
    const fake = fakeNamespace(() => new Response("{}"));
    createGameSessionClient(fake.namespace);
    expect(fake.calls).toEqual([]);
  });
});
