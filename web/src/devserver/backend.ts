// make dev の /api・/ws の行き先を決める（T-504a / ADR-0026）。
// vite.config.ts（Node）と src（ブラウザ）の両方の型検査に入るので、Node の API（process など）を使わない。

/** Vite の server.proxy の 1 項目（vite の ProxyOptions と構造が合う最小の形）。 */
export interface StandaloneProxyEntry {
  readonly target: string; // "http://127.0.0.1:<port>"
  readonly ws: boolean;
  readonly changeOrigin: false;
}

export type DevBackend =
  | { readonly kind: "worker" }
  | {
      readonly kind: "standalone";
      readonly target: string;
      readonly proxy: Readonly<Record<string, StandaloneProxyEntry>>;
    };

export interface ResolveOptions {
  /** vite preview のとき true。 */
  readonly preview: boolean;
}

// キーが ^ で始まると、Vite は正規表現として照合する（/apix・/wsx には当たらない）。
const API_KEY = "^/api(?:[/?]|$)";
const WS_KEY = "^/ws(?:[/?]|$)";

const PORT_RE = /^[1-9][0-9]{0,4}$/;
const MAX_PORT = 65535;

/**
 * 環境変数 env（vite.config.ts が process.env を渡す）から、/api・/ws の行き先を決める。
 * 誤りは Error を投げる（Vite の起動が止まる）。
 */
export function resolveDevBackend(
  env: Readonly<Record<string, string | undefined>>,
  options: ResolveOptions,
): DevBackend {
  const backend = env["DEV_BACKEND"];
  if (backend === undefined || backend === "" || backend === "worker") {
    return { kind: "worker" };
  }
  if (backend !== "standalone") {
    throw new Error(
      `DEV_BACKEND の値が不正: "${backend}"（standalone か worker。未設定は worker）`,
    );
  }
  if (options.preview) {
    throw new Error(
      "DEV_BACKEND=standalone は vite（開発サーバー）だけで使える（vite preview では使えない）",
    );
  }
  const port = env["DEV_SERVER_PORT"];
  if (port === undefined || !PORT_RE.test(port) || Number(port) > MAX_PORT) {
    throw new Error(
      `DEV_BACKEND=standalone には DEV_SERVER_PORT が要る（値: "${port ?? ""}"。1〜65535 の 10 進の整数。make dev が DEV_SLOT から計算して渡す）`,
    );
  }
  const target = `http://127.0.0.1:${port}`;
  // changeOrigin: false で Host と Origin をブラウザの値のまま Go に渡す（Go は ALLOWED_ORIGINS で照合する）。
  return {
    kind: "standalone",
    target,
    proxy: {
      [API_KEY]: { target, ws: false, changeOrigin: false },
      [WS_KEY]: { target, ws: true, changeOrigin: false },
    },
  };
}
