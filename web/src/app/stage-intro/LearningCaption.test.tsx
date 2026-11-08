// @vitest-environment jsdom
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { LEARNING_ART_KEYS } from "../../ui/art/learning/LearningArt.tsx";
import { setShowRaftTerms, termsPreference } from "../../ui/terms/preference.ts";
import { LEARNING_LABELS } from "./introView.ts";
import { LEARNING_CAPTION_KEYS, LearningCaption } from "./LearningCaption.tsx";

beforeEach(() => {
  localStorage.clear();
  act(() => {
    termsPreference.reload();
  });
});

afterEach(() => {
  cleanup();
  localStorage.clear();
  act(() => {
    termsPreference.reload();
  });
});

describe("学べることの図と説明（T-433）", () => {
  it("LC-1 学べることのキーごとに、図と説明がある（過不足なし）", () => {
    const keys = Object.keys(LEARNING_LABELS);
    expect([...LEARNING_ART_KEYS]).toEqual(keys);
    expect([...LEARNING_CAPTION_KEYS].sort()).toEqual([...keys].sort());
  });

  it("LC-2 知らないキー・Object.prototype のキーでは何も出さない", () => {
    for (const key of ["unknown_key", "toString", "hasOwnProperty"]) {
      const { container } = render(<LearningCaption learningKey={key} />);
      expect(container.innerHTML).toBe("");
      cleanup();
    }
  });

  it.each<[string, string, string]>([
    [
      "log_replication",
      "代表が記録を配り、ほかの島も台帳に写す",
      "代表（Leader）が記録（Log Entry）を配り、ほかの島も台帳（Log）に写す",
    ],
    [
      "commit",
      "過半数から「記録したよ！」が届くと、記録が確定する",
      "過半数（Quorum）から「記録したよ！」が届くと、記録（Log Entry）が確定（Commit）する",
    ],
    [
      "network_partition",
      "嵐で島が分かれ、島の多い側で新しい代表が選ばれる",
      "嵐（Network Partition）で島が分かれ、島の多い側で新しい代表（Leader）が選ばれる",
    ],
    [
      "prevote",
      "勝てそうか周りに相談してから立候補する",
      "勝てそうか周りに相談（PreVote）してから立候補する",
    ],
  ])("LC-3 %s: オフはゲームの言葉だけ、オンは Raft の用語を併記する", (key, off, on) => {
    const { container } = render(<LearningCaption learningKey={key} className="c" />);
    const span = container.querySelector("span.c");
    expect(span?.getAttribute("data-learning-caption")).toBe(key);
    expect(span?.textContent).toBe(off);
    expect(span?.querySelector("small.raft-term-note")).toBeNull();

    act(() => {
      setShowRaftTerms(true);
    });
    expect(span?.textContent).toBe(on);
    expect(span?.querySelector("small.raft-term-note")).not.toBeNull();
  });
});
