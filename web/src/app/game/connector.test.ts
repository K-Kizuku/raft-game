import { afterEach, describe, expect, it, vi } from "vitest";

import {
  createManualTimers,
  type ConnectionHandlers,
  type GameConnection,
  type SocketFactory,
  type SocketHandlers,
} from "../../net/index.ts";
import type { SessionHandle } from "../types.ts";
import { createRoundConnector, openServerLink, type RoundLink } from "./connector.ts";

afterEach(() => {
  vi.unstubAllEnvs();
});

const SESSION: SessionHandle = {
  sessionId: "s_abc.sig",
  token: "tok-1",
  wsPath: "/ws/sessions/s_abc.sig",
};

const handlers: ConnectionHandlers = { onSnapshot: () => {}, onEvents: () => {} };

function fakeLink(source: RoundLink["source"]): RoundLink {
  const connection: GameConnection = {
    start: () => {},
    close: () => {},
    sendCommand: () => Promise.reject(new Error("unused")),
    setEngineerSubscription: () => {},
    getStatus: () => ({ state: "idle", attempt: 0, reason: null }),
  };
  return { source, connection, dispose: () => {} };
}

describe("createRoundConnector", () => {
  it.each([
    { session: SESSION, mockEnabled: true, mode: "server" },
    { session: SESSION, mockEnabled: false, mode: "server" },
    { session: null, mockEnabled: true, mode: "mock" },
    { session: null, mockEnabled: false, mode: "offline" },
  ] as const)(
    "CN-1 modeOf: session $session.sessionId × mockEnabled $mockEnabled → $mode",
    ({ session, mockEnabled, mode }) => {
      const connector = createRoundConnector({
        mockEnabled,
        openServer: () => fakeLink("server"),
        openMock: () => fakeLink("mock"),
      });
      expect(connector.modeOf(session)).toBe(mode);
    },
  );

  it("CN-2 open は server なら openServer、mock なら openMock に渡し、offline では投げる", () => {
    const serverLink = fakeLink("server");
    const mockLink = fakeLink("mock");
    const openServer = vi.fn<(s: SessionHandle, h: ConnectionHandlers) => RoundLink>(
      () => serverLink,
    );
    const openMock = vi.fn<(h: ConnectionHandlers) => RoundLink>(() => mockLink);

    const withMock = createRoundConnector({ mockEnabled: true, openServer, openMock });
    expect(withMock.open(SESSION, handlers)).toBe(serverLink);
    expect(openServer).toHaveBeenCalledWith(SESSION, handlers);
    expect(withMock.open(null, handlers)).toBe(mockLink);
    expect(openMock).toHaveBeenCalledWith(handlers);

    const offline = createRoundConnector({ mockEnabled: false, openServer, openMock });
    expect(() => offline.open(null, handlers)).toThrow(Error);
  });

  it("CN-2 openMock を渡さないとき、既定の openMock（動的 import）が使われる", async () => {
    // 既定の openMock は開発（MODE が development）のときだけ作られる。
    vi.stubEnv("MODE", "development");
    const connector = createRoundConnector({ mockEnabled: true });
    const opened = connector.open(null, handlers);
    expect(opened).toBeInstanceOf(Promise);
    const link = await opened;
    // 既定の openMock は browserTimers を使うので、start() を呼ばずにすぐ片付ける。
    link.dispose();
    expect(link.source).toBe("mock");
  });
});

type ManualSocket = { url: string; handlers: SocketHandlers; closed: number[] };

function manualSocketFactory() {
  const sockets: ManualSocket[] = [];
  const factory: SocketFactory = (url, socketHandlers) => {
    const socket: ManualSocket = { url, handlers: socketHandlers, closed: [] };
    sockets.push(socket);
    return {
      send: () => {},
      close: (code) => {
        socket.closed.push(code ?? 1005);
      },
    };
  };
  return { factory, sockets };
}

describe("openServerLink", () => {
  it("CN-3 wsPath と token の URL で開き、dispose で閉じる", () => {
    const { factory, sockets } = manualSocketFactory();
    const timers = createManualTimers();
    const link = openServerLink(SESSION, handlers, {
      socketFactory: factory,
      timers,
      location: { protocol: "https:", host: "raft.example" },
    });
    expect(link.source).toBe("server");

    link.connection.start();
    expect(sockets).toHaveLength(1);
    const url = sockets[0]?.url ?? "";
    expect(url).toBe("wss://raft.example/ws/sessions/s_abc.sig?token=tok-1");
    // sessionId は wsPath の中にあるだけで、ほかのところ（クエリなど）に入れない。
    expect(url.split(SESSION.sessionId)).toHaveLength(2);
    expect(url).not.toContain("sessionId");

    link.dispose();
    expect(link.connection.getStatus().state).toBe("closed");
    expect(sockets[0]?.closed).toEqual([1000]);
    link.dispose();
    expect(sockets[0]?.closed).toEqual([1000]);
    expect(link.connection.getStatus().state).toBe("closed");
  });
});
