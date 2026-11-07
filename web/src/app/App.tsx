// 画面の出し分けと遷移（T-401・T-413・DesignDoc §13.6）。
import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useReducer,
  useRef,
  useState,
  type ReactElement,
} from "react";

import { fetchStages } from "./api/stages.ts";
import { CreditsScreen } from "./credits/CreditsScreen.tsx";
import { flowReducer, initialFlowState, nextStageOf } from "./flow.ts";
import { GameScreen } from "./game/GameScreen.tsx";
import { HowToScreen } from "./howto/HowToScreen.tsx";
import { ResultScreen } from "./result/ResultScreen.tsx";
import { navigate, useRoute } from "./router/history.ts";
import { Link } from "./router/Link.tsx";
import { formatRoute, parsePath, type KnownRoute, type Route } from "./router/routes.ts";
import { createSession, type SessionRequest } from "./session/api.ts";
import { TURNSTILE_SITE_KEY } from "./session/config.ts";
import { randomSeed } from "./session/seed.ts";
import { SessionNotice } from "./session/SessionNotice.tsx";
import { createTurnstileClient, type TurnstileClient } from "./session/turnstile.ts";
import { useSessionCreation } from "./session/useSessionCreation.ts";
import { SettingsScreen } from "./settings/SettingsScreen.tsx";
import { StageIntroScreen } from "./stage-intro/StageIntroScreen.tsx";
import { StageSelectScreen } from "./stage-select/StageSelectScreen.tsx";
import { TitleScreen } from "./title/TitleScreen.tsx";
import type { RoundContext, SessionHandle, StageCatalogState, StageSummary } from "./types.ts";
import "../ui/theme/index.ts";
import "./app.css";

export { TITLE } from "./constants.ts";

export type AppDeps = {
  loadStages: (signal: AbortSignal) => Promise<StageSummary[]>;
  postSession: (request: SessionRequest) => Promise<SessionHandle>; // 既定: createSession
  turnstile: TurnstileClient | null; // 既定: TURNSTILE_SITE_KEY があれば createTurnstileClient、なければ null
  newSeed: () => number; // 既定: randomSeed
  allowOffline: boolean; // 既定: import.meta.env.DEV
};

const defaultLoadStages: AppDeps["loadStages"] = (signal) => fetchStages({ signal });
const defaultPostSession: AppDeps["postSession"] = (request) => createSession(request);
const defaultNewSeed: AppDeps["newSeed"] = () => randomSeed();

function resolveDeps(deps: Partial<AppDeps> | undefined): AppDeps {
  let turnstile: TurnstileClient | null;
  if (deps?.turnstile !== undefined) {
    turnstile = deps.turnstile;
  } else {
    turnstile =
      TURNSTILE_SITE_KEY === null ? null : createTurnstileClient({ siteKey: TURNSTILE_SITE_KEY });
  }
  return {
    loadStages: deps?.loadStages ?? defaultLoadStages,
    postSession: deps?.postSession ?? defaultPostSession,
    turnstile,
    newSeed: deps?.newSeed ?? defaultNewSeed,
    allowOffline: deps?.allowOffline ?? import.meta.env.DEV,
  };
}

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

// 今のルートの文字列（作成の前後で画面が移ったかを比べる）。
function currentRouteKey(): string {
  const route = parsePath(window.location.pathname);
  return route.name === "notFound" ? "" : formatRoute(route);
}

// デザインの見本の画面（T-420）。開発のときだけ、URL の `?theme-preview` で開く。
// 本番のビルドでは import.meta.env.DEV が false になり、見本の画面ごと取り除かれる。
const ThemePreview = import.meta.env.DEV
  ? lazy(() => import("../ui/theme/ThemePreview.tsx").then((m) => ({ default: m.ThemePreview })))
  : null;

function wantsThemePreview(): boolean {
  return import.meta.env.DEV && new URLSearchParams(window.location.search).has("theme-preview");
}

export function App(props: { deps?: Partial<AppDeps> } = {}): ReactElement {
  if (ThemePreview !== null && wantsThemePreview()) {
    return (
      <Suspense fallback={null}>
        <ThemePreview />
      </Suspense>
    );
  }
  return <AppRoutes {...props} />;
}

