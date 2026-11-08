// ゲーム画面のスロット「マップ」（T-401 が置き、T-404 が書く / DesignDoc §13.3・§13.6・ADR-0010）。
// データは props で受けず、ストア（T-403）から読む。PixiJS は動的 import で読み込み（最初の bundle に入れない）、
// React の外で毎フレーム gameStore.getState() を読んで描く。ストアに書くのは選択だけ。
import { useEffect, useRef, useState } from "react";
import type { ReactElement } from "react";

import { gameStore, useGameStore } from "../state/index.ts";
import type { GameState, GameStore } from "../state/index.ts";
import { engineerModeStore } from "../state/engineerMode.ts";
import { pressIsland } from "../ui/ferry/islandPress.ts";
import { RaftTerm } from "../ui/terms/RaftTerm.tsx";
import { roleMarkOf } from "./model/scene.ts";
import type { MapHandle } from "./pixi/mapRenderer.ts";
import { PartitionLegend } from "./PartitionLegend.tsx";
import "./MapView.css";

export function MapView(): ReactElement {
  const hasWorld = useGameStore((s) => s.world !== null);
  if (!hasWorld) return <p>マップ（準備中）</p>;
  return <LoadedMap />;
}

// mapStoreOf は、マップに渡すストア。島を選ぶ（selectIsland）ときに、押したこと（pressIsland）も知らせる
// （#318。船を選んでいるときに隣の島を押すと、フェリー操作パネルが行き先にする）。ほかはストアそのまま。
// 状態が変わらなければ同じオブジェクトを返す（毎フレーム getState() を読むため）。
function mapStoreOf(store: GameStore): GameStore {
  let base: GameState | null = null;
  let wrapped: GameState | null = null;
  return {
    ...store,
    getState: () => {
      const state = store.getState();
      if (wrapped === null || state !== base) {
        base = state;
        wrapped = {
          ...state,
          selectIsland: (islandId) => {
            state.selectIsland(islandId);
            if (islandId !== null) pressIsland(islandId);
          },
        };
      }
      return wrapped;
    },
  };
}

// world があるときだけ載せる。外れたら（reset()）アンマウントされ、PixiJS を壊す。
function LoadedMap(): ReactElement {
  const hostRef = useRef<HTMLDivElement>(null);
  // 描けなかったときの理由（null は描けている・描いている途中）。
  const [failed, setFailed] = useState<string | null>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (host === null) return;
    let disposed = false;
    let handle: MapHandle | null = null;
    import("./pixi/mapRenderer.ts")
      .then(({ mountMap }) =>
        mountMap(host, {
          store: mapStoreOf(gameStore),
          now: () => performance.now(),
          engineer: () => engineerModeStore.getState().open,
        }),
      )
      .then(
        (mounted) => {
          // 載せている途中で後始末された（StrictMode の二重の実行・すぐ画面を離れた）ならすぐ壊す。
          if (disposed) mounted.destroy();
          else handle = mounted;
        },
        (error: unknown) => {
          // 失敗の理由は console に出す（#386。開発のときは画面の文の下にも出す）。
          console.error("[map] mountMap failed", error);
          if (!disposed) setFailed(error instanceof Error ? error.message : String(error));
        },
      );
    return () => {
      disposed = true;
      handle?.destroy();
      handle = null;
    };
  }, []);

  return (
    <div className="map-view">
      {failed !== null ? (
        <p role="status" className="map-view__fallback">
          このブラウザではマップを描けませんでした。島はボタンで選べます。
          {import.meta.env.DEV && (
            <>
              <br />
              <small data-testid="map-failure-reason">理由: {failed}</small>
            </>
          )}
        </p>
      ) : (
        <div className="map-view__canvas" aria-hidden="true" ref={hostRef} />
      )}
      <IslandButtons />
      <PartitionLegend />
    </div>
  );
}

// キーボードと支援技術のための島のボタンの列。
function IslandButtons(): ReactElement {
  const stage = useGameStore((s) => s.world?.stage ?? null);
  const nodes = useGameStore((s) => s.world?.nodes ?? null);
  const selectedId = useGameStore((s) => s.selection.islandId);
  const ids = stage === null ? [] : stage.islandIds;
  return (
    <nav aria-label="島を選ぶ" className="map-view__islands">
      {ids.map((id) => {
        const island = stage?.islands[id];
        if (island === undefined) return null;
        const leader = nodes !== null && roleMarkOf(nodes, id) === "leader";
        return (
          <button
            key={id}
            type="button"
            aria-pressed={selectedId === id}
            onClick={() => {
              gameStore.getState().selectIsland(id);
              pressIsland(id);
            }}
          >
            {island.name}
            {leader && (
              <span className="map-view__leader">
                {" "}
                <span aria-hidden="true">👑</span>
                <RaftTerm id="leader" />
              </span>
            )}
          </button>
        );
      })}
    </nav>
  );
}
