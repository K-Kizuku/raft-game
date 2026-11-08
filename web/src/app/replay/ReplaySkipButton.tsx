// リプレイ中だけゲーム画面に出す「次の出来事まで」のボタン（T-720f・ADR-0028 R4・DesignDoc §10.4）。
// cmd.game.skip を送り、記録の再生（net/replay/server.ts）が次の出来事の時刻まで進めて止まる。
// 表示はサーバーの clock・events が変わるまで変えない（ADR-0009）。航海中（sailing）でないとき・送れないときは押せない。
import { useEffect, useRef, useState } from "react";
import type { ReactElement } from "react";

import type { Ack } from "../../protocol/index.ts";
import { useGameStore } from "../../state/index.ts";
import { useGameCommand } from "../../ui/commands/GameCommands.tsx";

function failureText(ack: Ack): string {
  switch (ack.code) {
    case "phase_invalid":
      return "いまは次の出来事まで進められません。";
    case "rate_limited":
      return "操作が多すぎます。少し待ってください。";
    default:
      return "次の出来事まで進められませんでした。";
  }
}

export function ReplaySkipButton(): ReactElement {
  const send = useGameCommand();
  const sailing = useGameStore((s) => s.world?.phase === "sailing");
  const [pending, setPending] = useState(false);
  const [status, setStatus] = useState("");
  // 送っている間にアンマウントされたら、結果を捨てる。
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  function skip(): void {
    if (send === null || pending) return;
    setPending(true);
    let promise: Promise<Ack>;
    try {
      promise = send("cmd.game.skip", { until: "next_notable" });
    } catch (err) {
      promise = Promise.reject(err instanceof Error ? err : new Error(String(err)));
    }
    promise.then(
      (ack) => {
        if (!mounted.current) return;
        setPending(false);
        setStatus(ack.ok ? "" : failureText(ack));
      },
      () => {
        if (!mounted.current) return;
        setPending(false);
        setStatus("次の出来事まで進められませんでした。");
      },
    );
  }

  return (
    <div className="game-connection-mock">
      <button type="button" onClick={skip} disabled={send === null || !sailing || pending}>
        次の出来事まで
      </button>
      {status !== "" && <p role="status">{status}</p>}
    </div>
  );
}
