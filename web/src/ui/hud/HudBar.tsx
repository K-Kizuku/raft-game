// ゲーム画面のスロット「状況」（T-406・ゲームデザイン §7.2 の上部）。
// 残り時間・スコア・確定した依頼の数・フェス準備度と重要な資源・重要な依頼の進み具合（T-431）・一時停止と速度・効果音のオン・オフ（T-434）を出す。
// 値はストア（T-403）から読むだけで、速度と一時停止もサーバーの時計が変わるまで表示を変えない（ADR-0009）。
import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactElement } from "react";

import type { Ack } from "../../protocol/index.ts";
import { useGameStore } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { FireworkIcon } from "../art/icons/Firework.tsx";
import { useGameCommand } from "../commands/GameCommands.tsx";
import { RequestStepper } from "../requests/RequestStepper.tsx";
import { criticalRequestsInProgress } from "../requests/requestProgress.ts";
import { SoundToggle } from "../sound/SoundToggle.tsx";
import { RaftTerm } from "../terms/RaftTerm.tsx";
import {
  adjacentSpeeds,
  festReadiness,
  formatRemaining,
  formatScore,
  formatSpeed,
} from "./format.ts";
import { useSimNow } from "./useSimNow.ts";
import "./HudBar.css";

export function HudBar(): ReactElement {
  const world = useGameStore((s) => s.world);
  if (world === null) {
    return <p>状況（準備中）</p>;
  }
  return <HudContent world={world} />;
}

function HudContent({ world }: { world: World }): ReactElement {
  const simNow = useSimNow();
  const { clock, score } = world;
  const remaining = clock.endsAt - (simNow ?? clock.simTime);
  const fest = festReadiness(score.successes, score.target);
  const critical = world.stage.resources.filter((r) => r.critical);
  return (
    <div className="hud">
      <span className="hud-item hud-time">
        <span aria-hidden="true">⏱</span>
        <span className="visually-hidden">残り時間</span>
        {formatRemaining(remaining)}
      </span>
      <span className="hud-item hud-score">スコア {formatScore(score.total)}</span>
      <span className="hud-item hud-successes">
        <RaftTerm id="apply">確定</RaftTerm>{" "}
        {score.target > 0
          ? `${String(score.successes)}/${String(score.target)}`
          : String(score.successes)}
      </span>
      <span className="hud-item hud-fest">
        フェス準備度
        <meter min={0} max={100} value={fest.percent} aria-label="フェス準備度" />
        {`${String(fest.percent)}%`}
        {critical.length > 0 && (
          <ul aria-label="重要な資源" className="hud-critical-list">
            {critical.map((r) => {
              const secured = score.criticalSecured.includes(r.id);
              return (
                <li
                  key={r.id}
                  className={secured ? "hud-critical is-secured" : "hud-critical"}
                  title={r.label}
                >
                  <FireworkIcon />
                  <span className="visually-hidden">
                    {r.label}（{secured ? "確定" : "まだ"}）
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </span>
      <CriticalRequests world={world} />
      <SpeedControls world={world} />
      <SoundToggle className="hud-sound" />
    </div>
  );
}

// 重要な依頼（花火）の進み具合（T-431）。重要な資源ごとに、まだ終わっていない最後の依頼を 1 つ出す。
function CriticalRequests({ world }: { world: World }): ReactElement | null {
  const requests = useMemo(() => criticalRequestsInProgress(world), [world]);
  if (requests.length === 0) return null;
  return (
    <ul aria-label="重要な依頼の進み具合" className="hud-item hud-critical-requests">
      {requests.map((request) => {
        const label =
          world.stage.resources.find((r) => r.id === request.resource)?.label ?? request.kind;
        return (
          <li key={request.id} className="hud-critical-request" data-request={request.id}>
            <span className="hud-critical-request-label">
              <span aria-hidden="true">🎆</span>
              {label}
            </span>
            <RequestStepper status={request.status} compact />
          </li>
        );
      })}
    </ul>
  );
}

type Operation = "速度を変更" | "一時停止・再開";

function failureText(operation: Operation, ack: Ack): string {
  switch (ack.code) {
    case "phase_invalid":
      return `いまは${operation}できません。`;
    case "rate_limited":
      return "操作が多すぎます。少し待ってください。";
    default:
      return `${operation}できませんでした。`;
  }
}

function SpeedControls({ world }: { world: World }): ReactElement {
  const send = useGameCommand();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  // 送っている間にアンマウントされたら、結果を捨てる。
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const { clock } = world;
  const { slower, faster } = adjacentSpeeds(clock.allowedSpeeds, clock.speed);
  const disabled = send === null || world.phase !== "sailing" || pending;

  function run(operation: Operation, call: () => Promise<Ack>): void {
    setPending(true);
    let promise: Promise<Ack>;
    try {
      promise = call();
    } catch (err) {
      promise = Promise.reject(err instanceof Error ? err : new Error(String(err)));
    }
    promise.then(
      (ack) => {
        if (!mounted.current) return;
        setPending(false);
        setError(ack.ok ? "" : failureText(operation, ack));
      },
      () => {
        if (!mounted.current) return;
        setPending(false);
        setError(`サーバーにつながっていないため、${operation}できませんでした。`);
      },
    );
  }

  function pause(): void {
    if (send === null) return;
    const paused = !clock.paused;
    run("一時停止・再開", () => send("cmd.game.pause", { paused }));
  }

  function changeSpeed(speed: number | null): void {
    if (send === null || speed === null) return;
    run("速度を変更", () => send("cmd.game.speed", { speed }));
  }

  return (
    <>
      <div role="group" aria-label="速度" className="hud-item hud-speed">
        <button type="button" onClick={pause} disabled={disabled}>
          {clock.paused ? "▶ 再開" : "⏸ 一時停止"}
        </button>
        <button
          type="button"
          aria-label="遅くする"
          onClick={() => {
            changeSpeed(slower);
          }}
          disabled={disabled || slower === null}
        >
          ◀
        </button>
        <output aria-label="いまの速度" className="hud-speed-value">
          {formatSpeed(clock.speed)}
        </output>
        <button
          type="button"
          aria-label="速くする"
          onClick={() => {
            changeSpeed(faster);
          }}
          disabled={disabled || faster === null}
        >
          ▶
        </button>
      </div>
      <p role="status" className="hud-status">
        {error}
      </p>
    </>
  );
}
