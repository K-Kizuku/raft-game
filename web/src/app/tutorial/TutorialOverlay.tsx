// ゲーム画面に重ねる Stage 1 のチュートリアル（T-409・FR-UI-08・docs/05-game-progression.md §7）。
// 条件はストアの値で判定し、サーバーは関与しない。送るのは自動の一時停止の cmd.game.pause だけで、
// ストアに書かない。進み具合は保存せず、ラウンド（session.id）ごとに最初から出す。
import { useEffect, useReducer, useRef, useState } from "react";
import type { MouseEvent, ReactElement } from "react";

import { useGameStore } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { useGameCommand } from "../../ui/commands/GameCommands.tsx";
import { useSimNow } from "../../ui/hud/useSimNow.ts";
import { useOpeningCeremonyActive } from "../../ui/opening/index.ts";
import { letterRaftTerm } from "../../ui/terms/glossary.ts";
import { RaftTerm, WithRaftTerm } from "../../ui/terms/RaftTerm.tsx";
import {
  advance,
  expand,
  finish,
  initialTutorialState,
  observeFacts,
  pressPrimary,
  resolveIslands,
} from "./machine.ts";
import type { TutorialIslands, TutorialState } from "./machine.ts";
import { INITIAL_PAUSE_MEMORY, onPauseAck, planPause } from "./pause.ts";
import type { PauseMemory } from "./pause.ts";
import { tutorialScriptFor } from "./script.ts";
import type { TextPart, TutorialScript, TutorialStep } from "./script.ts";
import { findTargets } from "./targets.ts";
import "./TutorialOverlay.css";

type Holder = Readonly<{ sessionId: string | null; tutorial: TutorialState }>;
type HolderAction = Readonly<{
  sessionId: string;
  script: TutorialScript;
  update: (state: TutorialState, script: TutorialScript) => TutorialState;
}>;
type Box = Readonly<{ top: number; left: number; width: number; height: number }>;
type Layout = Readonly<{ key: string | null; anchor: Box | null; rings: readonly Box[] }>;

const INITIAL_TUTORIAL: TutorialState = initialTutorialState();
const EMPTY_LAYOUT: Layout = { key: null, anchor: null, rings: [] };
const INITIAL_HOLDER: Holder = { sessionId: null, tutorial: INITIAL_TUTORIAL };

// 進み具合は session.id ごと。違う session.id の操作が来たら最初の状態から始める。
function holderReducer(prev: Holder, action: HolderAction): Holder {
  const same = prev.sessionId === action.sessionId;
  const base = same ? prev.tutorial : initialTutorialState();
  const next = action.update(base, action.script);
  if (same && next === prev.tutorial) return prev;
  return { sessionId: action.sessionId, tutorial: next };
}

function boxOf(element: Element): Box | null {
  const r = element.getBoundingClientRect();
  if (r.width === 0 && r.height === 0) return null;
  return { top: r.top, left: r.left, width: r.width, height: r.height };
}

function sameBox(a: Box | null, b: Box | null): boolean {
  if (a === null || b === null) return a === b;
  return a.top === b.top && a.left === b.left && a.width === b.width && a.height === b.height;
}

function sameLayout(a: Layout, b: Layout): boolean {
  return (
    a.key === b.key &&
    sameBox(a.anchor, b.anchor) &&
    a.rings.length === b.rings.length &&
    a.rings.every((ring, i) => sameBox(ring, b.rings[i] ?? null))
  );
}

function StepText({ parts }: { parts: readonly TextPart[] }): ReactElement {
  return (
    <>
      {parts.map((part, i) =>
        typeof part === "string" ? (
          <span key={i}>{part}</span>
        ) : "letter" in part ? (
          <WithRaftTerm key={i} raft={letterRaftTerm(part.letter)}>
            {part.text}
          </WithRaftTerm>
        ) : (
          <RaftTerm key={i} id={part.term}>
            {part.text}
          </RaftTerm>
        ),
      )}
    </>
  );
}

