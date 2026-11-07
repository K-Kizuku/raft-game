// 手紙の種類（label）ごとの名前・色・形（T-406・ゲームデザイン §2.1）。
// 色だけに頼らず、形・塗り・破線・印の組でも区別する（NFR-10）。マップ（T-404）は山と数だけを描き、
// 1 通ずつのアイコン（LetterIcon）は DOM のパネルに出す（DesignDoc §13.3）。
import type { LetterLabel } from "../../protocol/index.ts";
import { tokens } from "../theme/tokens.ts";

export type LetterShape = "circle" | "square" | "triangle" | "star" | "diamond";
export type LetterMark = "none" | "dot" | "check" | "cross" | "reply";
export type LetterKindStyle = Readonly<{
  name: string; // ゲーム上の名前（ゲームデザイン §2.1）
  color: string; // 塗りの色
  shape: LetterShape;
  fill: "solid" | "outline";
  dashed: boolean;
  mark: LetterMark;
  countBadge: boolean; // 記録の件数のバッジ（append だけ）
}>;

// 種類ごとの色はデザイントークン（T-420・T-422）の値。SVG の属性には var() が効かないことがあるので値を入れる。
// 縁取りだけの形（outline）の線の色になるので、白の塗り・パネルの地（--color-paper-deep）と 3:1 以上の色にする。
const GRAY = tokens["--color-ink-muted"];
const BLUE = tokens["--color-sea-deep"];
const RED = tokens["--color-postal"];
const GOLD = tokens["--color-gold"];
// その他の手紙は生成りの紙の色（墨の線で形を示す）。
const PAPER = tokens["--color-paper"];

function kind(
  name: string,
  color: string,
  shape: LetterShape,
  fill: "solid" | "outline",
  dashed: boolean,
  mark: LetterMark,
  countBadge = false,
): LetterKindStyle {
  return Object.freeze({ name, color, shape, fill, dashed, mark, countBadge });
}

// prevote_resp・vote_resp の賛否（✓ / ×）は label に無いので描かず、返事の印（reply）で示す。
export const LETTER_KINDS: Readonly<Record<LetterLabel, LetterKindStyle>> = Object.freeze({
  heartbeat: kind("お元気ですか？", GRAY, "circle", "solid", false, "none"),
  heartbeat_resp: kind("元気です！", GRAY, "circle", "outline", false, "dot"),
  append: kind("この内容を記録して！", BLUE, "square", "solid", false, "none", true),
  append_probe: kind("記録の確認", BLUE, "square", "outline", false, "none"),
  append_resp: kind("記録したよ！", BLUE, "square", "solid", false, "check"),
  append_reject: kind("記録が合わないよ…", BLUE, "square", "solid", false, "cross"),
  prevote: kind("立候補してもいいかな？", RED, "triangle", "outline", true, "none"),
  prevote_resp: kind("いいよ！ / まだ代表は元気だよ", RED, "triangle", "solid", true, "reply"),
  vote: kind("代表に立候補します！", RED, "triangle", "solid", false, "none"),
  vote_resp: kind(
    "あなたに投票します！ / 投票できません",
    RED,
    "triangle",
    "solid",
    false,
    "reply",
  ),
  request: kind("住民の依頼書", GOLD, "star", "solid", false, "none"),
  other: kind("その他の手紙", PAPER, "diamond", "solid", false, "none"),
});

function isLetterLabel(label: string): label is LetterLabel {
  return Object.hasOwn(LETTER_KINDS, label);
}

// 知らない label は「その他の手紙」（プロトコル §8）。
export function letterKind(label: string): LetterKindStyle {
  return isLetterLabel(label) ? LETTER_KINDS[label] : LETTER_KINDS.other;
}
