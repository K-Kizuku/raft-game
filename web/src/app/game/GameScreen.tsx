// ゲーム画面のレイアウト（FR-UI-03・ゲームデザイン §7.2・DesignDoc §13.6）。
// 各領域の中身はスロットのコンポーネントが持つ。WS の接続と、ラウンドの終わり
// （round.result を受けた）で onRoundEnd を呼ぶのは T-402・T-403 がここに足す。
import type { ReactElement } from "react";

import { MapView } from "../../game/MapView.tsx";
import { DemoKeys } from "../../ui/demo/DemoKeys.tsx";
import { EngineerPanel } from "../../ui/engineer/EngineerPanel.tsx";
import { FerryPanel } from "../../ui/ferry/FerryPanel.tsx";
import { HudBar } from "../../ui/hud/HudBar.tsx";
import { Notifications } from "../../ui/hud/Notifications.tsx";
import { IslandPanel } from "../../ui/island/IslandPanel.tsx";
import { OpeningCeremony } from "../../ui/opening/index.ts";
import { TutorialOverlay } from "../tutorial/TutorialOverlay.tsx";
import type { RoundContext } from "../types.ts";
import "./GameScreen.css";

export type GameScreenProps = {
  round: RoundContext;
  onRoundEnd: () => void;
};

export function GameScreen({ round, onRoundEnd }: GameScreenProps): ReactElement {
  return (
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
      </section>
      <aside aria-label="島の詳細" className="game-island">
        <IslandPanel />
      </aside>
      <section aria-label="フェリー操作" className="game-ferry">
        <FerryPanel />
      </section>
      {round.session === null && (
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
  );
}