function AppRoutes({ deps }: { deps?: Partial<AppDeps> }): ReactElement {
  const [{ loadStages, postSession, turnstile, newSeed, allowOffline }] = useState(() =>
    resolveDeps(deps),
  );
  const route = useRoute();
  const [flow, dispatch] = useReducer(flowReducer, initialFlowState);
  const [catalog, setCatalog] = useState<StageCatalogState>({ status: "loading" });

  // Turnstile のウィジェットを描く要素。どの画面でも同じ要素（<main> の最後）。
  const hostRef = useRef<HTMLDivElement>(null);
  const getHost = useCallback(() => hostRef.current, []);
  const creation = useSessionCreation({ postSession, turnstile, getHost });
  const { create, clearError } = creation;
  const busy = creation.state.status === "pending";

  // 作成中か（App の側。2 つ目の操作を create に渡さない）と、失敗したときの続き（開発用）。
  const creatingRef = useRef(false);
  const offlineRef = useRef<(() => void) | null>(null);

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

  // ルートが変わったら、作成のエラーの表示と覚えた続きを捨てる（エラーの表示はその画面だけ）。
  useEffect(() => {
    clearError();
    offlineRef.current = null;
  }, [route, clearError]);

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

  // セッションを作り、成功したら onCreated、作成中に画面が移っていたら選び直しのために持つだけ。
  // 失敗したら画面を移さず、開発のときは同じ続きを session: null で行えるように覚える。
  async function startSession(
    stage: StageSummary,
    seed: number,
    onCreated: (session: SessionHandle | null) => void,
  ): Promise<void> {
    if (creatingRef.current) {
      return;
    }
    creatingRef.current = true;
    offlineRef.current = null;
    const startKey = currentRouteKey();
    let session: SessionHandle | null;
    try {
      session = await create({ stageId: stage.id, seed, demo: stage.demo === true });
    } finally {
      creatingRef.current = false;
    }
    const moved = currentRouteKey() !== startKey;
    if (session !== null) {
      if (moved) {
        dispatch({ type: "stageSelected", selection: { stageId: stage.id, session, seed } });
      } else {
        onCreated(session);
      }
      return;
    }
    if (moved) {
      clearError();
      return;
    }
    if (allowOffline) {
      offlineRef.current = () => {
        onCreated(null);
      };
    }
  }

  function reusableSession(stageId: string): boolean {
    return (
      flow.selection !== null &&
      flow.selection.stageId === stageId &&
      flow.selection.session !== null
    );
  }

  function onSelect(stage: StageSummary): void {
    if (reusableSession(stage.id)) {
      navigate({ name: "stageIntro", stageId: stage.id });
      return;
    }
    const seed = newSeed();
    void startSession(stage, seed, (session) => {
      dispatch({ type: "stageSelected", selection: { stageId: stage.id, session, seed } });
      navigate({ name: "stageIntro", stageId: stage.id });
    });
  }

  function onDepart(stage: StageSummary): void {
    if (reusableSession(stage.id)) {
      dispatch({ type: "departed", stage });
      navigate({ name: "game" }, { replace: true });
      return;
    }
    const seed = newSeed();
    void startSession(stage, seed, (session) => {
      dispatch({ type: "stageSelected", selection: { stageId: stage.id, session, seed } });
      dispatch({ type: "departed", stage });
      navigate({ name: "game" }, { replace: true });
    });
  }

  function onRetry(round: RoundContext): void {
    // 1 つのセッションは 1 ラウンドだけなので、必ず作り直す。シードは前のラウンドと同じ。
    const seed = round.seed ?? newSeed();
    void startSession(round.stage, seed, (session) => {
      dispatch({ type: "retried", session, seed });
      navigate({ name: "game" }, { replace: true });
    });
  }

  function onNextStage(stage: StageSummary): void {
    if (reusableSession(stage.id)) {
      navigate({ name: "stageIntro", stageId: stage.id });
      return;
    }
    const seed = newSeed();
    void startSession(stage, seed, (session) => {
      // 先に round を消すと /result の転送が先に走るので、紹介へ移ってから選ぶ。
      navigate({ name: "stageIntro", stageId: stage.id });
      dispatch({ type: "stageSelected", selection: { stageId: stage.id, session, seed } });
    });
  }

  const onOffline = useCallback(() => {
    const next = offlineRef.current;
    offlineRef.current = null;
    clearError();
    next?.();
  }, [clearError]);

  const showNotice =
    !redirecting &&
    (route.name === "stageSelect" || route.name === "stageIntro" || route.name === "result");

  return (
    <main className="app-main" ref={mainRef}>
      {redirecting ? null : renderRoute()}
      {showNotice && (
        <SessionNotice state={creation.state} allowOffline={allowOffline} onOffline={onOffline} />
      )}
      <div className="turnstile-host" ref={hostRef} />
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
        return (
          <StageSelectScreen
            catalog={catalog}
            onRetry={retryCatalog}
            onSelect={onSelect}
            busy={busy}
          />
        );
      case "stageIntro": {
        if (catalog.status !== "ready") {
          return (
            <StageSelectScreen
              catalog={catalog}
              onRetry={retryCatalog}
              onSelect={onSelect}
              busy={busy}
            />
          );
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
        return <StageIntroScreen stage={stage} busy={busy} onDepart={() => onDepart(stage)} />;
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
              onRetry(round);
            }}
            onNextStage={() => {
              if (nextStage !== null) {
                onNextStage(nextStage);
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
