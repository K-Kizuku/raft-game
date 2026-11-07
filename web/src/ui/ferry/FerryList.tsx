// 船の一覧（T-426）。フェリー操作（FerryPanel）で何も選んでいないときに、船のタブの代わりに出す紙のカードの列。
// 名前・場所・積み荷を出し、押すとその船を選ぶ（ストアの selectFerry。マップで選ぶのと同じ）。
// 中身は ferryTabs（./ferryView.ts）が作る FerryTab で、文言は船のタブと同じ。
// チュートリアル（T-409）が指す ferry-panel__tabs（DesignDoc §13.8）を外枠に付けたままにする。
import type { ReactElement } from "react";

import { gameStore } from "../../state/index.ts";
import { formatRemaining } from "../hud/format.ts";
import type { FerryTab } from "./ferryView.ts";
import "./FerryList.css";

function PlaceText({ tab }: { tab: FerryTab }): ReactElement {
  if (tab.state === "docked") {
    if (tab.placeName === null) return <>停泊中</>;
    return (
      <>
        <span aria-hidden="true">⚓</span>
        {` ${tab.placeName} 停泊中`}
      </>
    );
  }
  if (tab.placeName === null) return <>航行中</>;
  return <>{`→ ${tab.placeName} あと ${formatRemaining(tab.etaMs ?? 0)}`}</>;
}

export function FerryList({ tabs }: { tabs: readonly FerryTab[] }): ReactElement {
  if (tabs.length === 0) {
    return <p className="ferry-panel__note">船がありません。</p>;
  }
  return (
    <div role="group" aria-label="船を選ぶ" className="ferry-list ferry-panel__tabs">
      {tabs.map((tab) => (
        <button
          key={tab.id}
          type="button"
          className="ferry-list__card"
          aria-pressed={tab.selected}
          data-ferry={tab.id}
          onClick={() => {
            gameStore.getState().selectFerry(tab.id);
          }}
        >
          <span className="ferry-list__name ferry-panel__tab-name">
            <span aria-hidden="true">⛴</span>
            {` ${tab.name}`}
          </span>{" "}
          <span className="ferry-list__place">
            <PlaceText tab={tab} />
          </span>{" "}
          <span className="ferry-list__cargo">{`積み荷 ${String(tab.cargoCount)}/${String(tab.capacity)}`}</span>
          {tab.broken && (
            <>
              {" "}
              <span className="ferry-list__broken">
                <span aria-hidden="true">🔧</span> 故障中
              </span>
            </>
          )}
        </button>
      ))}
    </div>
  );
}
