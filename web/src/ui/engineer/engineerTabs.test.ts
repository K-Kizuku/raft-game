import { describe, expect, it } from "vitest";

import { ENGINEER_TABS, tabAfterKey } from "./engineerTabs.ts";

describe("engineerTabs", () => {
  it("ET-1 タブは「ノード一覧」「Raft ログ」「Progress」「手紙」の順", () => {
    expect(ENGINEER_TABS.map((tab) => tab.label)).toEqual([
      "ノード一覧",
      "Raft ログ",
      "Progress",
      "手紙",
    ]);
  });

  it("ET-2 → で次、← で前のタブ。端では反対の端に回る", () => {
    expect(tabAfterKey("nodes", "ArrowRight")).toBe("log");
    expect(tabAfterKey("log", "ArrowRight")).toBe("progress");
    expect(tabAfterKey("letters", "ArrowRight")).toBe("nodes");
    expect(tabAfterKey("progress", "ArrowLeft")).toBe("log");
    expect(tabAfterKey("nodes", "ArrowLeft")).toBe("letters");
  });

  it("ET-3 ほかのキーでは移らない", () => {
    for (const key of ["ArrowUp", "ArrowDown", "Enter", "e", "Home", "End"]) {
      expect(tabAfterKey("log", key)).toBeNull();
    }
  });
});
