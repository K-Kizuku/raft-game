// ゲーム画面のレイアウト（FR-UI-03・ゲームデザイン §7.2・DesignDoc §13.6）。
// 各領域の中身はスロットのコンポーネントが持つ。この画面は、ラウンドの接続（useRoundConnection）を作って
// ストアにつなぎ、パネルにコマンドを送る関数（GameCommandProvider）と sub.engineer の関数を渡し、
// 接続の状態を出す。round.result を受けたら（ストアに結果を入れてから）onRoundEnd を呼ぶ。
// エンジニアモードを開いている間は、右の島の詳細の代わりにエンジニアパネルを出し、マップを覆わない（T-438）。
import type { ReactElement } from "react";

import { MapView } from "../../game/MapView.tsx";
import { useEngineerMode } from "../../state/engineerMode.ts";
import { DepartureArt } from "../../ui/art/DepartureArt.tsx";
import { GameCommandProvider } from "../../ui/commands/GameCommands.tsx";
import { DemoKeys } from "../../ui/demo/DemoKeys.tsx";
import { EngineerPanel } from "../../ui/engineer/EngineerPanel.tsx";
import { EngineerSubscriptionProvider } from "../../ui/engineer/EngineerSubscription.tsx";
import { FerryPanel } from "../../ui/ferry/FerryPanel.tsx";
import { HudBar } from "../../ui/hud/HudBar.tsx";
import { Notifications } from "../../ui/hud/Notifications.tsx";
import { IslandPanel } from "../../ui/island/IslandPanel.tsx";
import { OpeningCeremony } from "../../ui/opening/index.ts";
import { GameShortcuts } from "../../ui/shortcuts/GameShortcuts.tsx";
import { useGameSounds } from "../../ui/sound/useGameSounds.ts";
import { Link } from "../router/Link.tsx";
import { ReplaySkipButton } from "../replay/ReplaySkipButton.tsx";
import { TutorialOverlay } from "../tutorial/TutorialOverlay.tsx";
import type { RoundContext } from "../types.ts";
import { connectionNotice, mockNotice, replayNotice } from "./connectionText.ts";
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
  // 出航・到着・配達・確定・嵐の予報で効果音を鳴らす（T-434）。リプレイでも同じストアなので鳴る。
  useGameSounds();
  const notice = connectionNotice(view);
  // 待ち時間（接続の準備中・接続中）はマップの中央に出港の絵と出し、それ以外（再接続中・失敗）は隅の札に出す。
  const departing = notice?.departing === true ? notice : null;
  const corner = notice !== null && notice.departing !== true ? notice : null;
  const mock = mockNotice(view);
  const replay = replayNotice(view);
  // リプレイ（ADR-0028 R4）では、チュートリアル（自動の一時停止を送る）とデモの操作を出さず、
  // 「次の出来事まで」のボタンを出す（T-720f）。
  const replaying =
    view.kind !== "offline" && view.kind !== "unavailable" && view.source === "replay";
  const engineerOpen = useEngineerMode();

  return (
    <GameCommandProvider send={send}>
      <EngineerSubscriptionProvider subscribe={subscribe}>
        <div className="game-screen">
          <h1 tabIndex={-1} className="visually-hidden">
            {round.stage.title}
          </h1>
          <section aria-label="状況" className="game-hud">
            <HudBar />
            {/* エンジニアモードの切り替えは HUD の並びの右に置き、マップの内容に重ねない（T-439）。
                パネル自体はアンカー配置でマップの左半分に重ねる（EngineerPanel.css）。 */}
            <EngineerPanel />
          </section>
          <section aria-label="マップ" className="game-map">
            <MapView />
            <section aria-label="お知らせ" aria-live="polite" className="game-notifications">
              <Notifications />
            </section>
            {departing !== null && (
              // 出航を押してからゲームが始まるまで（T-436）。マップの中央に出港の絵と文言を出す。
              <div role="status" className="game-departure">
                <DepartureArt className="game-departure-art" />
                <p>{departing.text}</p>
              </div>
            )}
            {(corner !== null || mock !== null || replay !== null || replaying) && (
              <div className="game-connection">
                {corner !== null && (
                  <div
                    role={corner.tone === "error" ? "alert" : "status"}
                    className={`game-connection-notice game-connection-${corner.tone}`}
                  >
                    <p>{corner.text}</p>
                    {corner.tone === "error" && (
                      <Link to={{ name: "stageSelect" }}>ステージ選択へ</Link>
                    )}
                  </div>
                )}
                {mock !== null && <p className="game-connection-mock">{mock}</p>}
                {replay !== null && <p className="game-connection-mock">{replay}</p>}
                {replaying && <ReplaySkipButton />}
              </div>
            )}
          </section>
          {/* エンジニアモードを開いている間は、この列にエンジニアパネルを置く（T-438。GameScreen.css）。 */}
          {!engineerOpen && (
            <aside aria-label="島の詳細" className="game-island">
              <IslandPanel />
            </aside>
          )}
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
          {!replaying && <DemoKeys />}
          <GameShortcuts />
          <OpeningCeremony />
          {!replaying && <TutorialOverlay stageId={round.stage.id} />}
        </div>
      </EngineerSubscriptionProvider>
    </GameCommandProvider>
  );
}
