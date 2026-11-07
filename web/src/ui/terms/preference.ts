// 「Raft の用語を併記する」の設定（T-412・FR-UI-10・DesignDoc §13.7）。
// ルーターと同じく、モジュールに 1 つの外部ストア＋useSyncExternalStore で持つ。
import { useSyncExternalStore } from "react";

// localStorage のキー。値は JSON の文字列 {"v":1,"showRaftTerms":<boolean>}。
export const SETTINGS_STORAGE_KEY = "raft-game.settings";
export const SETTINGS_VERSION = 1;

export type StorageLike = Pick<Storage, "getItem" | "setItem">;

// showRaftTerms: 併記するか（既定 false）。
// persistent: 今の値がブラウザに保存されている（再読み込みしても残る）か。
export type TermsPreference = Readonly<{ showRaftTerms: boolean; persistent: boolean }>;

export type TermsPreferenceStore = {
  getSnapshot(): TermsPreference;
  subscribe(listener: () => void): () => void;
  setShowRaftTerms(value: boolean): void;
  reload(): void;
};

type Loaded = { storage: StorageLike | null; preference: TermsPreference };

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseShowRaftTerms(raw: string): boolean {
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return false;
  }
  if (!isRecord(value) || value["v"] !== SETTINGS_VERSION) {
    return false;
  }
  const show = value["showRaftTerms"];
  return typeof show === "boolean" ? show : false;
}

function load(openStorage: () => StorageLike | null): Loaded {
  let storage: StorageLike | null;
  let raw: string | null;
  try {
    storage = openStorage();
    if (storage === null) {
      return { storage: null, preference: { showRaftTerms: false, persistent: false } };
    }
    raw = storage.getItem(SETTINGS_STORAGE_KEY);
  } catch {
    return { storage: null, preference: { showRaftTerms: false, persistent: false } };
  }
  if (raw === null) {
    return { storage, preference: { showRaftTerms: false, persistent: true } };
  }
  return { storage, preference: { showRaftTerms: parseShowRaftTerms(raw), persistent: true } };
}

export function createTermsPreferenceStore(
  openStorage: () => StorageLike | null,
): TermsPreferenceStore {
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

  function getSnapshot(): TermsPreference {
    return ensureLoaded().preference;
  }

  function subscribe(listener: () => void): () => void {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }

  function setShowRaftTerms(value: boolean): void {
    const current = ensureLoaded();
    if (current.preference.showRaftTerms === value) {
      return;
    }
    let persistent = false;
    if (current.storage !== null) {
      try {
        current.storage.setItem(
          SETTINGS_STORAGE_KEY,
          JSON.stringify({ v: SETTINGS_VERSION, showRaftTerms: value }),
        );
        persistent = true;
      } catch {
        persistent = false;
      }
    }
    loaded = { storage: current.storage, preference: { showRaftTerms: value, persistent } };
    notify();
  }

  function reload(): void {
    const previous = loaded;
    const next = load(openStorage);
    if (
      previous !== null &&
      previous.preference.showRaftTerms === next.preference.showRaftTerms &&
      previous.preference.persistent === next.preference.persistent
    ) {
      loaded = { storage: next.storage, preference: previous.preference };
      return;
    }
    loaded = next;
    notify();
  }

  return { getSnapshot, subscribe, setShowRaftTerms, reload };
}

// localStorage に触れるのはここだけ。アクセスの例外（SecurityError など）は捕まえず、ストアの側で捕まえる。
export function openBrowserStorage(): StorageLike | null {
  if (typeof window === "undefined") {
    return null;
  }
  return window.localStorage;
}

export const termsPreference: TermsPreferenceStore = createTermsPreferenceStore(openBrowserStorage);

export function useTermsPreference(): TermsPreference {
  return useSyncExternalStore(
    termsPreference.subscribe,
    termsPreference.getSnapshot,
    termsPreference.getSnapshot,
  );
}

export function useShowRaftTerms(): boolean {
  return useTermsPreference().showRaftTerms;
}

export function setShowRaftTerms(value: boolean): void {
  termsPreference.setShowRaftTerms(value);
}
