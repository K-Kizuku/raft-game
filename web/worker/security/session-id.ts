// sessionId の発行と検証（T-310・ADR-0018 G2・G3・DesignDoc §16・プロトコル §2）。
//
//   sessionId = "s_" + base64url(payload) + "." + base64url(signature)          … 74 文字
//   payload   = 版（0x01） ‖ 乱数（16 バイト） ‖ 期限（UNIX 秒、4 バイト、ビッグエンディアン） … 21 バイト
//   signature = HMAC-SHA256(鍵, "raft-game.session.v1\n" + host + "\n" + base64url(payload)) … 32 バイト
//
// 検証は暗号の計算（crypto.subtle.verify、定数時間）と時計だけで行い、Durable Object・設定・
// ネットワークに触れない。VerifiedSessionId への型の主張は、このファイルの 2 か所だけに置く。

import type { VerifiedSessionId } from "../routes/ports.ts";
import { decodeBase64Url, encodeBase64Url } from "./base64url.ts";

export const SESSION_ID_LENGTH = 74;

const VERSION = 0x01;
const RANDOM_BYTES = 16;
const PAYLOAD_BYTES = 1 + RANDOM_BYTES + 4;
const SIGNATURE_BYTES = 32;
const DOMAIN = "raft-game.session.v1";
const GRACE_MINUTES = 5; // 切断されてからの保持時間（DesignDoc §12.5）
const MAX_UINT32 = 0xffffffff;
const SESSION_ID_FORMAT = /^s_([A-Za-z0-9_-]{28})\.([A-Za-z0-9_-]{43})$/;

export interface SessionSigner {
  issue(host: string, maxMinutes: number): Promise<VerifiedSessionId>;
  verify(raw: string, host: string): Promise<VerifiedSessionId | null>;
}

export interface SessionSignerDeps {
  readonly now: () => number; // UNIX ミリ秒
  readonly randomBytes: (length: number) => Uint8Array;
}

function signedText(host: string, payload: string): Uint8Array<ArrayBuffer> {
  return new TextEncoder().encode(`${DOMAIN}\n${host}\n${payload}`);
}

type HmacKey = Awaited<ReturnType<typeof crypto.subtle.importKey>>;

// 鍵の規則（表示できる ASCII で 32〜256 文字）の検査は呼ぶ側（ports.ts）が行う。
export function createSessionSigner(key: string, deps: SessionSignerDeps): SessionSigner {
  let keyPromise: Promise<HmacKey> | undefined;
  const hmacKey = (): Promise<HmacKey> => {
    keyPromise ??= crypto.subtle.importKey(
      "raw",
      new TextEncoder().encode(key),
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign", "verify"],
    );
    return keyPromise;
  };

  return {
    async issue(host, maxMinutes) {
      const expires = Math.floor(deps.now() / 1000) + (maxMinutes + GRACE_MINUTES) * 60;
      if (!Number.isSafeInteger(expires) || expires < 0 || expires > MAX_UINT32) {
        throw new RangeError("session expiry is out of range");
      }
      const random = deps.randomBytes(RANDOM_BYTES);
      if (random.length !== RANDOM_BYTES)
        throw new RangeError("randomBytes returned a wrong length");

      const payload = new Uint8Array(PAYLOAD_BYTES);
      payload[0] = VERSION;
      payload.set(random, 1);
      new DataView(payload.buffer).setUint32(1 + RANDOM_BYTES, expires, false);
      const encodedPayload = encodeBase64Url(payload);

      const signature = new Uint8Array(
        await crypto.subtle.sign("HMAC", await hmacKey(), signedText(host, encodedPayload)),
      );
      return `s_${encodedPayload}.${encodeBase64Url(signature)}` as VerifiedSessionId;
    },

    async verify(raw, host) {
      try {
        const match = SESSION_ID_FORMAT.exec(raw);
        const encodedPayload = match?.[1];
        const encodedSignature = match?.[2];
        if (encodedPayload === undefined || encodedSignature === undefined) return null;

        const payload = decodeBase64Url(encodedPayload);
        const signature = decodeBase64Url(encodedSignature);
        if (payload?.length !== PAYLOAD_BYTES || signature?.length !== SIGNATURE_BYTES) {
          return null;
        }

        const valid = await crypto.subtle.verify(
          "HMAC",
          await hmacKey(),
          signature,
          signedText(host, encodedPayload),
        );
        if (!valid) return null;
        if (payload[0] !== VERSION) return null;

        const expires = new DataView(payload.buffer, payload.byteOffset).getUint32(
          1 + RANDOM_BYTES,
          false,
        );
        if (!(deps.now() < expires * 1000)) return null;
        return raw as VerifiedSessionId;
      } catch {
        return null;
      }
    },
  };
}
