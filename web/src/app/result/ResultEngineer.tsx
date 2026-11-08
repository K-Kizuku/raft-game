// リザルトのエンジニアモード（T-701・FR-ENG-09）。「E エンジニアモード」のボタンか E キーで、
// Term・Leader・Commit のタイムラインの節を開く。記録はストアの timeline（届いたイベントを並べたもの）をそのまま描く。
// 開いたときはフォーカスを動かさない（ゲーム中のエンジニアパネルと同じ）。
import { useEffect, useId, useMemo, useRef, useState, type ReactElement } from "react";

import { useGameStore } from "../../state/index.ts";
import { isEngineerToggleKey } from "../../ui/engineer/engineerKey.ts";
import { Timeline } from "../../ui/timeline/Timeline.tsx";
import { buildTimelineView, type TimelineIsland } from "../../ui/timeline/timelineView.ts";
import { tokens } from "../../ui/theme/tokens.ts";
import { termColor } from "./resultView.ts";
import "./ResultEngineer.css";

const SECTION_ID = "result-engineer";
const LEAD =
  "代表（Leader）の任期（Term）・交代と、共和国で確定した記録の番号（Commit Index）の移り変わりです。";
const NO_COLOR = tokens["--color-ink-muted"];

function prefersReducedMotion(): boolean {
  return (
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

export function ResultEngineer(): ReactElement {
  const [open, setOpen] = useState(false);
  const headingId = useId();
  const sectionRef = useRef<HTMLElement>(null);
  const timeline = useGameStore((s) => s.timeline);
  const world = useGameStore((s) => s.world);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent): void {
      if (isEngineerToggleKey(event)) setOpen((value) => !value);
    }
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
    };
  }, []);

  useEffect(() => {
    if (!open) return;
    const section = sectionRef.current;
    if (section === null || typeof section.scrollIntoView !== "function") return;
    section.scrollIntoView({
      block: "start",
      behavior: prefersReducedMotion() ? "auto" : "smooth",
    });
  }, [open]);

  const islands = useMemo<TimelineIsland[]>(() => {
    if (world === null) return [];
    return world.stage.islandIds.map((id) => {
      const island = world.stage.islands[id];
      return { id, name: island?.name ?? `島 ${id}`, color: island?.color ?? NO_COLOR };
    });
  }, [world]);
  const endsAt = world?.clock.endsAt ?? 0;
  const view = useMemo(() => {
    const last = timeline.items[timeline.items.length - 1];
    const endT = Math.max(endsAt, last?.t ?? 0);
    return buildTimelineView({ timeline, endT, islands });
  }, [timeline, endsAt, islands]);

  return (
    <div className="result-engineer-area">
      <button
        type="button"
        className="result-engineer-toggle"
        aria-expanded={open}
        aria-controls={SECTION_ID}
        aria-keyshortcuts="E"
        onClick={() => {
          setOpen((value) => !value);
        }}
      >
        E エンジニアモード
      </button>
      {open && (
        <section
          id={SECTION_ID}
          ref={sectionRef}
          className="result-section result-engineer"
          aria-labelledby={headingId}
        >
          <h2 id={headingId}>タイムライン</h2>
          <p className="result-engineer-lead">{LEAD}</p>
          <Timeline view={view} termColor={termColor} />
          {view.truncated && <p className="result-engineer-note">古い記録は省きました。</p>}
        </section>
      )}
    </div>
  );
}
