// 接続とストアをつなぐフック（T-402b・DesignDoc §13.6）。
// 届いたスナップショットとイベントをストアに渡すだけで、Raft の結果を決めない（ADR-0009）。
// 画面が決めるのは cmd.game.start を送る時期だけ（ゲーム画面で最初に受けた intro のスナップショット）。
import { useEffect, useEffectEvent, useState } from "react";

import type { ClientPayload, ConnectionHandlers, ConnectionStatus } from "../../net/index.ts";
import type { SubEngineerPayload } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import type {
  GameCommandPayload,
  GameCommandSender,
  GameCommandType,
} from "../../ui/commands/GameCommands.tsx";
import type { EngineerSubscriber } from "../../ui/engineer/EngineerSubscription.tsx";
import type { SessionHandle } from "../types.ts";
import type { RoundConnector, RoundLink, RoundSource } from "./connector.ts";

export type RoundConnectionView =
  | { kind: "offline" } // 接続しない（session が null で、モックも使わない）
  | { kind: "preparing"; source: RoundSource } // open の Promise を待っている
  | { kind: "connected"; source: RoundSource; status: ConnectionStatus } // 接続を作った（status は onStatus の最後の値）
  | { kind: "unavailable" }; // open が失敗した（例外・Promise の失敗）

export type RoundConnection = {
  view: RoundConnectionView;
  send: GameCommandSender | null;
  subscribe: EngineerSubscriber | null; // sub.engineer（接続があれば。EngineerSubscriptionProvider に渡す）
};

// Entry は effect の 1 回分の状態。session・connector が今の描画と違えば古いものとして読まない。
type Entry = Readonly<{
  session: SessionHandle | null;
  connector: RoundConnector;
  view: RoundConnectionView;
  send: GameCommandSender | null; // link ごとに 1 つ（同じ link の間は同じ関数）
  subscribe: EngineerSubscriber | null;
}>;

const OFFLINE: RoundConnectionView = { kind: "offline" };

function canSend(view: RoundConnectionView): boolean {
  return (
    view.kind === "connected" && (view.status.state === "ready" || view.status.state === "syncing")
  );
}

export function useRoundConnection(args: {
  session: SessionHandle | null;
  connector: RoundConnector;
  onRoundEnd: () => void;
}): RoundConnection {
  const { session, connector } = args;
  const mode = connector.modeOf(session);
  const [entry, setEntry] = useState<Entry | null>(null);

  // App は描画のたびに新しい onRoundEnd を渡すので、effect の依存に入れずに最新の値を呼ぶ。
  const roundEnded = useEffectEvent(() => {
    args.onRoundEnd();
  });

  useEffect(() => {
    if (mode === "offline") {
      return;
    }
    const source: RoundSource = mode;
    let cancelled = false;
    let link: RoundLink | null = null;
    let ended = false; // round.result を受けた
    let startWanted = false; // intro のスナップショットを受け、cmd.game.start をまだ送っていない
    let startPending = false; // 送った cmd.game.start の結果を待っている

    function update(next: Omit<Entry, "session" | "connector">): void {
      if (!cancelled) {
        setEntry({ session, connector, ...next });
      }
    }

    function trySendStart(): void {
      const current = link;
      if (cancelled || current === null || !startWanted || startPending) {
        return;
      }
      const state = current.connection.getStatus().state;
      if (state !== "ready" && state !== "syncing") {
        return; // スナップショットの直後はまだ ready になっていない。onStatus で送る
      }
      startWanted = false;
      startPending = true;
      current.connection.sendCommand("cmd.game.start", {}).then(
        () => {
          startPending = false;
        },
        () => {
          startPending = false;
        },
      );
    }

    const store = gameStore.getState();
    let sender: GameCommandSender | null = null;
    let subscriber: EngineerSubscriber | null = null;
    const handlers: ConnectionHandlers = {
      onSnapshot(snapshot, receivedAt) {
        store.applySnapshot(snapshot, receivedAt);
        startWanted = snapshot.phase === "intro" && !startPending;
        trySendStart();
      },
      onEvents(clock, events, receivedAt) {
        store.applyEvents(clock, events, receivedAt);
      },
      onNodeDetail(detail) {
        store.setNodeDetail(detail);
      },
      onRoundResult(result) {
        ended = true;
        store.setRoundResult(result);
        if (!cancelled) {
          roundEnded();
        }
      },
      onStatus(status) {
        update({
          view: { kind: "connected", source, status },
          send: sender,
          subscribe: subscriber,
        });
        trySendStart();
      },
    };

    function attach(opened: RoundLink): void {
      if (cancelled) {
        opened.dispose();
        return;
      }
      link = opened;
      const connection = opened.connection;
      // GameCommandPayload と ClientPayload は同じ定義（ClientMessage の payload）だが、
      // TypeScript は総称のままでは同じと示せないので、ここだけ型を合わせる。
      sender = <T extends GameCommandType>(type: T, payload: GameCommandPayload<T>) =>
        connection.sendCommand<T>(type, payload as ClientPayload<T>);
      subscriber = (payload: SubEngineerPayload) => {
        connection.setEngineerSubscription(payload);
      };
      update({
        view: { kind: "connected", source, status: connection.getStatus() },
        send: sender,
        subscribe: subscriber,
      });
      connection.start();
    }

    function unavailable(): void {
      update({ view: { kind: "unavailable" }, send: null, subscribe: null });
    }

    try {
      const opened = connector.open(session, handlers);
      if (opened instanceof Promise) {
        opened.then(attach, () => {
          unavailable();
        });
      } else {
        attach(opened);
      }
    } catch {
      unavailable();
    }

    return () => {
      cancelled = true;
      if (link !== null) {
        link.dispose();
        link = null;
      }
      if (!ended) {
        gameStore.getState().reset();
      }
    };
  }, [session, connector, mode]);

  if (mode === "offline") {
    return { view: OFFLINE, send: null, subscribe: null };
  }
  const current = entry !== null && entry.session === session && entry.connector === connector;
  if (!current) {
    return { view: { kind: "preparing", source: mode }, send: null, subscribe: null };
  }
  return {
    view: entry.view,
    send: canSend(entry.view) ? entry.send : null,
    subscribe: entry.subscribe,
  };
}
