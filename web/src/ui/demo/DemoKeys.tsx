// ゲーム画面に重ねるスロット「デモ操作のキー」（T-401 のスロット、中身は T-411）。
// デモのセッション（snapshot の session.demo が真）でだけ、D・F（Shift+F）・N・Space を受け付ける
// （進行表 §8・ゲームデザイン §7.6）。データは props で受けず、ストア（T-403）から読む（ADR-0010・DesignDoc §13.3）。
// 表示はサーバーの clock・events が変わるまで変えない（ADR-0009）。
import { useEffect, useRef, useState } from "react";
import type { ReactElement } from "react";

import type { Ack } from "../../protocol/index.ts";
import { gameStore, useGameStore } from "../../state/index.ts";
import { useGameCommand } from "../commands/GameCommands.tsx";
import type { GameCommandSender } from "../commands/GameCommands.tsx";
import { DemoStormFlavor } from "./DemoStormFlavor.tsx";
import { demoActionOf, fastSpeed } from "./demoKeys.ts";
import type { DemoAction } from "./demoKeys.ts";
import "./DemoKeys.css";

export function DemoKeys(): ReactElement | null {
  const demo = useGameStore((s) => s.world !== null && s.world.session.demo);
  if (!demo) return null;
  return (
    <>
      <DemoKeysActive />
      <DemoStormFlavor />
    </>
  );
}

// 失敗の文に入れる〈操作〉の言い方。
const OPERATION: Readonly<Record<DemoAction, string>> = {
  storm: "嵐を起こせ",
  skip: "次の出来事まで進められ",
  pause: "一時停止・再開でき",
  fast: "速度を変えられ",
};

function failureText(action: DemoAction, ack: Ack): string {
  const operation = OPERATION[action];
  switch (ack.code) {
    case "phase_invalid":
      return `いまは${operation}ません。`;
    case "rate_limited":
      return "操作が多すぎます。少し待ってください。";
    default:
      return `${operation}ませんでした。`;
  }
}

// 結果を待つ操作（D・N・Space）。F の速度の電文は待たずに順に送る。
type PendingAction = Exclude<DemoAction, "fast">;

type Hold = { restore: number; current: number };

function DemoKeysActive(): ReactElement {
  const send = useGameCommand();
  const sailing = useGameStore((s) => s.world?.phase === "sailing");
  const [status, setStatus] = useState("");
  const [fast, setFast] = useState<number | null>(null);

  // ハンドラーはその時点の値を読む（古いクロージャの値で速度を戻さない）。
  const sendRef = useRef<GameCommandSender | null>(send);
  useEffect(() => {
    sendRef.current = send;
  }, [send]);

  useEffect(() => {
    let mounted = true;
    let hold: Hold | null = null;
    const pending: Record<PendingAction, boolean> = { storm: false, skip: false, pause: false };

    function settle(action: DemoAction, promise: Promise<Ack>): void {
      promise.then(
        (ack) => {
          if (!mounted) return;
          setStatus(ack.ok ? "" : failureText(action, ack));
        },
        () => {
          if (!mounted) return;
          setStatus(`サーバーにつながっていないため、${OPERATION[action]}ませんでした。`);
        },
      );
    }

    function call(sender: GameCommandSender, invoke: (s: GameCommandSender) => Promise<Ack>) {
      try {
        return invoke(sender);
      } catch (err) {
        return Promise.reject(err instanceof Error ? err : new Error(String(err)));
      }
    }

    function sendSpeed(speed: number): void {
      const sender = sendRef.current;
      if (sender === null) return;
      settle(
        "fast",
        call(sender, (s) => s("cmd.game.speed", { speed })),
      );
    }

    function runPending(
      action: PendingAction,
      sender: GameCommandSender,
      invoke: (s: GameCommandSender) => Promise<Ack>,
    ): void {
      if (pending[action]) return;
      pending[action] = true;
      const promise = call(sender, invoke);
      promise.then(
        () => {
          pending[action] = false;
        },
        () => {
          pending[action] = false;
        },
      );
      settle(action, promise);
    }

    // F を押している間に Shift を押した・離した。
    function shiftChanged(shift: boolean): void {
      if (hold === null) return;
      const world = gameStore.getState().world;
      if (world === null) return;
      const target = fastSpeed(shift, world.clock.allowedSpeeds);
      if (target === null || target === hold.current) return;
      hold = { restore: hold.restore, current: target };
      setFast(target);
      sendSpeed(target);
    }

    // F を離した・ウィンドウのフォーカスが外れた。押したときの速度に戻す。
    function release(): void {
      if (hold === null) return;
      const { restore, current } = hold;
      hold = null;
      setFast(null);
      if (restore !== current) sendSpeed(restore);
    }

    function onKeyDown(event: KeyboardEvent): void {
      if (hold !== null && event.key === "Shift") {
        shiftChanged(true);
        return;
      }
      const action = demoActionOf(event);
      if (action === null) return;
      const sender = sendRef.current;
      const world = gameStore.getState().world;
      if (sender === null || world === null || world.phase !== "sailing") return;
      event.preventDefault();
      switch (action) {
        case "storm":
          runPending("storm", sender, (s) => s("demo.trigger", { event: "storm" }));
          return;
        case "skip":
          runPending("skip", sender, (s) => s("cmd.game.skip", { until: "next_notable" }));
          return;
        case "pause": {
          const paused = !world.clock.paused;
          runPending("pause", sender, (s) => s("cmd.game.pause", { paused }));
          return;
        }
        case "fast": {
          if (hold !== null) return;
          const restore = world.clock.speed;
          const target = fastSpeed(event.shiftKey, world.clock.allowedSpeeds);
          if (target === null) return;
          hold = { restore, current: target };
          setFast(target);
          if (target !== restore) sendSpeed(target);
          return;
        }
      }
    }

    function onKeyUp(event: KeyboardEvent): void {
      if (event.code === "KeyF") {
        release();
        return;
      }
      if (hold !== null && event.key === "Shift") shiftChanged(false);
    }

    function onBlur(): void {
      release();
    }

    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("keyup", onKeyUp);
    window.addEventListener("blur", onBlur);
    return () => {
      // 画面を離れたら hold を捨てる（送らない）。結果が届いても捨てる。
      mounted = false;
      hold = null;
      window.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("keyup", onKeyUp);
      window.removeEventListener("blur", onBlur);
    };
  }, []);

  const enabled = send !== null && sailing;
  return (
    <aside aria-label="デモ操作" className="demo-keys">
      <ul className="demo-keys-list" aria-disabled={enabled ? undefined : "true"}>
        <li>
          <kbd>D</kbd> 嵐を起こす
        </li>
        <li>
          <kbd>F</kbd> 押している間 4 倍速（<kbd>Shift</kbd> で 8 倍速）
        </li>
        <li>
          <kbd>N</kbd> 次の出来事まで進めて止まる
        </li>
        <li>
          <kbd>Space</kbd> 一時停止・再開
        </li>
      </ul>
      {fast !== null && <p className="demo-keys-fast">早送り中 ×{String(fast)}</p>}
      <p role="status" className="demo-keys-status">
        {status}
      </p>
    </aside>
  );
}
