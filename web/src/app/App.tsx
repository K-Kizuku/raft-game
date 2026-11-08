// 画面の出し分けと遷移（T-401・T-413・DesignDoc §13.6）。ステージの解放（T-703・DesignDoc §13.9）。
// ステージ紹介の URL の ?seed= でシードを指定する（T-705・DesignDoc §13.6）。
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

import { gameStore } from "../state/index.ts";
import { fetchStages } from "./api/stages.ts";
import { CreditsScreen } from "./credits/CreditsScreen.tsx";
import { MOCK_ENABLED, withMockStage } from "./dev/mockMode.ts";
import { flowReducer, initialFlowState, nextStageOf } from "./flow.ts";
import { defaultRoundConnector, type RoundConnector } from "./game/connector.ts";
import { GameScreen } from "./game/GameScreen.tsx";
import { HowToScreen } from "./howto/HowToScreen.tsx";
import { LockedStageScreen } from "./progress/LockedStageScreen.tsx";
import { stageProgress, useStageProgress } from "./progress/progress.ts";
import { UnlockNotice } from "./progress/UnlockNotice.tsx";
import { lockStateOf, newlyUnlocked, wantsUnlockAll, type LockState } from "./progress/unlock.ts";
import { ResultScreen } from "./result/ResultScreen.tsx";
import { navigate, useRoute } from "./router/history.ts";
import { Link } from "./router/Link.tsx";
import { formatRoute, parsePath, type KnownRoute, type Route } from "./router/routes.ts";
import { createSession, type SessionRequest } from "./session/api.ts";
import { TURNSTILE_SITE_KEY } from "./session/config.ts";
import { randomSeed } from "./session/seed.ts";
import { SeedNotice } from "./session/SeedNotice.tsx";
import { parseSeedParam, type SeedParam } from "./session/seedParam.ts";
import { SessionNotice } from "./session/SessionNotice.tsx";
import { createTurnstileClient, type TurnstileClient } from "./session/turnstile.ts";
import { useSessionCreation } from "./session/useSessionCreation.ts";
import { SettingsScreen } from "./settings/SettingsScreen.tsx";
import { StageIntroScreen } from "./stage-intro/StageIntroScreen.tsx";
import { StageSelectScreen } from "./stage-select/StageSelectScreen.tsx";
import { TitleScreen } from "./title/TitleScreen.tsx";
import type {
  RoundContext,
  SessionHandle,
  StageCatalogEntry,
  StageCatalogState,
  StageSummary,
} from "./types.ts";
import "../ui/theme/index.ts";
import "./app.css";

export { TITLE } from "./constants.ts";

export type AppDeps = {
  loadStages: (signal: AbortSignal) => Promise<StageCatalogEntry[]>;
  connector: RoundConnector; // 既定 defaultRoundConnector
  postSession: (request: SessionRequest) => Promise<SessionHandle>; // 既定: createSession
  turnstile: TurnstileClient | null; // 既定: TURNSTILE_SITE_KEY があれば createTurnstileClient、なければ null
  newSeed: () => number; // 既定: randomSeed
  allowOffline: boolean; // 既定: import.meta.env.DEV
};

const fetchStageList: AppDeps["loadStages"] = (signal) => fetchStages({ signal });

// 開発（make dev）では、一覧の末尾に記録を再生する開発用のステージを足す。
// 本番のビルドでは MODE の比較が偽に決まり、モックの動的 import ごと消える。
const defaultLoadStages: AppDeps["loadStages"] =
  import.meta.env.MODE === "development" && MOCK_ENABLED
    ? withMockStage(fetchStageList, () =>
        import("./dev/mockRound.ts").then((m) => m.mockStageSummary()),
      )
    : fetchStageList;
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
    connector: deps?.connector ?? defaultRoundConnector,
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

