// ステージの進み具合（クリアしたステージの最高の★）の保存（T-703・FR-GAME-08・DesignDoc §13.9）。
// 設定（ui/terms/preference.ts・DesignDoc §13.7）と同じく、モジュールに 1 つの外部ストア＋
// useSyncExternalStore で持つ。localStorage に触れるのは openBrowserStorage を通してだけ。
import { useSyncExternalStore } from "react";

import { openBrowserStorage, type StorageLike } from "../../ui/terms/preference.ts";
import { STAGE_ID_PATTERN } from "../router/routes.ts";

// localStorage のキー。値は JSON の文字列 {"v":1,"bestStars":{"stage1":2}}。
export const PROGRESS_STORAGE_KEY = "raft-game.progress";
export const PROGRESS_VERSION = 1;
// ★1 以上でクリア（docs/04 §5.3 の★の表。ステージで変えない画面の規則）。
export const CLEAR_STARS = 1;

const MAX_STARS = 3;
// 読むときに見る要素の数の上限。
const MAX_ENTRIES = 64;

export type StageProgress = Readonly<{
  // クリアしたステージの最高の★（1〜3）。クリアしていないステージは入れない。
  bestStars: Readonly<Record<string, number>>;
  // 今の値がブラウザに保存されているか。
  persistent: boolean;
}>;

export type StageProgressStore = {
  getSnapshot(): StageProgress;
  subscribe(listener: () => void): () => void;
  // stars < CLEAR_STARS なら何もしない。
  recordResult(stageId: string, stars: number): { firstClear: boolean };
  clearAll(): void;
  reload(): void;
};

type Loaded = { storage: StorageLike | null; progress: StageProgress };

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isStars(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 1 && value <= MAX_STARS;
}

// 壊れている・別の版なら空。各要素は形の合うものだけを残す。
function parseBestStars(raw: string): Record<string, number> {
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return {};
  }
  if (!isRecord(value) || value["v"] !== PROGRESS_VERSION) {
    return {};
  }
  const source = value["bestStars"];
  if (!isRecord(source)) {
    return {};
  }
  const bestStars: Record<string, number> = {};
  let count = 0;
  for (const [stageId, stars] of Object.entries(source)) {
    if (count >= MAX_ENTRIES) {
      break;
    }
    count++;
    if (STAGE_ID_PATTERN.test(stageId) && isStars(stars)) {
      bestStars[stageId] = stars;
    }
  }
  return bestStars;
}

function load(openStorage: () => StorageLike | null): Loaded {
  let storage: StorageLike | null;
  let raw: string | null;
  try {
    storage = openStorage();
    if (storage === null) {
      return { storage: null, progress: { bestStars: {}, persistent: false } };
    }
    raw = storage.getItem(PROGRESS_STORAGE_KEY);
  } catch {
    return { storage: null, progress: { bestStars: {}, persistent: false } };
  }
  if (raw === null) {
    return { storage, progress: { bestStars: {}, persistent: true } };
  }
  return { storage, progress: { bestStars: parseBestStars(raw), persistent: true } };
}

function sameBestStars(
  a: Readonly<Record<string, number>>,
  b: Readonly<Record<string, number>>,
): boolean {
  const keys = Object.keys(a);
  if (keys.length !== Object.keys(b).length) {
    return false;
  }
  return keys.every((key) => Object.hasOwn(b, key) && a[key] === b[key]);
}

export function createStageProgressStore(
  openStorage: () => StorageLike | null,
): StageProgressStore {
  const listeners = new Set<() => void>();
  let loaded: Loaded | null = null;

  function notify(): void {
    for (const listener of [...listeners]) {
      listener();
    }
  }

  function ensureLoaded(): Loaded {
    if (loaded === null) {
      loaded = load(openStorage);
    }
    return loaded;
  }

  // 保存して、画面に効かせる。保存できなくても画面には効かせる（persistent: false）。
  function save(current: Loaded, bestStars: Record<string, number>): void {
    let persistent = false;
    if (current.storage !== null) {
      try {
        current.storage.setItem(
          PROGRESS_STORAGE_KEY,
          JSON.stringify({ v: PROGRESS_VERSION, bestStars }),
        );
        persistent = true;
      } catch {
        persistent = false;
      }
    }
    loaded = { storage: current.storage, progress: { bestStars, persistent } };
  }

  function getSnapshot(): StageProgress {
    return ensureLoaded().progress;
  }

  function subscribe(listener: () => void): () => void {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }

  function recordResult(stageId: string, stars: number): { firstClear: boolean } {
    if (!Number.isInteger(stars) || stars < CLEAR_STARS) {
      return { firstClear: false };
    }
    const capped = Math.min(stars, MAX_STARS);
    const current = ensureLoaded();
    const previous = current.progress.bestStars;
    const firstClear = !Object.hasOwn(previous, stageId);
    const best = previous[stageId];
    if (best !== undefined && best >= capped) {
      return { firstClear };
    }
    save(current, { ...previous, [stageId]: capped });
    notify();
    return { firstClear };
  }

  function clearAll(): void {
    const current = ensureLoaded();
    const previous = current.progress;
    save(current, {});
    if (Object.keys(previous.bestStars).length !== 0) {
      notify();
      return;
    }
    // 空のままなら、保存できたかが変わったときだけ知らせる（変わらない間は同じオブジェクトを返す）。
    const next = ensureLoaded();
    if (next.progress.persistent === previous.persistent) {
      loaded = { storage: next.storage, progress: previous };
      return;
    }
    notify();
  }

  function reload(): void {
    const previous = loaded;
    const next = load(openStorage);
    if (
      previous !== null &&
      previous.progress.persistent === next.progress.persistent &&
      sameBestStars(previous.progress.bestStars, next.progress.bestStars)
    ) {
      loaded = { storage: next.storage, progress: previous.progress };
      return;
    }
    loaded = next;
    notify();
  }

  return { getSnapshot, subscribe, recordResult, clearAll, reload };
}

export const stageProgress: StageProgressStore = createStageProgressStore(openBrowserStorage);

export function useStageProgress(): StageProgress {
  return useSyncExternalStore(
    stageProgress.subscribe,
    stageProgress.getSnapshot,
    stageProgress.getSnapshot,
  );
}
