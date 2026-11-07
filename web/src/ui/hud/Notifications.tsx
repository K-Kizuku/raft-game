// ゲーム画面のスロット「お知らせ」（T-406・ゲームデザイン §7.3）。サーバーの notice を、
// 島の郵便局長のセリフとして出す。GameScreen の「お知らせ」の領域（aria-live）の中に描かれるので、
// ここでは aria-live を足さない。郵便局長の名前と絵がプロトコルに届くまでは「〇〇島の郵便局長」と島の色の丸。
import { useMemo } from "react";
import type { ReactElement } from "react";

import { useGameStore } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { useShowRaftTerms } from "../terms/preference.ts";
import { tokens } from "../theme/tokens.ts";
import { islandName, noticeText } from "./noticeText.ts";
import { useSimNow } from "./useSimNow.ts";
import "./Notifications.css";

// 画面のための値（ゲームの数値ではない）。ゲーム内の 10 秒で消し、新しい 3 件まで出す。
export const NOTICE_VISIBLE_MS = 10000;
export const NOTICE_VISIBLE_COUNT = 3;

// 島がステージに無いときの顔の丸の色（補足の文字の色）。
const FALLBACK_COLOR = tokens["--color-ink-muted"];

type Line = Readonly<{
  seq: number;
  speaker: string;
  initial: string;
  color: string;
  text: string;
}>;

export function Notifications(): ReactElement | null {
  const world = useGameStore((s) => s.world);
  if (world === null) return null;
  return <NoticeList world={world} />;
}

function NoticeList({ world }: { world: World }): ReactElement | null {
  const simNow = useSimNow();
  const show = useShowRaftTerms();
  const lines = useMemo(() => visibleLines(world, simNow, show), [world, simNow, show]);
  if (lines.length === 0) return null;
  return (
    <ol className="notices">
      {lines.map((line) => (
        <li key={line.seq} className="notice">
          <span aria-hidden="true" className="notice-face" style={{ backgroundColor: line.color }}>
            {line.initial}
          </span>
          <span className="notice-body">
            <strong className="notice-speaker">{line.speaker}</strong>
            <span className="notice-text">「{line.text}」</span>
          </span>
        </li>
      ))}
    </ol>
  );
}

// 古い順の notices から、出せるもの（知らない key を除き、10 秒より新しい）の最後の 3 件を新しい順に返す。
function visibleLines(world: World, simNow: number | null, show: boolean): Line[] {
  const lines: Line[] = [];
  for (let i = world.notices.length - 1; i >= 0 && lines.length < NOTICE_VISIBLE_COUNT; i--) {
    const notice = world.notices[i];
    if (notice === undefined) continue;
    if (simNow !== null && !(notice.t > simNow - NOTICE_VISIBLE_MS)) continue;
    const text = noticeText(notice, world, show);
    if (text === null) continue;
    const name = islandName(world, notice.islandId);
    const island = Object.hasOwn(world.stage.islands, notice.islandId)
      ? world.stage.islands[notice.islandId]
      : undefined;
    lines.push({
      seq: notice.seq,
      speaker: `${name}の郵便局長`,
      initial: island === undefined ? "?" : (Array.from(island.name)[0] ?? "?"),
      color: island?.color ?? FALLBACK_COLOR,
      text,
    });
  }
  return lines;
}
