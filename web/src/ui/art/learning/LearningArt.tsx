// ステージ紹介の「このステージで学べること」の小さな図（T-433・docs/11-visual-design.md §7）。
// 学べることのキー（ステージ定義の learning）ごとに、1 場面の絵を描く。文字は持たない（説明は隣の文が伝える）
// ので aria-hidden にする。色は art.css・learning.css のクラス（トークン）で塗り、ここには色を書かない。
import type { ReactElement } from "react";

import { Envelope, Star } from "../parts.tsx";
import "./learning.css";

// 図のある学べることのキー（web/src/app/stage-intro/introView.ts の LEARNING_LABELS と同じ並び）。
export const LEARNING_ART_KEYS = [
  "log_replication",
  "commit",
  "quorum",
  "prevote",
  "heartbeat",
  "election_timeout",
  "leader_election",
  "check_quorum",
  "state_machine_replication",
  "network_partition",
] as const;

export type LearningArtKey = (typeof LEARNING_ART_KEYS)[number];

export function isLearningArtKey(key: string): key is LearningArtKey {
  return (LEARNING_ART_KEYS as readonly string[]).includes(key);
}

// 図の大きさ（座標）。表示の大きさは CSS で決める。
const VIEW_BOX = "0 0 120 72";

type Point = { x: number; y: number };

// 島の様子。leader は代表（金の★）、former は退いた・連絡の途切れた代表（淡い★）、
// faded はまだ返事のない・数に入らない島（淡く）。
type IsleState = "calm" | "leader" | "former" | "faded";

// 小さな丸い島（砂浜＋緑の丘）。代表なら上に金の★を載せる。
function Isle({ x, y, state = "calm" }: Point & { state?: IsleState }): ReactElement {
  return (
    <g
      transform={`translate(${x} ${y})`}
      className={
        state === "faded" || state === "former" ? "learning-isle learning-faded" : "learning-isle"
      }
      data-isle={state}
    >
      <circle className="art-shoal" cx={0} cy={1} r={12} />
      <circle className="art-sand" cx={0} cy={0} r={8.5} />
      <path
        className="art-green"
        d="M-5.5 1 C-5.5 -4 -2.5 -6 0 -6 C3 -6 5.5 -3.5 5.5 0 C3 2 -3 2 -5.5 1 Z"
      />
      {(state === "leader" || state === "former") && <Star x={0} y={-15} scale={0.32} />}
    </g>
  );
}

// 封筒（手紙）。動きを許すときは小さく上下する。
function Letter({ x, y }: Point): ReactElement {
  return (
    <g className="learning-bob" data-letter="">
      <Envelope x={x} y={y} scale={0.26} />
    </g>
  );
}

// 「記録したよ」「賛成」などの返事の印（金の丸に✓）。
function Check({ x, y }: Point): ReactElement {
  return (
    <g transform={`translate(${x} ${y})`} data-check="">
      <circle className="art-gold" cx={0} cy={0} r={4.5} />
      <path className="art-line" d="M-2.2 0 L-0.6 1.8 L2.4 -1.8" />
    </g>
  );
}

// 航路（海図の点線）。
function Route({ from, to }: { from: Point; to: Point }): ReactElement {
  return (
    <path className="learning-route" d={`M${from.x} ${from.y} L${to.x} ${to.y}`} data-route="" />
  );
}

// 台帳（紙に記録の行）。rows は上からの行の色（committed は確定の金）。
type LedgerRow = "entry" | "committed" | "postal";

function Ledger({ x, y, rows }: Point & { rows: readonly LedgerRow[] }): ReactElement {
  const rowClass: Readonly<Record<LedgerRow, string>> = {
    entry: "art-sea-deep",
    committed: "art-gold",
    postal: "art-hull",
  };
  return (
    <g transform={`translate(${x} ${y})`} data-ledger={rows.join(" ")}>
      <rect className="art-paper" x={-7} y={-9} width={14} height={18} rx={2} />
      {rows.map((row, i) => (
        <rect
          key={i}
          className={rowClass[row]}
          x={-4.5}
          y={-6 + i * 5}
          width={9}
          height={3}
          rx={1}
        />
      ))}
    </g>
  );
}