// リプレイの画面（ADR-0028 R4）。ほかの画面の読み込みにリプレイのコードと WASM を入れないよう、遅延して読む。
const ReplayScreen = lazy(() =>
  import("./replay/ReplayScreen.tsx").then((m) => ({ default: m.ReplayScreen })),
);

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
  const [{ loadStages, connector, postSession, turnstile, newSeed, allowOffline }] = useState(() =>
    resolveDeps(deps),
  );
  const route = useRoute();
  const [flow, dispatch] = useReducer(flowReducer, initialFlowState);
  const [catalog, setCatalog] = useState<StageCatalogState>({ status: "loading" });
  const progress = useStageProgress();
  const unlockAll = wantsUnlockAll();
  // 解放の知らせ（初めてのクリアで開いたステージ）。リザルトを離れたら消す。
  const [unlocked, setUnlocked] = useState<StageCatalogEntry[]>([]);

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

  // リザルトを離れたら、解放の知らせを消す（描画の中で前のルートと比べる）。
  const [shownRouteName, setShownRouteName] = useState(route.name);
  if (shownRouteName !== route.name) {
    setShownRouteName(route.name);
    if (route.name !== "result" && unlocked.length > 0) {
      setUnlocked([]);
    }
  }

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

  // seedParam は紹介を描いたときの URL の ?seed=。ok ならそのシードで作り、同じシードの
  // 使っていないセッションだけを使う。none・invalid は今までどおり（新しいシード）。
  function onDepart(stage: StageSummary, seedParam: SeedParam): void {
    const fixedSeed = seedParam.status === "ok" ? seedParam.seed : null;
    if (reusableSession(stage.id) && (fixedSeed === null || flow.selection?.seed === fixedSeed)) {
      // 前のラウンドの world・roundResult を消してから、新しいラウンドを始める。
      gameStore.getState().reset();
      dispatch({ type: "departed", stage });
      navigate({ name: "game" }, { replace: true });
      return;
    }
    const seed = fixedSeed ?? newSeed();
    void startSession(stage, seed, (session) => {
      dispatch({ type: "stageSelected", selection: { stageId: stage.id, session, seed } });
      gameStore.getState().reset();
      dispatch({ type: "departed", stage });
      navigate({ name: "game" }, { replace: true });
    });
  }

  function onRetry(round: RoundContext): void {
    // 1 つのセッションは 1 ラウンドだけなので、必ず作り直す。シードは前のラウンドと同じ。
    const seed = round.seed ?? newSeed();
    void startSession(round.stage, seed, (session) => {
      gameStore.getState().reset();
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

  // ラウンドの結果（サーバーの round.result の★）でクリアを記録する。結果が無ければ記録しない。
  function recordRound(stageId: string): void {
    const result = gameStore.getState().roundResult;
    if (result === null) {
      return;
    }
    const { firstClear } = stageProgress.recordResult(stageId, result.stars);
    if (firstClear && catalog.status === "ready") {
      setUnlocked(newlyUnlocked(stageId, catalog.stages));
    }
  }

  // 一覧が読めていなければ判定できないので、解放済みとして扱う（今の表示のまま）。
  function lockOf(stage: StageCatalogEntry): LockState {
    return catalog.status === "ready"
      ? lockStateOf(stage, catalog.stages, progress, unlockAll)
      : { locked: false };
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
        const lock = lockOf(stage);
        if (lock.locked) {
          return <LockedStageScreen stage={stage} requires={lock.requires} />;
        }
        // ルートが変われば描き直されるので、その時点の URL の値になる。
        const seedParam = parseSeedParam(window.location.search);
        return (
          <>
            <StageIntroScreen
              stage={stage}
              busy={busy}
              onDepart={() => {
                onDepart(stage, seedParam);
              }}
            />
            {seedParam.status !== "none" && <SeedNotice param={seedParam} />}
          </>
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
            connector={connector}
            onRoundEnd={() => {
              recordRound(round.stage.id);
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
        const nextStage = unlockedNextStage(round.stage.id);
        return (
          <>
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
              session={round.session}
            />
            <UnlockNotice stages={unlocked} />
          </>
        );
      }
      case "replay":
        return (
          <Suspense fallback={<p role="status">読み込んでいます…</p>}>
            <ReplayScreen />
          </Suspense>
        );
      case "notFound":
        return null;
    }
  }

  // 一覧の次のステージ。未解放なら null（「次のステージ」を出さない）。
  function unlockedNextStage(stageId: string): StageSummary | null {
    if (catalog.status !== "ready") {
      return null;
    }
    const next = nextStageOf(catalog.stages, stageId);
    const entry = next === null ? undefined : catalog.stages.find((s) => s.id === next.id);
    if (entry === undefined || lockOf(entry).locked) {
      return null;
    }
    return next;
  }
}
