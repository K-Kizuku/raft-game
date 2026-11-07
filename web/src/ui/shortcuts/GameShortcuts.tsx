// ゲーム画面に重ねる「キーボードショートカット」（T-702・FR-UI-09・ゲームデザイン §7.7）。
// Space で一時停止・再開（デモでないセッションの航行中だけ。デモのセッションでは DemoKeys が受ける）、
// 1〜4 で船を選ぶ（フェリー操作パネルの左から順）。データは props で受けず、ストアとコマンドの Context から読む。
// 表示はサーバーの時計が変わるまで変えない（ADR-0009）。描くのは支援技術向けの状態の文だけ。
import { useEffect, useRef, useState } from "react";
import type { ReactElement } from "react";

import type { Ack } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import { useGameCommand } from "../commands/GameCommands.tsx";
import type { GameCommandSender } from "../commands/GameCommands.tsx";
import { shortcutActionOf } from "./shortcutKeys.ts";

// HUD の一時停止のボタンと同じ文。
function pauseFailureText(ack: Ack): string {
  switch (ack.code) {
    case "phase_invalid":
      return "いまは一時停止・再開できません。";
    case "rate_limited":
      return "操作が多すぎます。少し待ってください。";
    default:
      return "一時停止・再開できませんでした。";
  }
}

const PAUSE_OFFLINE_TEXT = "サーバーにつながっていないため、一時停止・再開できませんでした。";

export function GameShortcuts(): ReactElement {
  const send = useGameCommand();
  const [status, setStatus] = useState("");

  // ハンドラーはその時点の送信関数を読む。
  const sendRef = useRef<GameCommandSender | null>(send);
  useEffect(() => {
    sendRef.current = send;
  }, [send]);

  useEffect(() => {
    let mounted = true;
    let pausePending = false;

    function sendPause(sender: GameCommandSender, paused: boolean): void {
      pausePending = true;
      let promise: Promise<Ack>;
      try {
        promise = sender("cmd.game.pause", { paused });
      } catch (err) {
        promise = Promise.reject(err instanceof Error ? err : new Error(String(err)));
      }
      promise.then(
        (ack) => {
          pausePending = false;
          if (!mounted) return;
          setStatus(ack.ok ? "" : pauseFailureText(ack));
        },
        () => {
          pausePending = false;
          if (!mounted) return;
          setStatus(PAUSE_OFFLINE_TEXT);
        },
      );
    }

    function onKeyDown(event: KeyboardEvent): void {
      const state = gameStore.getState();
      const world = state.world;
      if (world === null) return;
      const action = shortcutActionOf(event, world.session.demo);
      if (action === null) return;
      switch (action.kind) {
        case "pause": {
          const sender = sendRef.current;
          if (world.phase !== "sailing" || sender === null || pausePending) return;
          event.preventDefault();
          sendPause(sender, !world.clock.paused);
          return;
        }
        case "ferry": {
          const id = world.ferryIds[action.index];
          if (id === undefined) return;
          event.preventDefault();
          state.selectFerry(id);
          const name = world.ferries[id]?.name ?? id;
          setStatus(`${name}を選びました`);
          return;
        }
      }
    }

    window.addEventListener("keydown", onKeyDown);
    return () => {
      // 画面を離れた後に届いた結果は捨てる。
      mounted = false;
      window.removeEventListener("keydown", onKeyDown);
    };
  }, []);

  return (
    <p role="status" className="visually-hidden">
      {status}
    </p>
  );
}