// 嵐の雲と雨（docs/11 §9。色だけでなく雨の線の形でも示す）。
function StormCloud({ x, y, rain }: Point & { rain: number }): ReactElement {
  const drops = [-10, -3, 4, 11];
  return (
    <g transform={`translate(${x} ${y})`} data-storm="">
      {drops.map((dx) => (
        <path key={dx} className="learning-rain" d={`M${dx} 6 L${dx - 5} ${6 + rain}`} />
      ))}
      <path
        className="learning-cloud"
        d="M-16 6 C-22 6 -22 -3 -15 -3 C-15 -10 -5 -12 -2 -6 C1 -12 12 -11 12 -3 C19 -3 19 6 13 6 Z"
      />
    </g>
  );
}

// 「？」（文字にせず線で描く。文字にすると支援技術やテストの本文に混ざるため）。
function Question({ x, y }: Point): ReactElement {
  return (
    <g transform={`translate(${x} ${y})`} data-question="">
      <rect className="art-paper" x={-7} y={-7} width={14} height={13} rx={4} />
      <path className="art-line" d="M-2.5 -3 C-2.5 -5.5 2.5 -5.5 2.5 -2.8 C2.5 -1 0 -1 0 1.2" />
      <circle className="art-ink" cx={0} cy={3.6} r={0.9} />
    </g>
  );
}

// 時計（定期便を待つ時間）。
function Clock({ x, y }: Point): ReactElement {
  return (
    <g transform={`translate(${x} ${y})`} data-clock="">
      <circle className="art-paper" cx={0} cy={0} r={7} />
      <path className="art-line" d="M0 -4 L0 0 L3 2" />
    </g>
  );
}

