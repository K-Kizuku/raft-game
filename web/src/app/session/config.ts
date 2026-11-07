// Turnstile のサイトキー（T-413）。ビルドのときの環境変数 VITE_TURNSTILE_SITE_KEY から読む。
// サイトキーは秘密ではない（ブラウザに配る値）が、リポジトリには書かず、デプロイのビルドで渡す（T-603）。
// 未設定・形が違うときは null で、ウィジェットを作らず、スクリプトも読み込まず、トークンを送らない。

const SITE_KEY_PATTERN = /^[0-9A-Za-z_-]{1,128}$/;

export function turnstileSiteKeyFrom(value: string | undefined): string | null {
  if (value === undefined) {
    return null;
  }
  const trimmed = value.trim();
  return SITE_KEY_PATTERN.test(trimmed) ? trimmed : null;
}

export const TURNSTILE_SITE_KEY: string | null = turnstileSiteKeyFrom(
  import.meta.env.VITE_TURNSTILE_SITE_KEY,
);
