// Stage 1 のチュートリアルの台本（T-409・docs/05-game-progression.md §7・FR-UI-08）。
// 文言はクライアントの画面の文言で、ステージ定義（data/stages）に置かない。
// ここの数（t8AfterMs など）はチュートリアルの見せ方の値で、ゲームの結果に影響しない。
// チュートリアルの数はこのファイルにまとめ、ほかのファイルに書かない。
import type { RaftTermId } from "../../ui/terms/glossary.ts";

export type TutorialStepId = "T1" | "T2" | "T3" | "T4" | "T5" | "T6" | "T7" | "T8" | "T9";

// 文言の一部。用語を添える言葉は { term, text }（1 つの吹き出しの中で最初の 1 か所だけ。DesignDoc §13.7）。
export type TextPart = string | Readonly<{ term: RaftTermId; text: string }>;

// 強調する UI。selector は DOM のクラス名、island はマップの島のボタン。
export type TargetSpec =
  | Readonly<{ kind: "selector"; selector: string; fallback?: string }>
  | Readonly<{ kind: "island"; island: "leader" | "relay" | "far" }>;

export type TutorialStep = Readonly<{
  id: TutorialStepId;
  text: readonly TextPart[];
  advance: "click" | "action"; // click は「次へ」で進む。action は条件で進み、「わかった」で畳める
  optional: boolean; // T8・T9
  targets: readonly TargetSpec[];
}>;

export type TutorialScript = Readonly<{
  islands: Readonly<{ leader: string; relay: string; far: string }>; // 島の名前（world.stage から ID を引く）
  ferryName: string;
  t8AfterMs: number; // T8 を出してよいゲーム内時刻
  autoPause: boolean;
  steps: readonly TutorialStep[]; // T1〜T9 の順
}>;

function selector(sel: string, fallback?: string): TargetSpec {
  return fallback === undefined
    ? { kind: "selector", selector: sel }
    : { kind: "selector", selector: sel, fallback };
}

function island(which: "leader" | "relay" | "far"): TargetSpec {
  return { kind: "island", island: which };
}

export const STAGE1_TUTORIAL: TutorialScript = {
  islands: { leader: "小豆島", relay: "豊島", far: "直島" },
  ferryName: "せとうち丸",
  t8AfterMs: 120000,
  autoPause: true,
  steps: [
    {
      id: "T1",
      text: [
        "あなたは離島Raft共和国の郵便船の船長です。島どうしの手紙を運んで、しまじまフェスを成功させましょう",
      ],
      advance: "click",
      optional: false,
      targets: [],
    },
    {
      id: "T2",
      text: [
        "小豆島の港に",
        { term: "letter", text: "手紙" },
        "が出ました。船『せとうち丸』に積んでみましょう（ドラッグ）",
      ],
      advance: "action",
      optional: false,
      targets: [selector(".ferry-panel__port"), selector(".ferry-panel__cargo")],
    },
    {
      id: "T3",
      text: [
        "行き先に豊島を選んで、出航！ 行き先は、下の「行き先」のボタンか、マップで隣の島を押して選べます。隣でない島あての手紙は、隣の島を経由して運びます（おまかせなら、経由の手紙も一緒に積みます）",
      ],
      advance: "action",
      optional: false,
      targets: [selector(".ferry-panel__destinations"), selector(".ferry-panel__depart")],
    },
    {
      id: "T4",
      text: ["豊島が返事を書きました。小豆島へ持ち帰りましょう"],
      advance: "action",
      optional: false,
      targets: [island("relay")],
    },
    {
      id: "T5",
      text: [
        { term: "apply", text: "確定" },
        "！ 3 つの島のうち 2 つが",
        { term: "entry", text: "記録" },
        "すれば、正式に決まります",
      ],
      advance: "click",
      optional: false,
      targets: [selector(".hud-score")],
    },
    {
      id: "T6",
      text: [
        "直島から依頼書（★）が出ています。依頼書は",
        { term: "leader", text: "代表" },
        "（👑）の島へ届けましょう。直島へは豊島を経由して行けます",
      ],
      advance: "action",
      optional: false,
      targets: [island("far"), island("leader"), selector(".game-map")],
    },
    {
      id: "T7",
      text: [
        "共和国では",
        { term: "commit", text: "決まった" },
        "けど、直島はまだ知りません。結果を届けるまでが仕事です",
      ],
      advance: "action",
      optional: false,
      targets: [selector(".island-requests", ".game-island")],
    },
    {
      id: "T8",
      text: ["近い豊島を経由すると、直島への手紙も一緒に運べるよ"],
      advance: "click",
      optional: true,
      targets: [selector(".game-map")],
    },
    {
      id: "T9",
      text: [
        "直島が",
        { term: "leader", text: "代表" },
        "と連絡がつかなくて不安になっています。",
        { term: "heartbeat", text: "定期便" },
        "も届けてあげましょう。ほかの島は代表と連絡が取れているので、相談を断ります",
      ],
      advance: "click",
      optional: true,
      targets: [island("far")],
    },
  ],
};

// 台本のキーは、ステージ定義の tutorial（stage1）と同じ値。
export const TUTORIAL_SCRIPTS: Readonly<Record<string, TutorialScript>> = {
  stage1: STAGE1_TUTORIAL,
};

// tutorialScriptFor は stageId の台本を返す（Object のプロトタイプのキーは拾わない）。無ければ null。
export function tutorialScriptFor(stageId: string): TutorialScript | null {
  return Object.hasOwn(TUTORIAL_SCRIPTS, stageId) ? (TUTORIAL_SCRIPTS[stageId] ?? null) : null;
}

// stepText は文言の文字をつないだもの（用語の部分は text）。畳んだ吹き出しの title などに使う。
export function stepText(step: TutorialStep): string {
  return step.text.map((part) => (typeof part === "string" ? part : part.text)).join("");
}