export function TutorialOverlay({ stageId }: { stageId: string }): ReactElement | null {
  const script = tutorialScriptFor(stageId);
  const world = useGameStore((s) => s.world);
  const simNow = useSimNow();
  const openingActive = useOpeningCeremonyActive();
  const send = useGameCommand();

  const [holder, dispatch] = useReducer(holderReducer, INITIAL_HOLDER);
  const [layout, setLayout] = useState<Layout>(EMPTY_LAYOUT);

  const sessionId = world?.session.id ?? null;
  const islands = script !== null && world !== null ? resolveIslands(world, script) : null;
  const tutorial = holder.sessionId === sessionId ? holder.tutorial : INITIAL_TUTORIAL;
  const sailing = world?.phase === "sailing";
  const visible = script !== null && islands !== null && sailing && !tutorial.finished;
  const current = visible ? tutorial.current : null;
  const step: TutorialStep | null =
    current === null || script === null
      ? null
      : (script.steps.find((s) => s.id === current.id) ?? null);
  const expanded = step !== null && current !== null && current.expanded;
  const stepKey = step === null ? null : step.id;
  const want = script !== null && script.autoPause && expanded && sailing;
  const paused = world?.clock.paused ?? false;

  // 描画の外（effect・rAF・アンマウント）から読む最新の値。
  const latest = useRef<{
    world: World | null;
    islands: TutorialIslands | null;
    send: typeof send;
    sailing: boolean;
  }>({ world: null, islands: null, send: null, sailing: false });
  const memoryRef = useRef<PauseMemory>(INITIAL_PAUSE_MEMORY);
  const pauseSession = useRef<string | null>(null);
  const pressedRef = useRef<HTMLElement | null>(null);
  const asideRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    latest.current = { world, islands, send, sailing };
  });

  // 世界・時刻・開会式が変わるたびに、事実を足して吹き出しを進める。session.id が変わったら最初から。
  useEffect(() => {
    if (script === null || world === null) return;
    const found = resolveIslands(world, script);
    if (found === null) return;
    const input = {
      world,
      islands: found,
      simNow: simNow ?? world.clock.simTime,
      openingActive,
      t8AfterMs: script.t8AfterMs,
    };
    dispatch({
      sessionId: world.session.id,
      script,
      update: (state, s) => {
        const facts = observeFacts(state.facts, input);
        return advance(facts === state.facts ? state : { ...state, facts }, s);
      },
    });
  }, [script, world, simNow, openingActive]);

  // 自動の一時停止（pause.ts）。ステージに台本が無ければ何も送らない。
  useEffect(() => {
    if (script === null) return;
    if (pauseSession.current !== sessionId) {
      pauseSession.current = sessionId;
      memoryRef.current = INITIAL_PAUSE_MEMORY;
    }
    const planned = planPause(memoryRef.current, {
      want,
      stepKey,
      paused,
      sailing,
      canSend: send !== null,
    });
    memoryRef.current = planned.memory;
    if (planned.send === null || send === null) return;
    send("cmd.game.pause", { paused: planned.send }).then(
      (ack) => {
        memoryRef.current = onPauseAck(memoryRef.current, ack.ok, stepKey);
      },
      () => {
        memoryRef.current = onPauseAck(memoryRef.current, false, stepKey);
      },
    );
  }, [script, sessionId, want, stepKey, paused, sailing, send]);

  // アンマウントのときに自分が止めていたら、再開を 1 回送る（結果は捨てる）。
  useEffect(() => {
    const memory = memoryRef;
    const values = latest;
    return () => {
      const { send: sender, sailing: stillSailing } = values.current;
      if (!memory.current.owned || !stillSailing || sender === null) return;
      memory.current = { ...memory.current, owned: false, expected: false };
      sender("cmd.game.pause", { paused: false }).catch(() => undefined);
    };
  }, []);

  // 吹き出しの置き場所（マップの左上）と強調の輪。輪は広がった吹き出しの間だけ rAF で読み直す。
  const shown = step !== null;
  const targets = expanded && step !== null ? step.targets : null;
  useEffect(() => {
    if (!shown) return;
    let frame = 0;
    const measure = (): void => {
      const { world: w, islands: isl } = latest.current;
      const map = document.querySelector(".game-map");
      const anchor = map === null ? null : boxOf(map);
      const rings: Box[] = [];
      if (targets !== null && w !== null && isl !== null) {
        for (const element of findTargets(document, targets, isl, w)) {
          const box = boxOf(element);
          if (box !== null) rings.push(box);
        }
      }
      const next: Layout = { key: stepKey, anchor, rings };
      setLayout((prev) => (sameLayout(prev, next) ? prev : next));
      if (targets !== null) frame = requestAnimationFrame(measure);
    };
    const restart = (): void => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(measure);
    };
    frame = requestAnimationFrame(measure);
    window.addEventListener("resize", restart);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("resize", restart);
    };
  }, [shown, stepKey, targets]);

  // 押したボタンが吹き出しの切り替えで消えたら、次の吹き出しの最初のボタンか <h1> にフォーカスを移す。
  useEffect(() => {
    const pressed = pressedRef.current;
    if (pressed === null) return;
    pressedRef.current = null;
    if (pressed.isConnected && document.activeElement !== document.body) return;
    const target =
      asideRef.current?.querySelector<HTMLElement>("button") ??
      document.querySelector<HTMLElement>(".game-screen h1");
    target?.focus();
  });

  if (step === null || current === null || script === null) return null;

  const run = (
    event: MouseEvent<HTMLButtonElement>,
    update: (state: TutorialState, s: TutorialScript) => TutorialState,
  ): void => {
    pressedRef.current = event.currentTarget;
    if (sessionId !== null) dispatch({ sessionId, script, update });
  };
  const onPrimary = (event: MouseEvent<HTMLButtonElement>): void => {
    run(event, pressPrimary);
  };
  const onExpand = (event: MouseEvent<HTMLButtonElement>): void => {
    run(event, (state) => expand(state));
  };
  const onFinish = (event: MouseEvent<HTMLButtonElement>): void => {
    run(event, (state) => finish(state));
  };

  const anchor = layout.key === stepKey ? layout.anchor : null;
  const position =
    anchor === null
      ? undefined
      : {
          top: `calc(${String(anchor.top)}px + 0.5rem)`,
          left: `calc(${String(anchor.left)}px + 0.5rem)`,
        };
  const rings = expanded && layout.key === stepKey ? layout.rings : [];

  return (
    <>
      {current.expanded ? (
        <aside
          key={`${step.id}:open`}
          ref={asideRef}
          className="tutorial"
          aria-label="チュートリアル"
          data-step={step.id}
          style={position}
        >
          <span className="tutorial__speaker" aria-hidden="true">
            局
          </span>
          <p className="tutorial__text" aria-live="polite">
            <StepText parts={step.text} />
          </p>
          <div className="tutorial__actions">
            <button type="button" className="btn btn-primary" onClick={onPrimary}>
              {step.advance === "click" ? "次へ" : "わかった"}
            </button>
            <button type="button" className="btn btn-quiet" onClick={onFinish}>
              チュートリアルを終える
            </button>
          </div>
        </aside>
      ) : (
        <aside
          key={`${step.id}:closed`}
          ref={asideRef}
          className="tutorial tutorial--collapsed"
          aria-label="チュートリアル"
          data-step={step.id}
          style={position}
        >
          <span className="tutorial__speaker" aria-hidden="true">
            局
          </span>
          <p className="tutorial__text">
            <StepText parts={step.text} />
          </p>
          <div className="tutorial__actions">
            <button type="button" className="btn btn-primary" onClick={onExpand}>
              開く
            </button>
            <button type="button" className="btn btn-quiet" onClick={onFinish}>
              チュートリアルを終える
            </button>
          </div>
        </aside>
      )}
      {rings.map((ring, i) => (
        <div
          key={i}
          className="tutorial-highlight"
          aria-hidden="true"
          style={{ top: ring.top, left: ring.left, width: ring.width, height: ring.height }}
        />
      ))}
    </>
  );
}
