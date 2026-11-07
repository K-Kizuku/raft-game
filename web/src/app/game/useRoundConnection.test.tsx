// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import miniRecord from "../../net/fixtures/mini-record.json";
import miniStage from "../../net/fixtures/mini-stage.json";
import {
  CommandError,
  type ConnectionHandlers,
  type ConnectionState,
  type ConnectionStatus,
  type FailureReason,
  type GameConnection,
} from "../../net/index.ts";
import { parseRecord, snapshotFromRecord } from "../../net/mock/record.ts";
import { placeholderRoundResult } from "../../net/mock/server.ts";
import type {
  Ack,
  NodeDetail,
  Phase,
  ServerEvent,
  Snapshot,
  StageView,
} from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import type { SessionHandle } from "../types.ts";
import type { RoundConnector, RoundLink } from "./connector.ts";
import { useRoundConnection } from "./useRoundConnection.ts";

const record = parseRecord(miniRecord);

function snapshot(phase: Phase): Snapshot {
  return { ...snapshotFromRecord(record, { stage: miniStage as StageView }), phase };
}

const SESSION: SessionHandle = {
  sessionId: "s_1.sig",
  token: "t1",
  wsPath: "/ws/sessions/s_1.sig",
};

type SentCommand = {
  type: string;
  payload: unknown;
  resolve(ack: Ack): void;
  reject(error: unknown): void;
};

// 手で動かす接続。start・close・sendCommand を記録し、テストからハンドラーを呼ぶ。
function manualLink(handlers: ConnectionHandlers) {
  let status: ConnectionStatus = { state: "idle", attempt: 0, reason: null };
  const sent: SentCommand[] = [];
  const counts = { start: 0, close: 0, dispose: 0 };
  const connection: GameConnection = {
    start() {
      counts.start++;
    },
    close() {
      counts.close++;
    },
    sendCommand(type, payload) {
      return new Promise<Ack>((resolve, reject) => {
        sent.push({ type, payload, resolve, reject });
      });
    },
    setEngineerSubscription() {},
    getStatus: () => status,
  };
  const link: RoundLink = {
    source: "server",
    connection,
    dispose() {
      counts.dispose++;
    },
  };
  return {
    link,
    handlers,
    sent,
    counts,
    status(state: ConnectionState, reason: FailureReason | null = null) {
      status = { state, attempt: 1, reason };
      act(() => {
        handlers.onStatus?.(status);
      });
    },
    snapshot(phase: Phase) {
      act(() => {
        handlers.onSnapshot(snapshot(phase), 0);
      });
    },
  };
}

type ManualLink = ReturnType<typeof manualLink>;

function manualConnector(options: { async?: boolean } = {}) {
  const links: ManualLink[] = [];
  const pending: { resolve(link: RoundLink): void; reject(error: unknown): void }[] = [];
  const open = vi.fn((_session: SessionHandle | null, handlers: ConnectionHandlers) => {
    const m = manualLink(handlers);
    links.push(m);
    if (options.async === true) {
      return new Promise<RoundLink>((resolve, reject) => {
        pending.push({ resolve: () => resolve(m.link), reject });
      });
    }
    return m.link;
  });
  const connector: RoundConnector = {
    modeOf: (session) => (session === null ? "offline" : "server"),
    open,
  };
  return { connector, open, links, pending };
}

function last(links: ManualLink[]): ManualLink {
  const link = links.at(-1);
  if (link === undefined) {
    throw new Error("no link");
  }
  return link;
}

const okAck: Ack = { cmdId: "c-1", ok: true, t: 0 };
const ngAck: Ack = { cmdId: "c-1", ok: false, t: 0, code: "phase_invalid", message: "no" };

beforeEach(() => {
  gameStore.getState().reset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
});

