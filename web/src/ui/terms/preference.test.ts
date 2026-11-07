import { describe, expect, it, vi } from "vitest";

import {
  createTermsPreferenceStore,
  openBrowserStorage,
  SETTINGS_STORAGE_KEY,
  type StorageLike,
} from "./preference.ts";

type FakeStorage = StorageLike & {
  getItem: ReturnType<typeof vi.fn<(key: string) => string | null>>;
  setItem: ReturnType<typeof vi.fn<(key: string, value: string) => void>>;
  values: Map<string, string>;
};

function fakeStorage(initial?: string): FakeStorage {
  const values = new Map<string, string>();
  if (initial !== undefined) {
    values.set(SETTINGS_STORAGE_KEY, initial);
  }
  return {
    values,
    getItem: vi.fn((key: string) => values.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => {
      values.set(key, value);
    }),
  };
}

function fail(): never {
  throw new DOMException("denied", "SecurityError");
}

describe("createTermsPreferenceStore", () => {
  it("PF-1 遅延と既定", () => {
    const storage = fakeStorage();
    const openStorage = vi.fn(() => storage);
    const store = createTermsPreferenceStore(openStorage);
    expect(openStorage).not.toHaveBeenCalled();

    const first = store.getSnapshot();
    const second = store.getSnapshot();
    expect(first).toEqual({ showRaftTerms: false, persistent: true });
    expect(second).toBe(first);
    expect(openStorage).toHaveBeenCalledTimes(1);
    expect(storage.getItem).toHaveBeenCalledTimes(1);
    expect(storage.getItem).toHaveBeenCalledWith("raft-game.settings");
  });

  it.each<[string, boolean]>([
    ['{"v":1,"showRaftTerms":true}', true],
    ['{"v":1,"showRaftTerms":false}', false],
    ['{"v":1,"showRaftTerms":true,"extra":1}', true],
    ['"true"', false],
    ["1", false],
    ["null", false],
    ["[]", false],
    ["{}", false],
    ['{"v":2,"showRaftTerms":true}', false],
    ['{"v":1,"showRaftTerms":"true"}', false],
    ["{not json", false],
  ])("PF-2 %s → %s", (raw, show) => {
    const storage = fakeStorage(raw);
    const store = createTermsPreferenceStore(() => storage);
    expect(store.getSnapshot()).toEqual({ showRaftTerms: show, persistent: true });
    expect(storage.setItem).not.toHaveBeenCalled();
  });

  it("PF-3 書き込み", () => {
    const storage = fakeStorage();
    const store = createTermsPreferenceStore(() => storage);
    const listener = vi.fn();
    store.subscribe(listener);
    const before = store.getSnapshot();

    store.setShowRaftTerms(true);
    expect(storage.setItem).toHaveBeenCalledTimes(1);
    expect(storage.setItem).toHaveBeenCalledWith(
      "raft-game.settings",
      '{"v":1,"showRaftTerms":true}',
    );
    expect(listener).toHaveBeenCalledTimes(1);
    const after = store.getSnapshot();
    expect(after).not.toBe(before);
    expect(after).toEqual({ showRaftTerms: true, persistent: true });

    store.setShowRaftTerms(true);
    expect(storage.setItem).toHaveBeenCalledTimes(1);
    expect(listener).toHaveBeenCalledTimes(1);
    expect(store.getSnapshot()).toBe(after);

    store.setShowRaftTerms(false);
    expect(storage.setItem).toHaveBeenLastCalledWith(
      "raft-game.settings",
      '{"v":1,"showRaftTerms":false}',
    );
    expect(listener).toHaveBeenCalledTimes(2);
    expect(store.getSnapshot()).toEqual({ showRaftTerms: false, persistent: true });
  });

  it.each<[string, (storage: FakeStorage) => StorageLike | null]>([
    ["openStorage が null を返す", () => null],
    ["openStorage が例外を投げる", () => fail()],
    [
      "getItem が例外を投げる",
      (storage) => {
        storage.getItem.mockImplementation(() => fail());
        return storage;
      },
    ],
  ])("PF-4 %s", (_name, open) => {
    const storage = fakeStorage();
    const store = createTermsPreferenceStore(() => open(storage));
    expect(store.getSnapshot()).toEqual({ showRaftTerms: false, persistent: false });

    const listener = vi.fn();
    store.subscribe(listener);
    expect(() => {
      store.setShowRaftTerms(true);
    }).not.toThrow();
    expect(store.getSnapshot()).toEqual({ showRaftTerms: true, persistent: false });
    expect(listener).toHaveBeenCalledTimes(1);
    expect(storage.setItem).not.toHaveBeenCalled();
  });

  it("PF-4 setItem だけが例外を投げる", () => {
    const storage = fakeStorage();
    storage.setItem.mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError");
    });
    const store = createTermsPreferenceStore(() => storage);
    const listener = vi.fn();
    store.subscribe(listener);

    expect(() => {
      store.setShowRaftTerms(true);
    }).not.toThrow();
    expect(store.getSnapshot()).toEqual({ showRaftTerms: true, persistent: false });
    expect(listener).toHaveBeenCalledTimes(1);

    storage.setItem.mockImplementation(() => undefined);
    store.setShowRaftTerms(false);
    expect(store.getSnapshot()).toEqual({ showRaftTerms: false, persistent: true });
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it("PF-5 解除と reload", () => {
    const storage = fakeStorage();
    const openStorage = vi.fn(() => storage);
    const store = createTermsPreferenceStore(openStorage);
    const removed = vi.fn();
    const listener = vi.fn();
    const unsubscribe = store.subscribe(removed);
    store.subscribe(listener);
    unsubscribe();
    store.getSnapshot();

    storage.values.set(SETTINGS_STORAGE_KEY, '{"v":1,"showRaftTerms":true}');
    store.reload();
    expect(listener).toHaveBeenCalledTimes(1);
    expect(removed).not.toHaveBeenCalled();
    expect(openStorage).toHaveBeenCalledTimes(2);
    const snapshot = store.getSnapshot();
    expect(snapshot.showRaftTerms).toBe(true);

    store.reload();
    expect(listener).toHaveBeenCalledTimes(1);
    expect(store.getSnapshot()).toBe(snapshot);
  });

  it("PF-6 openBrowserStorage は window が無ければ null", () => {
    expect(typeof window).toBe("undefined");
    expect(openBrowserStorage()).toBeNull();
  });
});
