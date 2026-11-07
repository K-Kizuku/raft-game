// 画面の出し分けと遷移（T-401・DesignDoc §13.6）。
import { useCallback, useEffect, useReducer, useRef, useState, type ReactElement } from "react";

import { fetchStages } from "./api/stages.ts";
import { CreditsScreen } from "./credits/CreditsScreen.tsx";
import { flowReducer, initialFlowState, nextStageOf } from "./flow.ts";
import { GameScreen } from "./game/GameScreen.tsx";
import { HowToScreen } from "./howto/HowToScreen.tsx";
import { ResultScreen } from "./result/ResultScreen.tsx";
import { navigate, useRoute } from "./router/history.ts";
import { Link } from "./router/Link.tsx";
import type { KnownRoute, Route } from "./router/routes.ts";
import { SettingsScreen } from "./settings/SettingsScreen.tsx";
import { StageIntroScreen } from "./stage-intro/StageIntroScreen.tsx";
import { StageSelectScreen } from "./stage-select/StageSelectScreen.tsx";
import { TitleScreen } from "./title/TitleScreen.tsx";
import type { RoundContext, StageCatalogState, StageSelection, StageSummary } from "./types.ts";
import "./app.css";

export { TITLE } from "./constants.ts";

export type AppDeps = { loadStages: (signal: AbortSignal) => Promise<StageSummary[]> };

const defaultLoadStages: AppDeps["loadStages"] = (signal) => fetchStages({ signal });

// 転送の先。どれも転送しない画面なので、転送は繰り返されない。
function redirectOf(route: Route, round: RoundContext | null): KnownRoute | null {
  switch (route.name) {
    case "notFound":
      return { name: "title" };
    case "game":
      if (round === null) {
        return { name: "stageSelect" };
      }
      return round.finished ? { name: "result" } : null;
    case "result":
      if (round === null) {
        return { name: "stageSelect" };
      }
      return round.finished ? null : { name: "game" };
    default:
      return null;
  }
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

export function App({ deps }: { deps?: Partial<AppDeps> } = {}): ReactElement {
  const [loadStages] = useState(() => deps?.loadStages ?? defaultLoadStages);
  const route = useRoute();
  const [flow, dispatch] = useReducer(flowReducer, initialFlowState);
  const [catalog, setCatalog] = useState<StageCatalogState>({ status: "loading" });

  // ステージの一覧は、ステージ選択・紹介を最初に表示したときに読み込み、以降はキャッシュする。
  const needsCatalog = route.name === "stageSelect" || route.name === "stageIntro";
  useEffect(() => {
    if (!needsCatalog || catalog.status !== "loading") {
      return;
    }
    const controller = new AbortController();
    loadStages(controller.signal).then(
      (stages) => {
        if (!controller.signal.aborted) {
          setCatalog({ status: "ready", stages });
        }
      },
      (error: unknown) => {
        if (!controller.signal.aborted && !isAbortError(error)) {
          setCatalog({ status: "error" });
        }
      },
    );
    return () => {
      controller.abort();
    };
  }, [needsCatalog, catalog.status, loadStages]);

  const retryCatalog = useCallback(() => {
    setCatalog({ status: "loading" });
  }, []);

  // 転送（replace）は描画の中ではなく effect の中で行う。
  const redirect = redirectOf(route, flow.round);
  useEffect(() => {
    const target = redirectOf(route, flow.round);
    if (target !== null) {
      navigate(target, { replace: true });
    }
  }, [route, flow.round]);

  // ルートが変わったら、新しい画面の <h1> にフォーカスを移す。最初の表示では移さない。
  const mainRef = useRef<HTMLElement>(null);
  const shownRouteRef = useRef<Route | null>(null);
  const redirecting = redirect !== null;
  useEffect(() => {
    if (redirecting) {
      return;
    }
    const previous = shownRouteRef.current;
    shownRouteRef.current = route;
    if (previous === null || previous === route) {
      return;
    }
    mainRef.current?.querySelector<HTMLElement>("h1")?.focus();
  }, [route, redirecting]);

  const onSelect = useCallback((selection: StageSelection) => {
    dispatch({ type: "stageSelected", selection });
    navigate({ name: "stageIntro", stageId: selection.stageId });
  }, []);

  return (
    <main className="app-main" ref={mainRef}>
      {redirecting ? null : renderRoute()}
    </main>
  );

  function renderRoute(): ReactElement | null {
    switch (route.name) {
      case "title":
        return <TitleScreen />;
      case "howto":
        return <HowToScreen />;
      case "credits":
        return <CreditsScreen />;
      case "settings":
        return <SettingsScreen />;
      case "stageSelect":
        return <StageSelectScreen catalog={catalog} onRetry={retryCatalog} onSelect={onSelect} />;
      case "stageIntro": {
        if (catalog.status !== "ready") {
          return <StageSelectScreen catalog={catalog} onRetry={retryCatalog} onSelect={onSelect} />;
        }
        const stage = catalog.stages.find((s) => s.id === route.stageId);
        if (stage === undefined) {
          return (
            <div className="app-screen">
              <h1 tabIndex={-1}>ステージが見つかりません。</h1>
              <Link to={{ name: "stageSelect" }}>ステージ選択へ</Link>
            </div>
          );
        }
        return (
          <StageIntroScreen
            stage={stage}
            onDepart={() => {
              dispatch({ type: "departed", stage });
              navigate({ name: "game" }, { replace: true });
            }}
          />
        );
      }
      case "game": {
        const round = flow.round;
        if (round === null) {
          return null;
        }
        return (
          <GameScreen
            round={round}
            onRoundEnd={() => {
              dispatch({ type: "roundEnded" });
              navigate({ name: "result" }, { replace: true });
            }}
          />
        );
      }
      case "result": {
        const round = flow.round;
        if (round === null) {
          return null;
        }
        const nextStage =
          catalog.status === "ready" ? nextStageOf(catalog.stages, round.stage.id) : null;
        return (
          <ResultScreen
            stage={round.stage}
            nextStage={nextStage}
            onRetry={() => {
              dispatch({ type: "retried" });
              navigate({ name: "game" }, { replace: true });
            }}
            onNextStage={() => {
              if (nextStage !== null) {
                navigate({ name: "stageIntro", stageId: nextStage.id });
              }
            }}
            onStageSelect={() => {
              navigate({ name: "stageSelect" });
            }}
          />
        );
      }
      case "notFound":
        return null;
    }
  }
}
