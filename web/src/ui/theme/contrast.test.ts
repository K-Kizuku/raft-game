// 使っている色の組（文字の色と地の色・文字でない部品と隣の色）のコントラスト比（T-425・NFR-10・WCAG 1.4.3・1.4.11）。
// 一覧の組は、各部品の CSS で実際に使っている組と一致させる（出どころは「どこで」に書く）。
// 半透明の地は、下の地（マップの海・紙の地）に重ねた後の色で比べる。color-mix(in srgb, …) は mixHex で同じ色を作る。
// 基準: 文字 4.5:1 以上・大きな文字（24px 以上か太字 18.66px 以上）3:1 以上・文字でない部品 3:1 以上。
// プロジェクターで読む HUD の数字と本文は 7:1 以上（docs/11-visual-design.md §10・§12）。
import { describe, expect, it } from "vitest";

import { contrastRatio, mixHex } from "./contrast.ts";
import { tokens, type ColorTokenName } from "./tokens.ts";

const c = (name: ColorTokenName): string => tokens[name];
const WHITE = "#ffffff";

const paper = c("--color-paper");
const paperDeep = c("--color-paper-deep");
const ink = c("--color-ink");
const inkMuted = c("--color-ink-muted");
const seaLight = c("--color-sea-light");
const seaMid = c("--color-sea-mid");
const seaDeep = c("--color-sea-deep");
const postal = c("--color-postal");
const gold = c("--color-gold");
const storm = c("--color-storm");
const stormDeep = c("--color-storm-deep");
const success = c("--color-success");
const dangerText = c("--color-danger-text");

// 夜の配色の地（エンジニアパネル・デモ操作のキー: --color-storm-deep。不透明）。
const nightOverSea = stormDeep;
const nightOverPaper = stormDeep;
// 夜の地の中の、さらに薄く敷いた地。
const leaderRow = mixHex(gold, nightOverSea, 0.12); // Leader の行（金 12%）
const candidateTag = mixHex(postal, nightOverSea, 0.35); // 立候補中の札（郵便の赤 35%）
const letterDetail = mixHex(paper, nightOverSea, 0.06); // 手紙の詳細（紙 6%）
// お知らせの吹き出し（紙 96%）。いちばん暗い海（--color-sea-deep）の上でも読めるか。
const noticePaper = mixHex(paper, seaDeep, 0.96);

type Kind = "text" | "large" | "ui" | "projector";
const MIN: Record<Kind, number> = { text: 4.5, large: 3, ui: 3, projector: 7 };

