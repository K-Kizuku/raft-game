// Term・Leader・Commit のタイムラインの図と一覧（T-701・FR-ENG-09）。
// 図は SVG（PixiJS・canvas を使わない）で、見た目のためのもの。内容はすべて図の下の一覧でも読める
// （支援技術・色の区別が難しい人のため）。アニメーションはしない。
// 島の色はステージの値（island.color）、Term の色は props の termColor。ほかの色は CSS のトークン。
import type { ReactElement } from "react";

import {
  formatTimelineTime,
  timelineTicks,
  timeToX,
  type LeaderRow,
  type TimelineView,
} from "./timelineView.ts";
import "./Timeline.css";

export type TimelineProps = { view: TimelineView; termColor: (term: number) => string };

// 図の寸法（viewBox の単位。画面の値で、ゲームの数値ではない）。
const WIDTH = 1000;
const PLOT_X = 160; // 図の左端（左は行の名前の欄）
const PLOT_W = 820;
const TOP = 22; // 「つなぎ直し」の文字の欄
const TERM_H = 28;
const SECTION_GAP = 16;
const ROW_H = 22;
const ROW_PITCH = 28;
const COMMIT_H = 80;
const AXIS_LABEL_H = 26;
const MIN_TERM_LABEL_W = 36; // 帯の中に T{term} を書く最小の幅
const NAME_MAX = 7; // 行の名前の欄の文字数（超えたら省略記号で切る）
const EDGE_W = 3; // open の区間の点線の縁

function shortName(name: string): string {
  const chars = Array.from(name);
  return chars.length <= NAME_MAX ? name : `${chars.slice(0, NAME_MAX - 1).join("")}…`;
}

// 0〜max の値を、下端 bottom・高さ COMMIT_H の y にする（整数の演算で切り捨て）。
function commitY(index: number, max: number, bottom: number): number {
  if (max <= 0) return bottom;
  const scaled = index * COMMIT_H;
  return bottom - (scaled - (scaled % max)) / max;
}

function LeaderRowView({
  row,
  y,
  x,
}: {
  row: LeaderRow;
  y: number;
  x: (t: number) => number;
}): ReactElement {
  const { island } = row;
  const mid = y + ROW_H / 2;
  return (
    <g className="timeline-leader-row" data-island={island.id}>
      <title>{island.name}</title>
      <circle cx={12} cy={mid} r={6} fill={island.color} />
      <text className="timeline-label" x={24} y={mid} dominantBaseline="central">
        {shortName(island.name)}
      </text>
      <rect className="timeline-track" x={PLOT_X} y={y} width={PLOT_W} height={ROW_H} />
      {row.segments.map((segment, i) => {
        const from = x(segment.from);
        const width = Math.max(x(segment.to) - from, 1);
        return (
          <g key={i} data-term={segment.term} data-open={segment.open ? "true" : "false"}>
            <rect
              className="timeline-leader-segment"
              x={from}
              y={y}
              width={width}
              height={ROW_H}
              fill={island.color}
            />
            {segment.open && (
              <line
                className="timeline-open-edge"
                x1={from + width - EDGE_W / 2}
                x2={from + width - EDGE_W / 2}
                y1={y}
                y2={y + ROW_H}
              />
            )}
          </g>
        );
      })}
    </g>
  );
}

