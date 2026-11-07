// テスト用の記録（simcli record の format: 1、T-108）をスナップショットとイベント列にする（テストだけが使う）。
// 記録の形の検査はしない（WS クライアントのモック、T-402 の役目）。値は T-402 の snapshotFromRecord と同じ。
import type { ServerEvent, Snapshot, StageView } from "../../protocol/index.ts";
import record from "./mini-record.json";
import stage from "./mini-stage.json";

type RecordSnapshot = Pick<
  Snapshot,
  "phase" | "ferries" | "routesState" | "letters" | "nodes" | "lastSeq"
> & { t: number; endsAt: number };

type MiniRecord = { snapshot: RecordSnapshot; events: ServerEvent[] };

export function loadMiniRound(): { snapshot: Snapshot; events: ServerEvent[] } {
  // JSON を読み直した値にして、テストどうしで同じオブジェクトを共有しない。
  const data = JSON.parse(JSON.stringify(record)) as unknown as MiniRecord;
  const stageView = JSON.parse(JSON.stringify(stage)) as unknown as StageView;
  const recorded = data.snapshot;
  const snapshot: Snapshot = {
    protocol: 1,
    session: { id: "mock_mini_1", stageId: "mini", seed: 1, demo: false, restored: false },
    phase: recorded.phase,
    clock: { t: 0, speed: 1, paused: false, endsAt: 60000, allowedSpeeds: [0.5, 1, 2, 4] },
    stage: stageView,
    ferries: recorded.ferries,
    routesState: recorded.routesState,
    letters: recorded.letters,
    requests: [],
    nodes: recorded.nodes,
    islandsState: [],
    score: { total: 0, successes: 0, target: 0, criticalSecured: [] },
    lastSeq: recorded.lastSeq,
  };
  return { snapshot, events: data.events };
}
