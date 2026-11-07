// ラウンドの接続の作り方（T-402b・DesignDoc §13.6）。
// セッションがあれば本物の接続、無ければ（開発のときだけ）記録を再生するモック、どちらでもなければ接続しない。
import {
  createGameConnection,
  type ConnectionHandlers,
  type GameConnection,
  type GameConnectionOptions,
} from "../../net/index.ts";
import { MOCK_ENABLED } from "../dev/mockMode.ts";
import type { SessionHandle } from "../types.ts";

export type RoundSource = "server" | "mock";
export type RoundMode = "offline" | RoundSource;

// 開いた接続 1 本。dispose は接続を閉じ、その接続のために作ったもの（モックのタイマー）を片付ける。何度呼んでもよい。
export type RoundLink = Readonly<{
  source: RoundSource;
  connection: GameConnection;
  dispose(): void;
}>;

export type RoundConnector = Readonly<{
  // 純粋。描画の中で呼んでよい。
  modeOf(session: SessionHandle | null): RoundMode;
  // modeOf が offline でないときだけ呼ぶ。接続を作るが start() は呼ばない。
  open(session: SessionHandle | null, handlers: ConnectionHandlers): RoundLink | Promise<RoundLink>;
}>;

export type RoundConnectorOptions = {
  mockEnabled: boolean;
  openServer?: (session: SessionHandle, handlers: ConnectionHandlers) => RoundLink; // 既定 openServerLink
  openMock?: (handlers: ConnectionHandlers) => RoundLink | Promise<RoundLink>; // 既定 dev/mockRound.ts を動的 import
};

// 既定の openMock。本番のビルド（MODE が development でない）では undefined になり、
// 動的 import の分岐ごと消える（モックと記録を本番のビルドに入れない）。
function defaultOpenMock(): RoundConnectorOptions["openMock"] {
  if (import.meta.env.MODE === "development") {
    return async (handlers) => {
      const { createMockRound } = await import("../dev/mockRound.ts");
      return createMockRound(handlers);
    };
  }
  return undefined;
}

export function createRoundConnector(options: RoundConnectorOptions): RoundConnector {
  const { mockEnabled } = options;
  const openServer = options.openServer ?? openServerLink;
  const openMock = options.openMock ?? (mockEnabled ? defaultOpenMock() : undefined);

  function modeOf(session: SessionHandle | null): RoundMode {
    if (session !== null) {
      return "server";
    }
    return mockEnabled ? "mock" : "offline";
  }

  return {
    modeOf,
    open(session, handlers) {
      const mode = modeOf(session);
      if (mode === "server" && session !== null) {
        return openServer(session, handlers);
      }
      if (mode === "mock") {
        if (openMock === undefined) {
          throw new Error("RoundConnector: mock is not available");
        }
        return openMock(handlers);
      }
      throw new Error("RoundConnector: open called in offline mode");
    },
  };
}

export const defaultRoundConnector: RoundConnector = createRoundConnector({
  mockEnabled: MOCK_ENABLED,
});

// openServerLink は本物の接続を作る。session の wsPath・token だけを渡す（sessionId は渡さない）。
export function openServerLink(
  session: SessionHandle,
  handlers: ConnectionHandlers,
  overrides?: Pick<GameConnectionOptions, "socketFactory" | "timers" | "location">,
): RoundLink {
  const connection = createGameConnection({
    session: { wsPath: session.wsPath, token: session.token },
    handlers,
    ...overrides,
  });
  return {
    source: "server",
    connection,
    dispose() {
      connection.close();
    },
  };
}