// [名前, 種類, 前の色, 地の色, どこで]
const PAIRS: readonly (readonly [string, Kind, string, string, string])[] = [
  // 紙の地とパネル
  ["本文と紙の地", "projector", ink, paper, "base.css body"],
  ["本文とパネル", "projector", ink, paperDeep, "base.css .paper-panel・GameScreen.css の各領域"],
  ["補足と紙の地", "text", inkMuted, paper, "base.css .text-muted・ProvenanceLabel.css"],
  [
    "補足とパネル",
    "text",
    inkMuted,
    paperDeep,
    "FerryPanel.css .ferry-panel__note・IslandList.css",
  ],
  ["リンクと紙の地", "text", seaDeep, paper, "base.css a"],
  ["押せないボタン", "text", inkMuted, paperDeep, "base.css .btn:disabled・FerryPanel.css"],
  ["主ボタン（白と郵便の赤）", "text", WHITE, postal, "base.css .btn-primary・FerryPanel.css 出航"],
  ["主ボタンのホバー", "text", WHITE, mixHex(postal, ink, 0.88), "base.css .btn-primary:hover"],
  [
    "開会式のリボン（大きな太字）",
    "large",
    paper,
    postal,
    "OpeningCeremony.css .opening-ceremony__title",
  ],
  // HUD（プロジェクターで離れて読む）
  ["HUD の残り時間・スコア", "projector", ink, paper, "HudBar.css .hud-time・.hud-score"],
  ["HUD の失敗の文", "text", dangerText, paperDeep, "HudBar.css .hud-status"],
  // 島の詳細・フェリー操作
  ["失敗・期限切れの文字と紙", "text", dangerText, paper, "FerryList.css .ferry-list__broken"],
  [
    "失敗・期限切れの文字とパネル",
    "text",
    dangerText,
    paperDeep,
    "IslandPanel.css・FerryPanel.css",
  ],
  ["依頼の ★", "text", mixHex(gold, ink, 0.35), paperDeep, "IslandPanel.css --island-gold-text"],
  [
    "行き先宛ての札",
    "text",
    mixHex(seaDeep, ink, 0.8),
    paperDeep,
    "FerryPanel.css --ferry-target-text",
  ],
  [
    "嵐で通れない行き先",
    "text",
    stormDeep,
    mixHex(storm, paper, 0.15),
    "FerryPanel.css .ferry-panel__storm",
  ],
  [
    "不明な手紙",
    "text",
    ink,
    mixHex(storm, paper, 0.25),
    "FerryPanel.css .ferry-panel__slot--unknown",
  ],
  ["選んだ船のタブ", "text", ink, seaLight, "FerryPanel.css .ferry-panel__tab[aria-pressed]"],
  // マップの上
  ["お知らせの文", "projector", ink, noticePaper, "Notifications.css .notice-body"],
  ["お知らせの話し手", "text", inkMuted, noticePaper, "Notifications.css .notice-speaker"],
  ["選んだ島のボタン", "text", paper, ink, "MapView.css .map-view__islands button[aria-pressed]"],
  // リザルト
  [
    "達成した条件",
    "text",
    mixHex(success, ink, 0.5),
    paperDeep,
    "ResultScreen.css --result-success-ink",
  ],
  [
    "今日の出来事の時刻",
    "text",
    mixHex(postal, ink, 0.75),
    paper,
    "ResultScreen.css --result-postal-ink",
  ],
  // 嵐の紹介
  ["嵐の紹介の見出し", "text", stormDeep, paper, "DemoStormFlavor.css .demo-storm-flavor-text"],
  // 夜の配色（エンジニアパネル）
  ["エンジニアパネルの文字", "projector", paper, nightOverSea, "EngineerPanel.css .engineer-panel"],
  ["エンジニアパネルの補足", "text", seaLight, nightOverSea, "EngineerPanel.css th・h4・dt"],
  ["Leader の行", "projector", paper, leaderRow, "EngineerPanel.css tr[data-role=leader]"],
  ["Leader の札", "text", stormDeep, gold, "EngineerPanel.css .engineer-role--leader"],
  ["立候補中の札", "text", paper, candidateTag, "EngineerPanel.css .engineer-role--candidate"],
  ["古い情報の札", "text", gold, nightOverSea, "EngineerPanel.css .engineer-stale"],
  [
    "古い情報の注意",
    "text",
    paper,
    mixHex(gold, nightOverSea, 0.12),
    "EngineerPanel.css .engineer-stale-note",
  ],
  ["メッセージ種別の札", "text", gold, stormDeep, "EngineerPanel.css .engineer-msg-type"],
  [
    "手紙の詳細の文字",
    "projector",
    paper,
    letterDetail,
    "EngineerPanel.css .engineer-letter-detail",
  ],
  ["押したボタン", "text", stormDeep, gold, "EngineerPanel.css button[aria-pressed]"],
  ["開いた切り替え", "text", paper, stormDeep, "EngineerPanel.css .engineer-toggle[aria-expanded]"],
  // 夜の配色（デモ操作のキー）
  ["デモ操作のキーの文字", "projector", paper, nightOverPaper, "DemoKeys.css .demo-keys"],
  [
    "操作できないときのデモ操作のキー",
    "text",
    mixHex(paper, nightOverPaper, 0.7),
    nightOverPaper,
    "DemoKeys.css .demo-keys-list[aria-disabled] opacity 0.7",
  ],
  ["キーの札", "text", ink, paper, "DemoKeys.css kbd"],
  ["早送り中の札", "text", stormDeep, gold, "DemoKeys.css .demo-keys-fast"],
  // 文字でない部品（線・縁・フォーカスの輪）
  ["フォーカスの輪と紙の地", "ui", seaDeep, paper, "base.css :focus-visible（--focus-ring-color）"],
  [
    "フォーカスの輪とパネル",
    "ui",
    seaDeep,
    paperDeep,
    "base.css :focus-visible（--focus-ring-color）",
  ],
  [
    "フォーカスの輪と海",
    "ui",
    ink,
    seaMid,
    "GameScreen.css .game-map（--focus-ring-color-on-sea）",
  ],
  [
    "フォーカスの輪と淡い海",
    "ui",
    ink,
    seaLight,
    "TitleScreen.css .app-title（--focus-ring-color-on-sea）",
  ],
  [
    "フォーカスの輪と夜の地",
    "ui",
    gold,
    nightOverSea,
    "EngineerPanel.css .engineer-panel（--focus-ring-color-on-night）",
  ],
  ["カードの縁", "ui", inkMuted, paper, "IslandList.css・FerryList.css .…__card"],
  ["紙のボタンの縁", "ui", ink, paper, "base.css .btn"],
  ["エンジニアパネルの表の罫", "ui", seaMid, nightOverSea, "EngineerPanel.css td（点線）"],
  ["エンジニアパネルのボタンの縁", "ui", seaLight, nightOverSea, "EngineerPanel.css button"],
  ["デモ操作のキーの縁", "ui", seaMid, nightOverPaper, "DemoKeys.css .demo-keys（破線）"],
  ["キーの札と夜の地", "ui", paper, nightOverPaper, "DemoKeys.css kbd"],
  ["切り替えと海", "ui", ink, seaMid, "EngineerPanel.css .engineer-toggle の縁"],
];

describe("使っている色の組のコントラスト比（T-425）", () => {
  it.each(PAIRS)("%s（%s）", (_name, kind, foreground, background) => {
    expect(contrastRatio(foreground, background)).toBeGreaterThanOrEqual(MIN[kind]);
  });

  it("組の名前が重ならない", () => {
    expect(new Set(PAIRS.map(([name]) => name)).size).toBe(PAIRS.length);
  });
});

describe("mixHex", () => {
  it("両端と中間が color-mix(in srgb) と同じ", () => {
    expect(mixHex("#000000", "#ffffff", 1)).toBe("#000000");
    expect(mixHex("#000000", "#ffffff", 0)).toBe("#ffffff");
    expect(mixHex("#000000", "#ffffff", 0.5)).toBe("#808080");
  });

  it("--color-danger-text は郵便の赤 60% と墨の混ぜた色", () => {
    expect(mixHex(c("--color-danger"), ink, 0.6)).toBe(dangerText);
  });
});
