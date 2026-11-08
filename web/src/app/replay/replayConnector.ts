// リプレイの接続（ADR-0028 R4・T-720d）。WASM で作り直した記録を、記録の再生（R3・net/replay）で
// ゲーム画面に流す。ブラウザの中だけで動き、/api/*・/ws/* を呼ばない（セッションを作らない）。
// 開発用の固定の記録を本番のビルドに入れないよう、app/dev/mockRound.ts を import しない。
import {
  browserTimers,
  createGameConnection,
  createRecordServer,
  readOnlyAckPolicy,
  type TimerId,
  type Timers,
} from "../../net/index.ts";
import type { ReplaySuccess } from "../../replay/response.ts";
import type { RoundConnector } from "../game/connector.ts";

// 記録の再生（createRecordServer）の socketFactory だけにつなぐので、実際の WS は開かない。
export const REPLAY_SESSION: Readonly<{ wsPath: string; token: string }> = Object.freeze({
  wsPath: "/ws/sessions/replay",
  token: "replay",
});

type TrackedTimers = Timers & { clearAll(): void };

// trackTimers は作ったタイマーを覚え、clearAll() で全部止める（記録の再生は接続が閉じても
// setInterval を止めないため）。
function trackTimers(base: Timers): TrackedTimers {
  const timeouts = new Set<TimerId>();
  const intervals = new Set<TimerId>();
  return {
    now: () => base.now(),
    setTimeout(fn, ms) {
      const id = base.setTimeout(() => {
        timeouts.delete(id);
        fn();
      }, ms);
      timeouts.add(id);
      return id;
    },
    clearTimeout(id) {
      timeouts.delete(id);
      base.clearTimeout(id);
    },
    setInterval(fn, ms) {
      const id = base.setInterval(fn, ms);
      intervals.add(id);
      return id;
    },
    clearInterval(id) {
      intervals.delete(id);
      base.clearInterval(id);
    },
    clearAll() {
      for (const id of timeouts) {
        base.clearTimeout(id);
      }
      for (const id of intervals) {
        base.clearInterval(id);
      }
      timeouts.clear();
      intervals.clear();
    },
  };
}

export function createReplayConnector(
  outcome: ReplaySuccess,
  options?: { timers?: Timers },
): RoundConnector {
  const timers = options?.timers ?? browserTimers;
  const { record } = outcome;
  return {
    modeOf: () => "replay",
    open(_session, handlers) {
      const serverTimers = trackTimers(timers);
      const server = createRecordServer(record, {
        timers: serverTimers,
        stage: outcome.stage,
        roundResult: outcome.result,
        islandsState: outcome.islandsState,
        score: outcome.score,
        sessionId: `replay_${record.header.stageId}_${record.header.seed}`,
        ackPolicy: readOnlyAckPolicy,
      });
      const connection = createGameConnection({
        session: REPLAY_SESSION,
        handlers,
        socketFactory: server.socketFactory,
        timers,
      });
      let disposed = false;
      return {
        source: "replay",
        connection,
        dispose() {
          if (disposed) {
            return;
          }
          disposed = true;
          connection.close();
          serverTimers.clearAll();
        },
      };
    },
  };
}
