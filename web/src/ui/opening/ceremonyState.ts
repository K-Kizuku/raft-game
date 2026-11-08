// 開会式の演出を出したかどうか（T-415）。ゲームの世界の状態ではないので T-403 のストアに入れず、
// T-412 の termsPreference（DesignDoc §13.7）と同じく、モジュールに 1 つの外部ストア＋useSyncExternalStore で持つ。
// doneKeys はページの寿命の間だけ持つ（ラウンドの数だけ増える。保存しない）。
import { useSyncExternalStore } from "react";

import { useGameStore } from "../../state/index.ts";
import { openingResult } from "./openingView.ts";
import type { OpeningResult } from "./openingView.ts";

export type CeremonySnapshot = Readonly<{
  showingKey: string | null;
  doneKeys: ReadonlySet<string>;
}>;

export type OpeningCeremonyStore = Readonly<{
  getSnapshot(): CeremonySnapshot; // 値が変わるまで同じオブジェクトを返す
  subscribe(listener: () => void): () => void;
  begin(key: string): void; // 出し始めた（showingKey = key）
  finish(key: string): void; // 終わった・出さないと決めた（doneKeys に足し、showingKey が key なら null に）
  reset(): void; // テスト用。最初の状態に戻す
}>;

function initialSnapshot(): CeremonySnapshot {
  return { showingKey: null, doneKeys: new Set<string>() };
}

function createOpeningCeremonyStore(): OpeningCeremonyStore {
  const listeners = new Set<() => void>();
  let snapshot = initialSnapshot();

  function update(next: CeremonySnapshot): void {
    snapshot = next;
    for (const listener of [...listeners]) {
      listener();
    }
  }

  return Object.freeze({
    getSnapshot(): CeremonySnapshot {
      return snapshot;
    },
    subscribe(listener: () => void): () => void {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    begin(key: string): void {
      if (snapshot.showingKey === key) return;
      update({ showingKey: key, doneKeys: snapshot.doneKeys });
    },
    finish(key: string): void {
      const showingKey = snapshot.showingKey === key ? null : snapshot.showingKey;
      const known = snapshot.doneKeys.has(key);
      if (known && showingKey === snapshot.showingKey) return;
      const doneKeys = known ? snapshot.doneKeys : new Set([...snapshot.doneKeys, key]);
      update({ showingKey, doneKeys });
    },
    reset(): void {
      const current = snapshot;
      if (current.showingKey === null && current.doneKeys.size === 0) return;
      update(initialSnapshot());
    },
  });
}

export const openingCeremonyState: OpeningCeremonyStore = createOpeningCeremonyStore();

// isCeremonyActive は、演出を出している最中か、まだ出していない開会式の結果があるときに true。
export function isCeremonyActive(
  result: OpeningResult | null,
  snapshot: CeremonySnapshot,
): boolean {
  return snapshot.showingKey !== null || (result !== null && !snapshot.doneKeys.has(result.key));
}

// useOpeningCeremonyActive は、開会式の結果が入った描画から演出が終わるまで true を返す。
// 世界とこのストアだけから同期的に決まる（演出のコンポーネントの effect を待たない）。
export function useOpeningCeremonyActive(): boolean {
  const world = useGameStore((s) => s.world);
  const snapshot = useSyncExternalStore(
    openingCeremonyState.subscribe,
    openingCeremonyState.getSnapshot,
    openingCeremonyState.getSnapshot,
  );
  return isCeremonyActive(openingResult(world), snapshot);
}
