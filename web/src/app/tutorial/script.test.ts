import { describe, expect, it } from "vitest";

import { TUTORIAL_SCRIPTS, stepText, tutorialScriptFor } from "./script.ts";
import type { TutorialScript, TutorialStepId } from "./script.ts";

function stage1(): TutorialScript {
  const script = TUTORIAL_SCRIPTS.stage1;
  if (script === undefined) throw new Error("stage1 の台本が無い");
  return script;
}

// docs/05-game-progression.md §7 の「文言」の列（鉤括弧を外したもの）。
const EXPECTED_TEXT: Readonly<Record<TutorialStepId, string>> = {
  T1: "あなたは離島Raft共和国の郵便船の船長です。島どうしの手紙を運んで、しまじまフェスを成功させましょう",
  T2: "小豆島の港に手紙が出ました。船『せとうち丸』に積んでみましょう（ドラッグ）",
  T3: "行き先に豊島を選んで、出航！ 行き先は、下の「行き先」のボタンか、マップで隣の島を押して選べます。隣でない島あての手紙は、隣の島を経由して運びます（おまかせなら、経由の手紙も一緒に積みます）",
  T4a: "確定させるには、代表の小豆島が出す『この内容を記録して！』をみんなに届けて、『記録したよ！』の返事を代表まで運び返す必要があります",
  T4: "豊島が返事を書きました。小豆島へ持ち帰りましょう。通り道の島（豊島）にも返事の手紙がたまります。寄った港では、おまかせで積んでから出航しましょう",
  T5: "確定！ 3 つの島のうち 2 つが記録すれば、正式に決まります",
  T6: "直島から依頼書（★）が出ています。依頼書は代表（👑）の島へ届けましょう。直島へは豊島を経由して行けます",
  T7: "共和国では決まったけど、直島はまだ知りません。結果を届けるまでが仕事です",
  T8: "近い豊島を経由すると、直島への手紙も一緒に運べるよ",
  T9: "直島が代表と連絡がつかなくて不安になっています。定期便も届けてあげましょう。ほかの島は代表と連絡が取れているので、相談を断ります",
};

describe("TUTORIAL_SCRIPTS", () => {
  it("SC-1 T1〜T3・T4a・T4〜T9 の順で、進み方と任意の段が表のとおり", () => {
    const script = stage1();
    expect(script.steps.map((s) => s.id)).toEqual([
      "T1",
      "T2",
      "T3",
      "T4a",
      "T4",
      "T5",
      "T6",
      "T7",
      "T8",
      "T9",
    ]);
    const click = new Set(["T1", "T4a", "T5", "T8", "T9"]);
    for (const step of script.steps) {
      expect(step.advance, step.id).toBe(click.has(step.id) ? "click" : "action");
      expect(step.optional, step.id).toBe(step.id === "T8" || step.id === "T9");
    }
    expect(script.islands).toEqual({ leader: "小豆島", relay: "豊島", far: "直島" });
    expect(script.ferryName).toBe("せとうち丸");
    expect(script.t8AfterMs).toBe(120000);
    expect(script.autoPause).toBe(true);
  });

  it.each(Object.entries(EXPECTED_TEXT))("SC-2 %s の文言が進行表 §7 と一致する", (id, text) => {
    const step = stage1().steps.find((s) => s.id === id);
    expect(step).toBeDefined();
    if (step === undefined) return;
    expect(stepText(step)).toBe(text);
  });

  it("SC-3 1 つの吹き出しの中で同じ用語が 2 回出ない", () => {
    for (const step of stage1().steps) {
      const terms = step.text.flatMap((part) =>
        typeof part === "string" ? [] : ["term" in part ? part.term : `letter:${part.letter}`],
      );
      expect(new Set(terms).size, step.id).toBe(terms.length);
    }
  });

  it("用語を添える言葉が表のとおり", () => {
    const terms = Object.fromEntries(
      stage1().steps.map((step) => [
        step.id,
        step.text.flatMap((part) =>
          typeof part === "string"
            ? []
            : [`${part.text}:${"term" in part ? part.term : `letter.${part.letter}`}`],
        ),
      ]),
    );
    expect(terms).toEqual({
      T1: [],
      T2: ["手紙:letter"],
      T3: [],
      T4a: ["代表:leader", "この内容を記録して！:letter.append", "記録したよ！:letter.append_resp"],
      T4: [],
      T5: ["確定:apply", "記録:entry"],
      T6: ["代表:leader"],
      T7: ["決まった:commit"],
      T8: [],
      T9: ["代表:leader", "定期便:heartbeat"],
    });
  });

  it("tutorialScriptFor は stage1 だけを返す", () => {
    expect(tutorialScriptFor("stage1")).toBe(stage1());
    expect(tutorialScriptFor("stage2")).toBeNull();
    expect(tutorialScriptFor("toString")).toBeNull();
  });
});