function TimelineFigure({ view, termColor }: TimelineProps): ReactElement {
  const x = (t: number): number => PLOT_X + timeToX(t, view, PLOT_W);
  const termY = TOP;
  const leaderY = termY + TERM_H + SECTION_GAP;
  const commitTop = leaderY + view.leaderRows.length * ROW_PITCH + SECTION_GAP;
  const commitBottom = commitTop + COMMIT_H;
  const height = commitBottom + AXIS_LABEL_H;
  const maxCommit = view.commits.reduce((max, point) => Math.max(max, point.index), 0);
  const ticks = timelineTicks(view);

  let stairs = "";
  const firstCommit = view.commits[0];
  if (firstCommit !== undefined) {
    stairs = `M ${x(firstCommit.t)} ${commitY(firstCommit.index, maxCommit, commitBottom)}`;
    for (const point of view.commits.slice(1)) {
      stairs += ` H ${x(point.t)} V ${commitY(point.index, maxCommit, commitBottom)}`;
    }
    stairs += ` H ${x(view.endT)}`;
  }

  return (
    <svg
      className="timeline-figure"
      viewBox={`0 0 ${WIDTH} ${height}`}
      role="img"
      aria-label="Term・Leader・Commit のタイムライン"
    >
      {ticks.map((tick) => (
        <line
          key={tick.t}
          className="timeline-grid"
          x1={x(tick.t)}
          x2={x(tick.t)}
          y1={termY}
          y2={commitBottom}
        />
      ))}

      <g className="timeline-terms">
        <text className="timeline-label" x={0} y={termY + TERM_H / 2} dominantBaseline="central">
          Term
        </text>
        {view.termBands.map((band) => {
          const from = x(band.from);
          const width = Math.max(x(band.to) - from, 1);
          return (
            <g key={`${band.term}-${band.from}`}>
              <rect
                className="timeline-term-band"
                data-term={band.term}
                x={from}
                y={termY}
                width={width}
                height={TERM_H}
                fill={termColor(band.term)}
              />
              {width >= MIN_TERM_LABEL_W && (
                <text
                  className="timeline-term-label"
                  x={from + width / 2}
                  y={termY + TERM_H / 2}
                  textAnchor="middle"
                  dominantBaseline="central"
                >
                  T{band.term}
                </text>
              )}
            </g>
          );
        })}
      </g>

      <g className="timeline-leaders">
        {view.leaderRows.map((row, i) => (
          <LeaderRowView key={row.island.id} row={row} y={leaderY + i * ROW_PITCH} x={x} />
        ))}
      </g>

      <g className="timeline-commits">
        <text
          className="timeline-label"
          x={0}
          y={commitTop + COMMIT_H / 2}
          dominantBaseline="central"
        >
          Commit Index
        </text>
        <text
          className="timeline-commit-max"
          x={PLOT_X - 6}
          y={commitTop}
          textAnchor="end"
          dominantBaseline="hanging"
        >
          {maxCommit}
        </text>
        <text className="timeline-commit-max" x={PLOT_X - 6} y={commitBottom} textAnchor="end">
          0
        </text>
        <rect
          className="timeline-track"
          x={PLOT_X}
          y={commitTop}
          width={PLOT_W}
          height={COMMIT_H}
        />
        {stairs !== "" && <path className="timeline-commit-line" d={stairs} />}
        {view.commits.map((point, i) => (
          <circle
            key={i}
            className="timeline-commit-point"
            cx={x(point.t)}
            cy={commitY(point.index, maxCommit, commitBottom)}
            r={3}
          />
        ))}
      </g>

      <g className="timeline-axis">
        {ticks.map((tick) => (
          <text
            key={tick.t}
            className="timeline-tick"
            x={x(tick.t)}
            y={commitBottom + AXIS_LABEL_H - 6}
            textAnchor="middle"
          >
            {tick.label}
          </text>
        ))}
      </g>

      <g className="timeline-resyncs">
        {view.resyncs.map((t, i) => (
          <g key={i} data-t={t}>
            <line className="timeline-resync" x1={x(t)} x2={x(t)} y1={TOP - 4} y2={commitBottom} />
            <text className="timeline-resync-label" x={x(t)} y={TOP - 8} textAnchor="middle">
              つなぎ直し
            </text>
          </g>
        ))}
      </g>
    </svg>
  );
}

export function Timeline({ view, termColor }: TimelineProps): ReactElement {
  if (view.lines.length === 0) {
    return (
      <div className="timeline">
        <p className="timeline-empty">記録がありません。</p>
      </div>
    );
  }
  return (
    <div className="timeline">
      <TimelineFigure view={view} termColor={termColor} />
      <ol className="timeline-lines" aria-label="出来事の一覧">
        {view.lines.map((line, i) => (
          <li key={i}>
            <time>{formatTimelineTime(line.t)}</time>
            <span>{line.text}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}
