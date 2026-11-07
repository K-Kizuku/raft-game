// simcli record の記録（T-108 の「記録の形式」format: 1）を読む（T-402 / プロトコル §10.6）。開発・テスト用。
import { Version } from "../../protocol/index.ts";
import type {
  Ferry,
  Letter,
  NodeSummary,
  Phase,
  RouteState,
  ServerEvent,
  Snapshot,
  StageView,
} from "../../protocol/index.ts";

export type RecordHeader = Readonly<{
  format: 1;
  kind: "simcli.record";
  build: string;
  stageId: string;
  stageHash: string;
  seed: number;
  bot: string;
  durationMs: number;
  eventCount: number;
  traceHash: string;
}>;

// sim.Snapshot の JSON（プロトコル §5.1 のうち Engine が持つ分）。
export type RecordSnapshot = Readonly<{
  phase: Phase;
  t: number;
  endsAt: number;
  ferries: Ferry[];
  routesState: RouteState[];
  letters: Letter[];
  nodes: NodeSummary[];
  lastSeq: number;
}>;

export type GameRecord = Readonly<{
  header: RecordHeader;
  snapshot: RecordSnapshot;
  events: readonly ServerEvent[];
}>;

export class RecordFormatError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RecordFormatError";
  }
}

type Obj = Record<string, unknown>;

function isObject(value: unknown): value is Obj {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNonNegInt(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 0;
}

function fail(where: string): never {
  throw new RecordFormatError(`invalid record: ${where}`);
}

const HEADER_STRING_KEYS = ["build", "stageId", "stageHash", "bot", "traceHash"] as const;

// parseRecord は JSON.parse した記録を確かめて GameRecord にする。入力と配列を共有しない。
export function parseRecord(value: unknown): GameRecord {
  if (!isObject(value)) {
    fail("not an object");
  }
  if (value["format"] !== 1) {
    fail("format");
  }
  if (value["kind"] !== "simcli.record") {
    fail("kind");
  }
  for (const key of HEADER_STRING_KEYS) {
    if (typeof value[key] !== "string") {
      fail(key);
    }
  }
  const seed = value["seed"];
  if (!isNonNegInt(seed)) {
    fail("seed");
  }
  const durationMs = value["durationMs"];
  if (!isNonNegInt(durationMs) || durationMs < 1) {
    fail("durationMs");
  }

  const snap = value["snapshot"];
  if (!isObject(snap)) {
    fail("snapshot");
  }
  if (typeof snap["phase"] !== "string") {
    fail("snapshot.phase");
  }
  for (const key of ["t", "endsAt", "lastSeq"] as const) {
    if (!isNonNegInt(snap[key])) {
      fail(`snapshot.${key}`);
    }
  }
  for (const key of ["ferries", "routesState", "letters", "nodes"] as const) {
    if (!Array.isArray(snap[key])) {
      fail(`snapshot.${key}`);
    }
  }
  if (snap["endsAt"] !== durationMs) {
    fail("snapshot.endsAt");
  }
  const snapT = snap["t"] as number;
  const lastSeq = snap["lastSeq"] as number;

  const events = value["events"];
  if (!Array.isArray(events)) {
    fail("events");
  }
  if (value["eventCount"] !== events.length) {
    fail("eventCount");
  }
  let prevT = snapT;
  events.forEach((event: unknown, i: number) => {
    if (!isObject(event)) {
      fail(`events[${i}]`);
    }
    if (typeof event["type"] !== "string" || event["type"] === "") {
      fail(`events[${i}].type`);
    }
    const t = event["t"];
    if (!isNonNegInt(t)) {
      fail(`events[${i}].t`);
    }
    if (event["seq"] !== lastSeq + 1 + i) {
      fail(`events[${i}].seq`);
    }
    if (t < prevT) {
      fail(`events[${i}].t`);
    }
    prevT = t;
  });

  const header: RecordHeader = {
    format: 1,
    kind: "simcli.record",
    build: value["build"] as string,
    stageId: value["stageId"] as string,
    stageHash: value["stageHash"] as string,
    seed,
    bot: value["bot"] as string,
    durationMs,
    eventCount: events.length,
    traceHash: value["traceHash"] as string,
  };
  const snapshot: RecordSnapshot = {
    phase: snap["phase"] as Phase,
    t: snapT,
    endsAt: snap["endsAt"] as number,
    ferries: [...(snap["ferries"] as Ferry[])],
    routesState: [...(snap["routesState"] as RouteState[])],
    letters: [...(snap["letters"] as Letter[])],
    nodes: [...(snap["nodes"] as NodeSummary[])],
    lastSeq,
  };
  return { header, snapshot, events: [...(events as ServerEvent[])] };
}

// DEFAULT_ALLOWED_SPEEDS はプロトコル §4 の cmd.game.speed の値。
export const DEFAULT_ALLOWED_SPEEDS: readonly number[] = Object.freeze([0.5, 1, 2, 4]);

// snapshotFromRecord はプロトコル §5.1 の Snapshot を作る。記録に無い分はモックの値で埋める
// （画面の確認に使わない）。
export function snapshotFromRecord(
  record: GameRecord,
  options?: { stage?: StageView; allowedSpeeds?: readonly number[] },
): Snapshot {
  const { header, snapshot } = record;
  const stage: StageView = options?.stage ?? {
    title: header.stageId,
    islands: [],
    routes: [],
    resources: [],
  };
  return {
    protocol: Version,
    session: {
      id: `mock_${header.stageId}_${header.seed}`,
      stageId: header.stageId,
      seed: header.seed,
      demo: false,
      restored: false,
    },
    phase: snapshot.phase,
    clock: {
      t: snapshot.t,
      speed: 1,
      paused: false,
      endsAt: snapshot.endsAt,
      allowedSpeeds: [...(options?.allowedSpeeds ?? DEFAULT_ALLOWED_SPEEDS)],
    },
    stage,
    ferries: [...snapshot.ferries],
    routesState: [...snapshot.routesState],
    letters: [...snapshot.letters],
    requests: [],
    nodes: [...snapshot.nodes],
    islandsState: [],
    score: { total: 0, successes: 0, target: 0, criticalSecured: [] },
    lastSeq: snapshot.lastSeq,
  };
}
