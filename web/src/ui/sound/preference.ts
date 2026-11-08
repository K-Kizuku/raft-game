// 「効果音を鳴らす」の設定（T-434）。用語の設定（ui/terms/preference.ts）と同じく、
// モジュールに 1 つの外部ストア＋useSyncExternalStore で持ち、ブラウザ（localStorage）に保存する。既定はオン。
import { useSyncExternalStore } from "react";

// localStorage のキー。値は JSON の文字列 {"v":1,"enabled":<boolean>}。
// 用語の設定のキー（raft-game.settings）とは分ける（その値の書式を変えないため）。
export const SOUND_STORAGE_KEY = "raft-game.sound";
export const SOUND_SETTINGS_VERSION = 1;

export type StorageLike = Pick<Storage, "getItem" | "setItem">;

// enabled: 鳴らすか（既定 true）。persistent: 今の値がブラウザに保存されている（再読み込みしても残る）か。
export type SoundPreference = Readonly<{ enabled: boolean; persistent: boolean }>;

export type SoundPreferenceStore = {
  getSnapshot(): SoundPreference;
  subscribe(listener: () => void): () => void;
  setEnabled(value: boolean): void;
  reload(): void;
};

type Loaded = { storage: StorageLike | null; preference: SoundPreference };

const DEFAULT_ENABLED = true;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseEnabled(raw: string): boolean {
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return DEFAULT_ENABLED;
  }
  if (!isRecord(value) || value["v"] !== SOUND_SETTINGS_VERSION) {
    return DEFAULT_ENABLED;
  }
  const enabled = value["enabled"];
  return typeof enabled === "boolean" ? enabled : DEFAULT_ENABLED;
}

function load(openStorage: () => StorageLike | null): Loaded {
  let storage: StorageLike | null;
  let raw: string | null;
  try {
    storage = openStorage();
    if (storage === null) {
      return { storage: null, preference: { enabled: DEFAULT_ENABLED, persistent: false } };
    }
    raw = storage.getItem(SOUND_STORAGE_KEY);
  } catch {
    return { storage: null, preference: { enabled: DEFAULT_ENABLED, persistent: false } };
  }
  if (raw === null) {
    return { storage, preference: { enabled: DEFAULT_ENABLED, persistent: true } };
  }
  return { storage, preference: { enabled: parseEnabled(raw), persistent: true } };
}

export function createSoundPreferenceStore(
  openStorage: () => StorageLike | null,
): SoundPreferenceStore {
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

  function getSnapshot(): SoundPreference {
    return ensureLoaded().preference;
  }

  function subscribe(listener: () => void): () => void {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }

  function setEnabled(value: boolean): void {
    const current = ensureLoaded();
    if (current.preference.enabled === value) {
      return;
    }
    let persistent = false;
    if (current.storage !== null) {
      try {
        current.storage.setItem(
          SOUND_STORAGE_KEY,
          JSON.stringify({ v: SOUND_SETTINGS_VERSION, enabled: value }),
        );
        persistent = true;
      } catch {
        persistent = false;
      }
    }
    loaded = { storage: current.storage, preference: { enabled: value, persistent } };
    notify();
  }

  function reload(): void {
    const previous = loaded;
    const next = load(openStorage);
    if (
      previous !== null &&
      previous.preference.enabled === next.preference.enabled &&
      previous.preference.persistent === next.preference.persistent
    ) {
      loaded = { storage: next.storage, preference: previous.preference };
      return;
    }
    loaded = next;
    notify();
  }

  return { getSnapshot, subscribe, setEnabled, reload };
}

function openBrowserStorage(): StorageLike | null {
  if (typeof window === "undefined") {
    return null;
  }
  return window.localStorage;
}

export const soundPreference: SoundPreferenceStore = createSoundPreferenceStore(openBrowserStorage);

export function useSoundPreference(): SoundPreference {
  return useSyncExternalStore(
    soundPreference.subscribe,
    soundPreference.getSnapshot,
    soundPreference.getSnapshot,
  );
}

export function setSoundEnabled(value: boolean): void {
  soundPreference.setEnabled(value);
}
