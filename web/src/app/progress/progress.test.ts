import { describe, expect, it, vi, type Mock } from "vitest";

import type { StorageLike } from "../../ui/terms/preference.ts";
import { createStageProgressStore, PROGRESS_STORAGE_KEY } from "./progress.ts";

type FakeStorage = {
  getItem: Mock<StorageLike["getItem"]>;
  setItem: Mock<StorageLike["setItem"]>;
  values: Map<string, string>;
};

function fakeStorage(initial?: string): FakeStorage {
  const values = new Map<string, string>();
  if (initial !== undefined) {
    values.set(PROGRESS_STORAGE_KEY, initial);
  }
  return {
    values,
    getItem: vi.fn<StorageLike["getItem"]>((key) => values.get(key) ?? null),
    setItem: vi.fn<StorageLike["setItem"]>((key, value) => {
      values.set(key, value);
    }),
  };
}

function storeWith(storage: StorageLike | null) {
  return createStageProgressStore(() => storage);
}

describe("createStageProgressStore", () => {
  it("PG-1 空の保存から初めてのクリアを書く", () => {
    const storage = fakeStorage();
    const store = storeWith(storage);

    expect(store.getSnapshot()).toEqual({ bestStars: {}, persistent: true });
    expect(store.recordResult("stage1", 2)).toEqual({ firstClear: true });
    expect(storage.setItem).toHaveBeenCalledTimes(1);
    expect(storage.setItem).toHaveBeenCalledWith(
      "raft-game.progress",
      '{"v":1,"bestStars":{"stage1":2}}',
    );
    expect(store.getSnapshot()).toEqual({ bestStars: { stage1: 2 }, persistent: true });
  });

  it("PG-1 読み込みは最初の getSnapshot で 1 回だけ", () => {
    const storage = fakeStorage('{"v":1,"bestStars":{"stage1":1}}');
    const store = storeWith(storage);
    expect(storage.getItem).not.toHaveBeenCalled();

    store.getSnapshot();
    store.getSnapshot();
    expect(storage.getItem).toHaveBeenCalledTimes(1);
  });

  it("PG-1 bestStars のキーは書いた順", () => {
    const storage = fakeStorage('{"v":1,"bestStars":{"stage2":1}}');
    const store = storeWith(storage);

    store.recordResult("stage1", 3);
    expect(storage.values.get(PROGRESS_STORAGE_KEY)).toBe(
      '{"v":1,"bestStars":{"stage2":1,"stage1":3}}',
    );
  });

  it("PG-2 最高の★より低ければ変えず、高ければ更新する", () => {
    const storage = fakeStorage('{"v":1,"bestStars":{"stage1":2}}');
    const store = storeWith(storage);
    const listener = vi.fn();
    store.subscribe(listener);

    expect(store.recordResult("stage1", 1)).toEqual({ firstClear: false });
    expect(store.recordResult("stage1", 2)).toEqual({ firstClear: false });
    expect(storage.setItem).not.toHaveBeenCalled();
    expect(listener).not.toHaveBeenCalled();

    expect(store.recordResult("stage1", 3)).toEqual({ firstClear: false });
    expect(storage.values.get(PROGRESS_STORAGE_KEY)).toBe('{"v":1,"bestStars":{"stage1":3}}');
    expect(listener).toHaveBeenCalledTimes(1);
    expect(store.getSnapshot().bestStars).toEqual({ stage1: 3 });
  });

  it("PG-2 ★は 3 で頭打ち", () => {
    const store = storeWith(fakeStorage());

    store.recordResult("stage1", 5);
    expect(store.getSnapshot().bestStars).toEqual({ stage1: 3 });
  });

  it.each([0, 1.5, -1, Number.NaN])("PG-3 ★%s は何もしない", (stars) => {
    const storage = fakeStorage();
    const store = storeWith(storage);
    const listener = vi.fn();
    store.subscribe(listener);

    expect(store.recordResult("stage1", stars)).toEqual({ firstClear: false });
    expect(storage.setItem).not.toHaveBeenCalled();
    expect(listener).not.toHaveBeenCalled();
    expect(store.getSnapshot().bestStars).toEqual({});
  });

  it.each([
    ["壊れた JSON", "{v:1"],
    ["v が 2", '{"v":2,"bestStars":{"stage1":2}}'],
    ["bestStars が配列", '{"v":1,"bestStars":[2]}'],
    ["bestStars が無い", '{"v":1}'],
    ["null", "null"],
  ])("PG-4 %s は空として読み、書き戻さない", (_label, raw) => {
    const storage = fakeStorage(raw);
    const store = storeWith(storage);

    expect(store.getSnapshot()).toEqual({ bestStars: {}, persistent: true });
    expect(storage.setItem).not.toHaveBeenCalled();
    expect(storage.values.get(PROGRESS_STORAGE_KEY)).toBe(raw);
  });

  it("PG-4 別の版は次のクリアで上書きする", () => {
    const storage = fakeStorage('{"v":2,"bestStars":{"stage1":2}}');
    const store = storeWith(storage);

    expect(store.recordResult("stage1", 1)).toEqual({ firstClear: true });
    expect(storage.values.get(PROGRESS_STORAGE_KEY)).toBe('{"v":1,"bestStars":{"stage1":1}}');
  });

  it("PG-5 一部が壊れていても、形の合う要素は残す", () => {
    const storage = fakeStorage(
      '{"v":1,"bestStars":{"stage1":2,"Bad":1,"stage2":"3","stage3":4,"stage4":0,"stage5":1.5}}',
    );
    const store = storeWith(storage);

    expect(store.getSnapshot().bestStars).toEqual({ stage1: 2 });
  });

  it("PG-5 要素は 64 個まで読む", () => {
    const bestStars: Record<string, number> = {};
    for (let i = 0; i < 70; i++) {
      bestStars[`s${i}`] = 1;
    }
    const store = storeWith(fakeStorage(JSON.stringify({ v: 1, bestStars })));

    const keys = Object.keys(store.getSnapshot().bestStars);
    expect(keys).toHaveLength(64);
    expect(keys.at(-1)).toBe("s63");
  });

  it("PG-6 getItem が投げても例外を出さず、画面には効く", () => {
    const store = storeWith({
      getItem: () => {
        throw new DOMException("denied", "SecurityError");
      },
      setItem: vi.fn(),
    });

    expect(store.getSnapshot()).toEqual({ bestStars: {}, persistent: false });
    expect(store.recordResult("stage1", 1)).toEqual({ firstClear: true });
    expect(store.getSnapshot()).toEqual({ bestStars: { stage1: 1 }, persistent: false });
  });

  it("PG-6 openStorage が投げる・null でも例外を出さない", () => {
    const throwing = createStageProgressStore(() => {
      throw new DOMException("denied", "SecurityError");
    });
    expect(throwing.getSnapshot().persistent).toBe(false);
    expect(throwing.recordResult("stage1", 2)).toEqual({ firstClear: true });
    expect(throwing.getSnapshot().bestStars).toEqual({ stage1: 2 });

    const none = storeWith(null);
    expect(none.recordResult("stage1", 2)).toEqual({ firstClear: true });
    expect(none.getSnapshot()).toEqual({ bestStars: { stage1: 2 }, persistent: false });
  });

  it("PG-6 setItem が投げても例外を出さず、persistent: false で画面には効く", () => {
    const storage = fakeStorage();
    storage.setItem.mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError");
    });
    const store = storeWith(storage);
    const listener = vi.fn();
    store.subscribe(listener);

    expect(store.getSnapshot().persistent).toBe(true);
    expect(store.recordResult("stage1", 2)).toEqual({ firstClear: true });
    expect(store.getSnapshot()).toEqual({ bestStars: { stage1: 2 }, persistent: false });
    expect(listener).toHaveBeenCalledTimes(1);

    expect(() => {
      store.clearAll();
    }).not.toThrow();
    expect(store.getSnapshot()).toEqual({ bestStars: {}, persistent: false });
  });

  it("PG-7 clearAll は空の値を書き、購読者を 1 回呼ぶ。空のときは呼ばない", () => {
    const storage = fakeStorage('{"v":1,"bestStars":{"stage1":2}}');
    const store = storeWith(storage);
    const listener = vi.fn();
    const unsubscribe = store.subscribe(listener);

    store.clearAll();
    expect(storage.values.get(PROGRESS_STORAGE_KEY)).toBe('{"v":1,"bestStars":{}}');
    expect(store.getSnapshot()).toEqual({ bestStars: {}, persistent: true });
    expect(listener).toHaveBeenCalledTimes(1);

    store.clearAll();
    expect(listener).toHaveBeenCalledTimes(1);

    unsubscribe();
    store.recordResult("stage1", 1);
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("PG-8 値が変わらない間は getSnapshot が同じオブジェクト", () => {
    const storage = fakeStorage('{"v":1,"bestStars":{"stage1":2}}');
    const store = storeWith(storage);

    const first = store.getSnapshot();
    expect(store.getSnapshot()).toBe(first);
    store.recordResult("stage1", 1);
    expect(store.getSnapshot()).toBe(first);
    store.reload();
    expect(store.getSnapshot()).toBe(first);

    store.recordResult("stage2", 1);
    const second = store.getSnapshot();
    expect(second).not.toBe(first);

    store.clearAll();
    const cleared = store.getSnapshot();
    store.clearAll();
    expect(store.getSnapshot()).toBe(cleared);
  });

  it("reload は保存の変化を読み直し、変わったときだけ知らせる", () => {
    const storage = fakeStorage();
    const store = storeWith(storage);
    const listener = vi.fn();
    store.subscribe(listener);
    store.getSnapshot();

    store.reload();
    expect(listener).not.toHaveBeenCalled();

    storage.values.set(PROGRESS_STORAGE_KEY, '{"v":1,"bestStars":{"stage1":1}}');
    store.reload();
    expect(listener).toHaveBeenCalledTimes(1);
    expect(store.getSnapshot().bestStars).toEqual({ stage1: 1 });
  });
});
