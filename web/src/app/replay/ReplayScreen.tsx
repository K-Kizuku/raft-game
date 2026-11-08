// リプレイの画面 /replay（FR-LOG-04・ADR-0028 R4・T-720d・DesignDoc §10.4・§13.6）。
// 入力ログ（FR-LOG-03 でダウンロードした JSON）を選ぶ → WASM（R2）で再シミュレーション → 記録の再生（R3）で
// ゲーム画面（読むだけ）に流す → リザルト。ブラウザの中だけで動き、/api/*・/ws/* を呼ばない。
// 入力ログの中身を console に出さない。
import { useEffect, useState, type ChangeEvent, type ReactElement } from "react";

import { browserTimers, type Timers } from "../../net/index.ts";
import { MAX_INPUT_LOG_BYTES } from "../../replay/limits.ts";
import { replayErrorText } from "../../replay/messages.ts";
import { takeQueuedReplayInput } from "../../replay/pending.ts";
import {
  parseReplayResponse,
  replayFailure,
  type ReplayFailure,
  type ReplayOutcome,
  type ReplaySuccess,
} from "../../replay/response.ts";
import { loadReplayEngine, type ReplayEngine } from "../../replay/wasm.ts";
import { gameStore } from "../../state/index.ts";
import type { RoundConnector } from "../game/connector.ts";
import { GameScreen } from "../game/GameScreen.tsx";
import { ResultScreen } from "../result/ResultScreen.tsx";
import { navigate } from "../router/history.ts";
import type { RoundContext, StageSummary } from "../types.ts";
import { createReplayConnector } from "./replayConnector.ts";
import "./ReplayScreen.css";

export type ReplayScreenProps = {
  loadEngine?: () => Promise<ReplayEngine>; // 既定 loadReplayEngine
  timers?: Timers; // 既定 browserTimers
};

type EngineState =
  { status: "loading" } | { status: "ready"; engine: ReplayEngine } | { status: "failed" };

type Phase =
  | { kind: "choose" }
  | { kind: "pending"; text: string } // 読み込み中・再計算中（WASM の状態で文言を変える）
  | { kind: "playing"; outcome: ReplaySuccess; connector: RoundConnector; round: RoundContext }
  | { kind: "result"; outcome: ReplaySuccess; round: RoundContext }
  | { kind: "error"; failure: ReplayFailure; text: string | null };

const defaultLoadEngine = (): Promise<ReplayEngine> => loadReplayEngine();

const LOADING_TEXT = "リプレイの部品を読み込んでいます（初回だけ、約 2〜3 MB）…";
const COMPUTING_TEXT = "再計算しています…";

// replayStageSummary は記録と StageView から、ゲーム画面とリザルトに渡すステージを作る。
// 画面に出ない欄（region）は空にする。
export function replayStageSummary(outcome: ReplaySuccess): StageSummary {
  const { record, stage } = outcome;
  return {
    id: record.header.stageId,
    title: stage.title,
    region: "",
    islands: stage.islands.length,
    ferries: record.snapshot.ferries.length,
    durationMs: record.header.durationMs,
  };
}

