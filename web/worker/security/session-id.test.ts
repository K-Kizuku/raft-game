import { describe, expect, it } from "vitest";

import { decodeBase64Url, encodeBase64Url } from "./base64url.ts";
import { createSessionSigner, SESSION_ID_LENGTH } from "./session-id.ts";
import {
  EXPIRES,
  flipChar,
  HOST,
  ISSUED_ID,
  ISSUED_ID_DEMO,
  KEY,
  NOW,
  replaceAt,
  sequentialRandom,
} from "./test-helpers.ts";

function signerAt(now: number, key = KEY) {
  return createSessionSigner(key, { now: () => now, randomBytes: sequentialRandom().randomBytes });
}

function payloadOf(id: string): Uint8Array {
  const payload = decodeBase64Url(id.slice(2, 30));
  if (payload === null) throw new Error("bad payload");
  return payload;
}

function expiresOf(id: string): number {
  const payload = payloadOf(id);
  return new DataView(payload.buffer, payload.byteOffset).getUint32(17, false);
}

// 任意の payload に、正しい鍵・ホストで署名を付ける（テストの中だけ）。
async function signPayload(payload: Uint8Array, host: string, key = KEY): Promise<string> {
  const hmac = await crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(key),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const encoded = encodeBase64Url(payload);
  const signature = new Uint8Array(
    await crypto.subtle.sign(
      "HMAC",
      hmac,
      new TextEncoder().encode(`raft-game.session.v1\n${host}\n${encoded}`),
    ),
  );
  return `s_${encoded}.${encodeBase64Url(signature)}`;
}

describe("I-1 既知の答え", () => {
  it("raft.example で発行した値", async () => {
    const id = await signerAt(NOW).issue(HOST, 30);
    expect(id).toBe(ISSUED_ID);
    expect(id).toHaveLength(SESSION_ID_LENGTH);
    expect(SESSION_ID_LENGTH).toBe(74);
    expect(expiresOf(id)).toBe(EXPIRES);
    expect(new Date(EXPIRES * 1000).toISOString()).toBe("2026-11-07T03:35:00.000Z");
  });

  it("raft-demo.example で発行した値", async () => {
    expect(await signerAt(NOW).issue("raft-demo.example", 30)).toBe(ISSUED_ID_DEMO);
  });

  it("payload は版・乱数・期限", () => {
    const payload = payloadOf(ISSUED_ID);
    expect(payload).toHaveLength(21);
    expect(payload[0]).toBe(0x01);
    expect([...payload.slice(1, 17)]).toEqual(Array.from({ length: 16 }, (_, i) => i));
  });
});

describe("I-2 往復", () => {
  it("同じ鍵・同じホストで verify すると同じ文字列が返る", async () => {
    expect(await signerAt(NOW).verify(ISSUED_ID, HOST)).toBe(ISSUED_ID);
    expect(await signerAt(NOW).verify(ISSUED_ID_DEMO, "raft-demo.example")).toBe(ISSUED_ID_DEMO);
    expect(ISSUED_ID).toMatch(/^[A-Za-z0-9._-]{1,256}$/);
  });
});

describe("I-3 期限", () => {
  it("期限の 1 ms 前は通り、期限ちょうどとそれより後は null", async () => {
    expect(await signerAt(EXPIRES * 1000 - 1).verify(ISSUED_ID, HOST)).toBe(ISSUED_ID);
    expect(await signerAt(EXPIRES * 1000).verify(ISSUED_ID, HOST)).toBeNull();
    expect(await signerAt(EXPIRES * 1000 + 1).verify(ISSUED_ID, HOST)).toBeNull();
    expect(await signerAt(EXPIRES * 1000 + 86_400_000).verify(ISSUED_ID, HOST)).toBeNull();
  });

  it.each([
    [15, 20],
    [30, 35],
    [60, 65],
  ])("maxMinutes = %i なら期限は発行 + %i 分", async (maxMinutes, minutes) => {
    const id = await signerAt(NOW).issue(HOST, maxMinutes);
    expect(expiresOf(id)).toBe(NOW / 1000 + minutes * 60);
  });

  it("maxMinutes = 60 の期限は 1794024300", async () => {
    expect(expiresOf(await signerAt(NOW).issue(HOST, 60))).toBe(1794024300);
  });

  it("期限が 2^32 − 1 を超えるなら RangeError", async () => {
    await expect(signerAt(0xffffffff * 1000).issue(HOST, 30)).rejects.toBeInstanceOf(RangeError);
  });
});

