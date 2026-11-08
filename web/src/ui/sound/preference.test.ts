import { describe, expect, it, vi } from "vitest";

import { SOUND_STORAGE_KEY, createSoundPreferenceStore, type StorageLike } from "./preference.ts";

function memoryStorage(initial: Record<string, string> = {}): StorageLike & {
  data: Record<string, string>;
} {
  const data = { ...initial };
  return {
    data,
    getItem: (key) => data[key] ?? null,
    setItem: (key, value) => {
      data[key] = value;
    },
  };
}

describe("createSoundPreferenceStore", () => {
  it("SP-1 既定はオンで、保存できる", () => {
    const store = createSoundPreferenceStore(() => memoryStorage());
    expect(store.getSnapshot()).toEqual({ enabled: true, persistent: true });
  });

  it("SP-2 切り替えるとブラウザに保存し、知らせる", () => {
    const storage = memoryStorage();
    const store = createSoundPreferenceStore(() => storage);
    const listener = vi.fn();
    store.subscribe(listener);

    store.setEnabled(false);
    expect(store.getSnapshot()).toEqual({ enabled: false, persistent: true });
    expect(storage.data[SOUND_STORAGE_KEY]).toBe('{"v":1,"enabled":false}');
    expect(listener).toHaveBeenCalledTimes(1);

    store.setEnabled(false);
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("SP-3 保存した値で始まる・壊れた値は既定に戻す", () => {
    expect(
      createSoundPreferenceStore(() =>
        memoryStorage({ [SOUND_STORAGE_KEY]: '{"v":1,"enabled":false}' }),
      ).getSnapshot().enabled,
    ).toBe(false);
    for (const raw of ["{", '{"v":2,"enabled":false}', '{"v":1,"enabled":"no"}', "[]"]) {
      expect(
        createSoundPreferenceStore(() => memoryStorage({ [SOUND_STORAGE_KEY]: raw })).getSnapshot()
          .enabled,
        raw,
      ).toBe(true);
    }
  });

  it("SP-4 保存できないときは persistent が false", () => {
    const store = createSoundPreferenceStore(() => ({
      getItem: () => null,
      setItem: () => {
        throw new DOMException("full", "QuotaExceededError");
      },
    }));
    store.setEnabled(false);
    expect(store.getSnapshot()).toEqual({ enabled: false, persistent: false });

    const blocked = createSoundPreferenceStore(() => {
      throw new DOMException("denied", "SecurityError");
    });
    expect(blocked.getSnapshot()).toEqual({ enabled: true, persistent: false });
    expect(createSoundPreferenceStore(() => null).getSnapshot()).toEqual({
      enabled: true,
      persistent: false,
    });
  });

  it("SP-5 reload で保存領域を読み直す", () => {
    const storage = memoryStorage();
    const store = createSoundPreferenceStore(() => storage);
    const listener = vi.fn();
    store.subscribe(listener);
    expect(store.getSnapshot().enabled).toBe(true);

    store.reload();
    expect(listener).not.toHaveBeenCalled();

    storage.data[SOUND_STORAGE_KEY] = '{"v":1,"enabled":false}';
    store.reload();
    expect(store.getSnapshot().enabled).toBe(false);
    expect(listener).toHaveBeenCalledTimes(1);
  });
});
