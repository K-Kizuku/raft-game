// 開発用のモックの接続（T-402b・プロトコル §10.6）。記録（#48 の手で書いた記録）を再生するモックのサーバーにつなぐ。
// 本番のビルドに入れないよう、MOCK_ENABLED が真の分岐の動的 import からだけ読む。
// 記録の値（スコア・リザルト）は仮の値なので、画面の確認に使わない。
import {
  browserTimers,
  createGameConnection,
  type ConnectionHandlers,
  type TimerId,
  type Timers,
} from "../../net/index.ts";
import miniRecord from "../../net/fixtures/mini-record.json";
import miniStage from "../../net/fixtures/mini-stage.json";
import { parseRecord } from "../../net/mock/record.ts";
import { createRecordServer } from "../../net/mock/server.ts";
import type { StageView } from "../../protocol/index.ts";
import type { RoundLink } from "../game/connector.ts";
import type { StageCatalogEntry } from "../types.ts";

export const MOCK_SESSION: Readonly<{ wsPath: string; token: string }> = Object.freeze({
  wsPath: "/ws/sessions/mock",
  token: "mock",
});

const record = parseRecord(miniRecord);
const stage = miniStage as StageView;

type TrackedTimers = Timers & { clearAll(): void };

// trackTimers は作ったタイマーを覚え、clearAll() で全部止める（モックのサーバーは接続が閉じても
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

export function createMockRound(
  handlers: ConnectionHandlers,
  options?: { timers?: Timers },
): RoundLink {
  const timers = options?.timers ?? browserTimers;
  const serverTimers = trackTimers(timers);
  const server = createRecordServer(record, { timers: serverTimers, stage });
  const connection = createGameConnection({
    session: MOCK_SESSION,
    handlers,
    socketFactory: server.socketFactory,
    timers,
  });
  let disposed = false;
  return {
    source: "mock",
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
}

// mockStageSummary は記録とステージの表示用の値から、開発用のステージを作る。ステージ選択・紹介の
// 画面が読む紹介文（intro・learning。T-416）は、開発用であることだけを書く（学べることは無し）。
export function mockStageSummary(): StageCatalogEntry {
  return {
    id: record.header.stageId,
    title: `${stage.title}（開発用の記録）`,
    region: "開発用",
    islands: stage.islands.length,
    ferries: record.snapshot.ferries.length,
    durationMs: record.header.durationMs,
    intro: {
      text: "開発用の記録を再生するステージです（スコアなどは仮の値です）。",
      provenance: "creative",
      source: null,
    },
    learning: [],
  };
}
