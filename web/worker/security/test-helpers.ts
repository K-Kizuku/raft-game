// テスト用の共通の値と偽の部品（本番のコードからは import しない）。
// 鍵とシークレットは明らかに作り物の値と、Cloudflare の Turnstile のテスト用のキーだけ。

export const KEY = "test-signing-key-0123456789abcdef"; // 33 文字
export const HOST = "raft.example";
export const NOW = 1794020400000; // 2026-11-07T03:00:00.000Z
export const EXPIRES = 1794022500; // NOW + 35 分（UNIX 秒）

export const ISSUED_ID =
  "s_AQABAgMEBQYHCAkKCwwNDg9q7pxk.v0jz489t4xQNr38bD_b7GFK0a-sktI3E_fkrUjVlHhY";
export const ISSUED_ID_DEMO =
  "s_AQABAgMEBQYHCAkKCwwNDg9q7pxk.BMu8AWCzV9RVviqHyO-7doOrO9nijRKpjuI60lIPaVg";
export const ISSUED_TOKEN = "EBESExQVFhcYGRobHB0eHw";
export const ISSUED_TOKEN_HASH = "1af3787867727da9403759b9fe483b6ba275698f43098082e3495985f49a3428";

export const TURNSTILE_PASS_SECRET = "1x0000000000000000000000000000000AA";
export const TURNSTILE_DUMMY_TOKEN = "XXXX.DUMMY.TOKEN.XXXX";

export interface RecordedRandom {
  readonly randomBytes: (length: number) => Uint8Array;
  readonly lengths: number[];
}

// n 回目（0 から）の呼び出しは 16n, 16n+1, … を返す（1 回目は 0x00〜0x0f、2 回目は 0x10〜0x1f）。
// 17 回目以降も重ならないよう、最初のバイトに floor(n / 16) を足す。
export function sequentialRandom(): RecordedRandom {
  const lengths: number[] = [];
  let call = 0;
  return {
    lengths,
    randomBytes(length) {
      lengths.push(length);
      const start = call * 16;
      const round = Math.floor(call / 16);
      call += 1;
      return Uint8Array.from({ length }, (_, i) => (start + i + (i === 0 ? round : 0)) & 0xff);
    },
  };
}

// 1 文字を別の base64url の文字に変える。
export function replaceAt(text: string, index: number, char: string): string {
  return text.slice(0, index) + char + text.slice(index + 1);
}

export function flipChar(text: string, index: number): string {
  const current = text.charAt(index);
  return replaceAt(text, index, current === "A" ? "B" : "A");
}
