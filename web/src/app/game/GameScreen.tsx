// ゲーム画面のレイアウト（FR-UI-03・ゲームデザイン §7.2・DesignDoc §13.6）。
// 各領域の中身はスロットのコンポーネントが持つ。この画面は、ラウンドの接続（useRoundConnection）を作って
// ストアにつなぎ、パネルにコマンドを送る関数（GameCommandProvider）と sub.engineer の関数を渡し、
// 接続の状態を出す。round.result を受けたら（ストアに結果を入れてから）onRoundEnd を呼ぶ。
import type { ReactElement } from "react";

import { MapView } from "../../game/MapView.tsx";
import { GameCommandProvider } from "../../ui/commands/GameCommands.tsx";
import { DemoKeys } from "../../ui/demo/DemoKeys.tsx";
import { EngineerPanel } from "../../ui/engineer/EngineerPanel.tsx";
import { EngineerSubscriptionProvider } from "../../ui/engineer/EngineerSubscription.tsx";
import { FerryPanel } from "../../ui/ferry/FerryPanel.tsx";
import { HudBar } from "../../ui/hud/HudBar.tsx";
import { Notifications } from "../../ui/hud/Notifications.tsx";
import { IslandPanel } from "../../ui/island/IslandPanel.tsx";
import { OpeningCeremony } from "../../ui/opening/index.ts";
import { Link } from "../router/Link.tsx";
import { TutorialOverlay } from "../tutorial/TutorialOverlay.tsx";
import type { RoundContext } from "../types.ts";
import { connectionNotice, mockNotice } from "./connectionText.ts";
import type { RoundConnector } from "./connector.ts";
import { useRoundConnection } from "./useRoundConnection.ts";
import "./GameScreen.css";

export type GameScreenProps = {
  round: RoundContext;
  onRoundEnd: () => void;
  connector: RoundConnector;
};

export function GameScreen({ round, onRoundEnd, connector }: GameScreenProps): ReactElement {
  const { view, send, subscribe } = useRoundConnection({
    session: round.session,
    connector,
    onRoundEnd,
  });
  const notice = connectionNotice(view);
  const mock = mockNotice(view);

  return (
    <GameCommandProvider send={send}>
      <EngineerSubscriptionProvider subscribe={subscribe}>
        <div className="game-screen">
          <h1 tabIndex={-1} className="visually-hidden">
            {round.stage.title}
          </h1>
          <section aria-label="状況" className="game-hud">
            <HudBar />
          </section>
          <section aria-label="マップ" className="game-map">
            <MapView />
            <section aria-label="お知らせ" aria-live="polite" className="game-notifications">
              <Notifications />
            </section>
            {(notice !== null || mock !== null) && (
              <div className="game-connection">
                {notice !== null && (
                  <div
                    role={notice.tone === "error" ? "alert" : "status"}
                    className={`game-connection-notice game-connection-${notice.tone}`}
                  >
                    <p>{notice.text}</p>
                    {notice.tone === "error" && (
                      <Link to={{ name: "stageSelect" }}>ステージ選択へ</Link>
                    )}
                  </div>
                )}
                {mock !== null && <p className="game-connection-mock">{mock}</p>}
              </div>
            )}
          </section>
          <aside aria-label="島の詳細" className="game-island">
            <IslandPanel />
          </aside>
          <section aria-label="フェリー操作" className="game-ferry">
            <FerryPanel />
          </section>
          {view.kind === "offline" && (
            <div className="game-offline">
              <p>サーバーに接続していません（画面の骨組みの表示です）。</p>
              <button type="button" onClick={onRoundEnd}>
                リザルトへ（仮）
              </button>
            </div>
          )}
          <EngineerPanel />
          <DemoKeys />
          <OpeningCeremony />
          <TutorialOverlay stageId={round.stage.id} />
        </div>
      </EngineerSubscriptionProvider>
    </GameCommandProvider>
  );
}