describe("useRoundConnection", () => {
  it("HC-1 offline では open を呼ばない", () => {
    const { connector, open } = manualConnector();
    const { result } = renderHook(() =>
      useRoundConnection({ session: null, connector, onRoundEnd: () => {} }),
    );
    expect(result.current.view).toEqual({ kind: "offline" });
    expect(result.current.send).toBeNull();
    expect(open).not.toHaveBeenCalled();
  });

  it("HC-2 同期の open と send", () => {
    const { connector, open, links } = manualConnector();
    const { result } = renderHook(() =>
      useRoundConnection({ session: SESSION, connector, onRoundEnd: () => {} }),
    );
    expect(open).toHaveBeenCalledTimes(1);
    const m = last(links);
    expect(m.counts.start).toBe(1);

    m.status("connecting");
    expect(result.current.view).toEqual({
      kind: "connected",
      source: "server",
      status: { state: "connecting", attempt: 1, reason: null },
    });
    expect(result.current.send).toBeNull();

    m.status("ready");
    const send = result.current.send;
    expect(send).toBeTypeOf("function");
    void send?.("cmd.ferry.depart", { ferryId: "f1", to: 3 });
    expect(m.sent.map((s) => ({ type: s.type, payload: s.payload }))).toEqual([
      { type: "cmd.ferry.depart", payload: { ferryId: "f1", to: 3 } },
    ]);
    m.status("syncing");
    expect(result.current.send).toBe(send);
    m.status("ready");
    expect(result.current.send).toBe(send);

    m.status("reconnecting");
    expect(result.current.send).toBeNull();
  });

  it("HC-3 スナップショット・イベント・node.detail をストアに入れる", () => {
    const { connector, links } = manualConnector();
    renderHook(() => useRoundConnection({ session: SESSION, connector, onRoundEnd: () => {} }));
    const m = last(links);
    m.status("connecting");
    m.snapshot("sailing");
    const world = gameStore.getState().world;
    expect(world?.stage.title).toBe("テストの海");

    const events: ServerEvent[] = [{ seq: 1, t: 1000, type: "phase.changed", phase: "ended" }];
    const clock = { t: 1500, speed: 1, paused: false, endsAt: 60000 };
    act(() => {
      m.handlers.onEvents(clock, events, 10);
    });
    expect(gameStore.getState().world?.phase).toBe("ended");
    expect(gameStore.getState().world?.clock.simTime).toBe(1500);

    const detail: NodeDetail = {
      id: 1,
      role: "leader",
      term: 2,
      vote: 1,
      lead: 1,
      commit: 3,
      applied: 3,
      lastIndex: 3,
      log: [],
      ledger: {},
      electionEstimate: { ticksSinceLeaderContact: 0, note: "" },
    };
    act(() => {
      m.handlers.onNodeDetail?.(detail);
    });
    expect(gameStore.getState().nodeDetail).toEqual(detail);
  });

  it("HC-4 intro のスナップショットで cmd.game.start を 1 つだけ送る", async () => {
    const { connector, links } = manualConnector();
    renderHook(() => useRoundConnection({ session: SESSION, connector, onRoundEnd: () => {} }));
    const m = last(links);
    const starts = () => m.sent.filter((s) => s.type === "cmd.game.start");
    m.status("connecting");

    // 本物の接続と同じく、スナップショットの後に ready になる。
    m.snapshot("intro");
    m.status("ready");
    expect(starts()).toHaveLength(1);
    expect(starts()[0]?.payload).toEqual({});

    // 結果を待っている間の intro では送らない。
    m.snapshot("intro");
    expect(starts()).toHaveLength(1);

    // ok: false で終わったら、次の intro で送り直す。
    await act(async () => {
      starts()[0]?.resolve(ngAck);
    });
    m.snapshot("intro");
    expect(starts()).toHaveLength(2);

    // CommandError で終わったら、次の intro で送り直す（未処理の失敗にしない）。
    await act(async () => {
      starts()[1]?.reject(new CommandError("disconnected"));
    });
    m.snapshot("intro");
    expect(starts()).toHaveLength(3);
    await act(async () => {
      starts()[2]?.resolve(okAck);
    });

    // intro 以外では送らない。
    m.snapshot("sailing");
    m.snapshot("opening");
    expect(starts()).toHaveLength(3);
  });

  it("HC-5 round.result で結果を入れてから onRoundEnd を呼び、離れても消さない", () => {
    const { connector, links } = manualConnector();
    const result = placeholderRoundResult(record);
    const seen: unknown[] = [];
    const onRoundEnd = vi.fn(() => {
      seen.push(gameStore.getState().roundResult);
    });
    const { unmount } = renderHook(() =>
      useRoundConnection({ session: SESSION, connector, onRoundEnd }),
    );
    const m = last(links);
    m.status("connecting");
    m.snapshot("sailing");
    m.status("ready");
    act(() => {
      m.handlers.onRoundResult?.(result);
    });
    expect(gameStore.getState().roundResult).toBe(result);
    expect(onRoundEnd).toHaveBeenCalledTimes(1);
    expect(seen).toEqual([result]);

    unmount();
    expect(m.counts.dispose).toBe(1);
    expect(gameStore.getState().roundResult).toBe(result);
    expect(gameStore.getState().world).not.toBeNull();
  });

  it("HC-6 途中で離れると dispose してストアを reset する", () => {
    const { connector, links } = manualConnector();
    const { unmount } = renderHook(() =>
      useRoundConnection({ session: SESSION, connector, onRoundEnd: () => {} }),
    );
    const m = last(links);
    m.status("connecting");
    m.snapshot("sailing");
    expect(gameStore.getState().world).not.toBeNull();
    unmount();
    expect(m.counts.dispose).toBe(1);
    expect(gameStore.getState().world).toBeNull();
    expect(gameStore.getState().roundResult).toBeNull();
  });

  it("HC-7 onRoundEnd が変わっても接続を作り直さず、最新のものを呼ぶ", () => {
    const { connector, open, links } = manualConnector();
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = renderHook(
      ({ onRoundEnd }: { onRoundEnd: () => void }) =>
        useRoundConnection({ session: SESSION, connector, onRoundEnd }),
      { initialProps: { onRoundEnd: first } },
    );
    rerender({ onRoundEnd: second });
    rerender({ onRoundEnd: second });
    expect(open).toHaveBeenCalledTimes(1);
    act(() => {
      last(links).handlers.onRoundResult?.(placeholderRoundResult(record));
    });
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
  });

  it("HC-8 非同期の open: 解決すると start する", async () => {
    const { connector, links, pending } = manualConnector({ async: true });
    const { result } = renderHook(() =>
      useRoundConnection({ session: SESSION, connector, onRoundEnd: () => {} }),
    );
    expect(result.current.view).toEqual({ kind: "preparing", source: "server" });
    expect(last(links).counts.start).toBe(0);
    await act(async () => {
      pending[0]?.resolve(last(links).link);
    });
    expect(last(links).counts.start).toBe(1);
    expect(result.current.view).toMatchObject({ kind: "connected", source: "server" });
  });

  it("HC-8 非同期の open: 解決の前に離れたら、解決した接続をすぐ片付け、start しない", async () => {
    const { connector, links, pending } = manualConnector({ async: true });
    const { unmount } = renderHook(() =>
      useRoundConnection({ session: SESSION, connector, onRoundEnd: () => {} }),
    );
    unmount();
    const m = last(links);
    await act(async () => {
      pending[0]?.resolve(m.link);
    });
    expect(m.counts.dispose).toBe(1);
    expect(m.counts.start).toBe(0);
    expect(gameStore.getState().world).toBeNull();
  });

  it("HC-8 open の失敗（Promise の失敗・例外）は unavailable", async () => {
    const { connector, pending } = manualConnector({ async: true });
    const { result } = renderHook(() =>
      useRoundConnection({ session: SESSION, connector, onRoundEnd: () => {} }),
    );
    await act(async () => {
      pending[0]?.reject(new Error("import failed"));
    });
    expect(result.current.view).toEqual({ kind: "unavailable" });
    expect(result.current.send).toBeNull();

    const throwing: RoundConnector = {
      modeOf: () => "mock",
      open: () => {
        throw new Error("boom");
      },
    };
    const thrown = renderHook(() =>
      useRoundConnection({ session: null, connector: throwing, onRoundEnd: () => {} }),
    );
    expect(thrown.result.current.view).toEqual({ kind: "unavailable" });
  });

  it("HC-9 session が変わると前の接続を片付けて新しく開く", () => {
    const { connector, open, links } = manualConnector();
    const { rerender } = renderHook(
      ({ session }: { session: SessionHandle }) =>
        useRoundConnection({ session, connector, onRoundEnd: () => {} }),
      { initialProps: { session: SESSION } },
    );
    const first = last(links);
    rerender({ session: { sessionId: "s_2.sig", token: "t2", wsPath: "/ws/sessions/s_2.sig" } });
    expect(first.counts.dispose).toBe(1);
    expect(open).toHaveBeenCalledTimes(2);
    expect(open.mock.calls[1]?.[0]?.sessionId).toBe("s_2.sig");
    expect(last(links).counts.start).toBe(1);
  });
});