async function tamperCases(): Promise<readonly (readonly [string, string, string, string])[]> {
  const payload = ISSUED_ID.slice(2, 30);
  const signature = ISSUED_ID.slice(31);

  const future = payloadOf(ISSUED_ID);
  new DataView(future.buffer, future.byteOffset).setUint32(17, EXPIRES + 3600, false);
  const futureId = `s_${encodeBase64Url(future)}.${signature}`;

  const v2 = payloadOf(ISSUED_ID);
  v2[0] = 0x02;
  const v2Id = await signPayload(v2, HOST);

  expect(signature.endsWith("Y")).toBe(true);
  return [
    ["署名の最初の文字を変える", flipChar(ISSUED_ID, 31), KEY, HOST],
    ["payload の最初の文字（版）を変える", flipChar(ISSUED_ID, 2), KEY, HOST],
    ["payload の乱数の部分を変える", flipChar(ISSUED_ID, 10), KEY, HOST],
    ["payload の期限を未来に変える（署名はそのまま）", futureId, KEY, HOST],
    ["署名の末尾の Y を Z に変える（余りのビット）", replaceAt(ISSUED_ID, 73, "Z"), KEY, HOST],
    ["別の鍵", ISSUED_ID, `${KEY}x`, HOST],
    ["別のホスト（ポート付き）", ISSUED_ID, KEY, "raft.example:8443"],
    ["別のホスト（デモ）", ISSUED_ID, KEY, "raft-demo.example"],
    ["デモで発行した id を一般公開で", ISSUED_ID_DEMO, KEY, HOST],
    ["版が 0x02 の payload に正しい署名", v2Id, KEY, HOST],
    [
      "payload と署名を入れ替える",
      `s_${signature.slice(0, 28)}.${payload}${signature.slice(28)}`,
      KEY,
      HOST,
    ],
  ];
}

describe("I-4 改ざん", () => {
  it("どれも null（例外を投げない）", async () => {
    const cases = await tamperCases();
    for (const [name, id, key, host] of cases) {
      await expect(signerAt(NOW, key).verify(id, host), name).resolves.toBeNull();
    }
  });

  it("余りのビットだけを変えた署名は、寛容な復号では同じバイトになる", () => {
    const original = ISSUED_ID.slice(31);
    const changed = replaceAt(ISSUED_ID, 73, "Z").slice(31);
    // atob は余りのビットを捨てる（寛容な復号）。
    const lenient = (text: string) => atob(text.replace(/-/g, "+").replace(/_/g, "/"));
    expect(lenient(changed)).toBe(lenient(original));
  });

  it("v2 の payload の署名自体は正しい（版の検査で弾かれる）", async () => {
    const v2 = payloadOf(ISSUED_ID);
    v2[0] = 0x02;
    const id = await signPayload(v2, HOST);
    expect(id).toHaveLength(74);
    expect(await signerAt(NOW).verify(id, HOST)).toBeNull();
  });
});

describe("I-5 形式", () => {
  const rest = ISSUED_ID.slice(2);
  it.each([
    ["空", ""],
    ["s_ だけ", "s_"],
    ["s_abc.def（T-307 のテストの値）", "s_abc.def"],
    ["大文字の S_", `S_${rest}`],
    [". が 2 つ", `${ISSUED_ID.slice(0, 40)}.${ISSUED_ID.slice(41)}`],
    ["73 文字", ISSUED_ID.slice(0, 73)],
    ["75 文字", `${ISSUED_ID}A`],
    ["署名なし", ISSUED_ID.slice(0, 30)],
    ["署名なしで . だけ", ISSUED_ID.slice(0, 31)],
    ["+ を含む", replaceAt(ISSUED_ID, 40, "+")],
    ["/ を含む", replaceAt(ISSUED_ID, 40, "/")],
    ["= を含む", replaceAt(ISSUED_ID, 73, "=")],
    ["区切りが . でない", replaceAt(ISSUED_ID, 30, "_")],
    ["前に空白", ` ${ISSUED_ID.slice(1)}`],
    ["末尾に改行", `${ISSUED_ID}\n`],
  ])("%s → null", async (_name, raw) => {
    await expect(signerAt(NOW).verify(raw, HOST)).resolves.toBeNull();
  });
});

describe("I-6 verify は例外を投げない", () => {
  it("I-4・I-5 のすべてで resolves", async () => {
    const cases: (readonly [string, string, string])[] = (await tamperCases()).map(
      ([, raw, key, host]) => [raw, key, host] as const,
    );
    for (const raw of ["", "s_", "s_abc.def", "\u0000".repeat(74), `s_${"é".repeat(72)}`]) {
      cases.push([raw, KEY, HOST]);
    }
    for (const [raw, key, host] of cases) {
      await expect(signerAt(NOW, key).verify(raw, host)).resolves.toBeNull();
    }
  });
});