function Picture({ learningKey }: { learningKey: LearningArtKey }): ReactElement {
  switch (learningKey) {
    case "log_replication": {
      // 代表が「記録して」を配り、ほかの島も台帳に同じ記録を写す。
      const leader = { x: 34, y: 42 };
      const a = { x: 84, y: 20 };
      const b = { x: 84, y: 58 };
      return (
        <g>
          <Route from={leader} to={a} />
          <Route from={leader} to={b} />
          <Isle {...leader} state="leader" />
          <Isle {...a} />
          <Isle {...b} />
          <Ledger x={12} y={42} rows={["entry"]} />
          <Ledger x={105} y={20} rows={["entry"]} />
          <Ledger x={105} y={58} rows={["entry"]} />
          <Letter x={59} y={31} />
          <Letter x={59} y={50} />
        </g>
      );
    }
    case "commit": {
      // 過半数（代表＋2 島）の「記録したよ」がそろうと、記録が確定（金）になる。
      const leader = { x: 60, y: 30 };
      const isles = [
        { x: 18, y: 18 },
        { x: 102, y: 18 },
        { x: 18, y: 56 },
        { x: 102, y: 56 },
      ];
      return (
        <g>
          {isles.map((p) => (
            <Route key={`${p.x},${p.y}`} from={p} to={leader} />
          ))}
          {isles.map((p, i) => (
            <Isle key={`${p.x},${p.y}`} {...p} state={i < 2 ? "calm" : "faded"} />
          ))}
          <Isle {...leader} state="leader" />
          <Check x={30} y={8} />
          <Check x={114} y={8} />
          <Ledger x={60} y={58} rows={["entry", "committed"]} />
        </g>
      );
    }
    case "quorum": {
      // 5 島のうち 3 島（半分より多く）がそろえば進める。
      const xs = [14, 37, 60, 83, 106];
      return (
        <g>
          <rect className="learning-group" x={2} y={20} width={70} height={40} rx={12} />
          {xs.map((x, i) => (
            <Isle key={x} x={x} y={44} state={i < 3 ? "calm" : "faded"} />
          ))}
          {xs.slice(0, 3).map((x) => (
            <Check key={x} x={x} y={14} />
          ))}
        </g>
      );
    }
    case "prevote": {
      // 立候補する前に「勝てそう？」と周りに相談し、賛成をもらってから立候補する。
      const asker = { x: 30, y: 46 };
      const a = { x: 92, y: 22 };
      const b = { x: 92, y: 58 };
      return (
        <g>
          <Route from={asker} to={a} />
          <Route from={asker} to={b} />
          <Isle {...asker} />
          <Isle {...a} />
          <Isle {...b} />
          <Question x={30} y={20} />
          <Check x={108} y={12} />
          <Check x={108} y={48} />
        </g>
      );
    }
    case "heartbeat": {
      // 代表が定期便（灰の丸い手紙）を出し続け、元気だと知らせる。
      const leader = { x: 20, y: 42 };
      const island = { x: 100, y: 42 };
      return (
        <g>
          <Route from={leader} to={island} />
          <Isle {...leader} state="leader" />
          <Isle {...island} />
          {[42, 60, 78].map((x, i) => (
            <circle
              key={x}
              className={`learning-beat learning-beat-${i + 1}`}
              cx={x}
              cy={42}
              r={4}
              data-beat=""
            />
          ))}
        </g>
      );
    }
    case "election_timeout": {
      // 定期便が途切れ、待つ時間が過ぎると、島は「代表がいなくなった？」と考える。
      const leader = { x: 18, y: 44 };
      const island = { x: 98, y: 44 };
      return (
        <g>
          <Route from={leader} to={{ x: 48, y: 44 }} />
          <Isle {...leader} state="former" />
          <Isle {...island} />
          <circle className="learning-beat learning-faded" cx={40} cy={44} r={4} data-beat="" />
          <Clock x={98} y={18} />
          <path className="learning-break" d="M58 36 L68 52 M68 36 L58 52" data-break="" />
        </g>
      );
    }
    case "leader_election": {
      // 立候補した島が過半数の投票を集め、新しい代表（★）になる。
      const candidate = { x: 60, y: 44 };
      const voters = [
        { x: 16, y: 22 },
        { x: 104, y: 22 },
      ];
      const others = [
        { x: 16, y: 60 },
        { x: 104, y: 60 },
      ];
      return (
        <g>
          {voters.map((p) => (
            <Route key={`${p.x},${p.y}`} from={p} to={candidate} />
          ))}
          {voters.map((p) => (
            <Isle key={`${p.x},${p.y}`} {...p} />
          ))}
          {others.map((p) => (
            <Isle key={`${p.x},${p.y}`} {...p} state="faded" />
          ))}
          <Isle {...candidate} state="leader" />
          <Letter x={38} y={33} />
          <Letter x={82} y={33} />
        </g>
      );
    }
    case "check_quorum": {
      // 嵐で返事が過半数から届かない代表は、自分から代表を退く（★が下りる）。
      const leader = { x: 22, y: 46 };
      return (
        <g>
          <Route from={leader} to={{ x: 98, y: 22 }} />
          <Route from={leader} to={{ x: 98, y: 60 }} />
          <StormCloud x={60} y={22} rain={36} />
          <Isle {...leader} state="former" />
          <Isle x={98} y={22} />
          <Isle x={98} y={60} />
          <path className="art-line" d="M40 12 L40 26 M36 22 L40 26 L44 22" data-step-down="" />
        </g>
      );
    }
    case "state_machine_replication": {
      // どの島も、確定した記録を同じ順に当てはめるので、台帳が同じになる。
      const xs = [20, 60, 100];
      const rows: readonly LedgerRow[] = ["committed", "entry", "postal"];
      return (
        <g>
          {xs.map((x) => (
            <g key={x}>
              <Ledger x={x} y={20} rows={rows} />
              <Isle x={x} y={54} />
            </g>
          ))}
          <path className="art-line" d="M35 17 L45 17 M35 23 L45 23 M75 17 L85 17 M75 23 L85 23" />
        </g>
      );
    }
    case "network_partition": {
      // 嵐で島が 2 つに分かれ、島の多い側で新しい代表が選ばれる。
      return (
        <g>
          <StormCloud x={58} y={14} rain={52} />
          <Isle x={16} y={30} state="former" />
          <Isle x={30} y={60} />
          <Isle x={88} y={24} state="leader" />
          <Isle x={108} y={48} />
          <Isle x={80} y={60} />
        </g>
      );
    }
  }
}

export function LearningArt({
  learningKey,
  className,
}: {
  learningKey: string;
  className?: string;
}): ReactElement | null {
  if (!isLearningArtKey(learningKey)) {
    return null;
  }
  return (
    <svg
      className={className === undefined ? "art learning-art" : `art learning-art ${className}`}
      viewBox={VIEW_BOX}
      aria-hidden="true"
      focusable="false"
      data-learning-art={learningKey}
    >
      <Picture learningKey={learningKey} />
    </svg>
  );
}