export function ReplayScreen({
  loadEngine = defaultLoadEngine,
  timers = browserTimers,
}: ReplayScreenProps = {}): ReactElement {
  const [engineState, setEngineState] = useState<EngineState>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  // R5 から来たとき（入れた入力ログがあるとき）は、選ぶのを飛ばす。
  const [phase, setPhase] = useState<Phase>(() => {
    const queued = takeQueuedReplayInput();
    return queued === null ? { kind: "choose" } : { kind: "pending", text: queued };
  });

  // 画面を開いたら WASM の読み込みを始める（ファイルを選ぶ間に読む）。
  useEffect(() => {
    let cancelled = false;
    let promise: Promise<ReplayEngine>;
    try {
      promise = loadEngine();
    } catch (error) {
      promise = Promise.reject(error instanceof Error ? error : new Error(String(error)));
    }
    promise.then(
      (engine) => {
        if (!cancelled) {
          setEngineState({ status: "ready", engine });
        }
      },
      () => {
        if (!cancelled) {
          setEngineState({ status: "failed" });
        }
      },
    );
    return () => {
      cancelled = true;
    };
  }, [loadEngine, attempt]);

  // 入力ログがあり WASM が読めたら、「再計算しています…」を 1 回描いてから engine.run を呼ぶ。
  const pendingText = phase.kind === "pending" ? phase.text : null;
  const engine = engineState.status === "ready" ? engineState.engine : null;
  useEffect(() => {
    if (pendingText === null || engine === null) {
      return;
    }
    const id = timers.setTimeout(() => {
      let outcome: ReplayOutcome;
      try {
        outcome = parseReplayResponse(engine.run(pendingText));
      } catch {
        outcome = replayFailure("internal", "raftReplay の呼び出しに失敗しました");
      }
      setPhase(
        outcome.ok
          ? playingOf(outcome, timers)
          : { kind: "error", failure: outcome, text: pendingText },
      );
    }, 0);
    return () => {
      timers.clearTimeout(id);
    };
  }, [pendingText, engine, timers]);

  function reloadEngine(): void {
    setEngineState({ status: "loading" });
    setAttempt((n) => n + 1);
  }

  function chooseAgain(): void {
    gameStore.getState().reset();
    setPhase({ kind: "choose" });
  }

  async function onFile(event: ChangeEvent<HTMLInputElement>): Promise<void> {
    const file = event.currentTarget.files?.[0];
    if (file === undefined) {
      return;
    }
    if (file.size > MAX_INPUT_LOG_BYTES) {
      setPhase({
        kind: "error",
        failure: replayFailure("too_large", "入力ログが 1 MiB を超えています"),
        text: null,
      });
      return;
    }
    let text: string;
    try {
      text = await file.text();
    } catch {
      setPhase({
        kind: "error",
        failure: replayFailure("bad_input", "ファイルを読めませんでした"),
        text: null,
      });
      return;
    }
    setPhase({ kind: "pending", text });
  }

  // WASM を読めなかったら、選ぶ・待つ段でも load_failed を出す。
  if ((phase.kind === "choose" || phase.kind === "pending") && engineState.status === "failed") {
    const text = phase.kind === "pending" ? phase.text : null;
    return renderError(replayFailure("load_failed", "WASM を読み込めませんでした"), () => {
      reloadEngine();
      setPhase(text === null ? { kind: "choose" } : { kind: "pending", text });
    });
  }

  switch (phase.kind) {
    case "choose":
      return (
        <div className="app-screen replay">
          <h1 tabIndex={-1}>リプレイ</h1>
          <p>
            リザルト画面でダウンロードした入力ログ（JSON）を選ぶと、同じゲームをもう一度見られます。ファイルはこの端末の外に送りません。
          </p>
          <label className="replay-file">
            入力ログのファイル
            <input
              type="file"
              accept="application/json,.json"
              onChange={(event) => {
                void onFile(event);
              }}
            />
          </label>
        </div>
      );
    case "pending":
      return (
        <div className="app-screen replay">
          <h1 tabIndex={-1}>リプレイ</h1>
          <p role="status">{engine === null ? LOADING_TEXT : COMPUTING_TEXT}</p>
        </div>
      );
    case "playing": {
      const { outcome } = phase;
      return (
        <GameScreen
          round={phase.round}
          connector={phase.connector}
          onRoundEnd={() => {
            setPhase({ kind: "result", outcome, round: { ...phase.round, finished: true } });
          }}
        />
      );
    }
    case "result": {
      const { outcome } = phase;
      // リザルトと「別の入力ログを選ぶ」を、画面の高さに一緒に収める（T-429・ReplayScreen.css）。
      return (
        <div className="replay-result">
          <ResultScreen
            stage={phase.round.stage}
            nextStage={null}
            onRetry={() => {
              // 同じ outcome で最初から再生する（engine.run をやり直さない。決定的なので結果は同じ）。
              gameStore.getState().reset();
              setPhase(playingOf(outcome, timers));
            }}
            onNextStage={() => {}}
            onStageSelect={() => {
              navigate({ name: "stageSelect" });
            }}
          />
          <div className="app-screen replay-actions">
            <button type="button" className="btn" onClick={chooseAgain}>
              別の入力ログを選ぶ
            </button>
          </div>
        </div>
      );
    }
    case "error": {
      const { failure, text } = phase;
      return renderError(
        failure,
        failure.code === "load_failed"
          ? () => {
              reloadEngine();
              setPhase(text === null ? { kind: "choose" } : { kind: "pending", text });
            }
          : null,
      );
    }
  }

  function renderError(failure: ReplayFailure, onReload: (() => void) | null): ReactElement {
    const message = replayErrorText(failure);
    return (
      <div className="app-screen replay">
        <h1 tabIndex={-1}>リプレイ</h1>
        <div role="alert">
          <p>{message.text}</p>
          {message.detail !== null && <p>{message.detail}</p>}
        </div>
        <div className="replay-actions">
          {onReload !== null && (
            <button type="button" className="btn btn-primary" onClick={onReload}>
              もう一度読み込む
            </button>
          )}
          <button
            type="button"
            className="btn"
            onClick={() => {
              if (failure.code === "load_failed") {
                reloadEngine(); // WASM がなければ何も再生できないので、選び直すときも読み直す
              }
              chooseAgain();
            }}
          >
            別の入力ログを選ぶ
          </button>
        </div>
      </div>
    );
  }
}

function playingOf(outcome: ReplaySuccess, timers: Timers): Phase {
  return {
    kind: "playing",
    outcome,
    connector: createReplayConnector(outcome, { timers }),
    round: { stage: replayStageSummary(outcome), session: null, finished: false },
  };
}
