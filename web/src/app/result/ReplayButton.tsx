// リザルトの「このラウンドをリプレイ」（T-720e・FR-LOG-04・ADR-0028 R5）。
// T-714 の fetchInputLog で入力ログを取り出し、R4 の queueReplayInput でメモリの上だけで /replay に渡す。
// URL・sessionStorage・localStorage に入れない。入力ログの中身・トークンを console・例外のメッセージに出さない。
// サーバーなし（session が null）と standalone（inputLogAvailable が false）では何も描かない（InputLogDownload と同じ）。
// /result を履歴に残さないよう、/replay へは replace で移る（DesignDoc §13.6）。
import { useEffect, useId, useRef, useState, type ReactElement } from "react";

import { fetchInputLog } from "../api/inputLog.ts";
import { navigate } from "../router/history.ts";
import type { SessionHandle } from "../types.ts";
import { MAX_INPUT_LOG_BYTES } from "../../replay/limits.ts";
import { queueReplayInput } from "../../replay/pending.ts";

const LABEL = "このラウンドをリプレイ";
const LOADING = "取り出しています…";
const NOT_FOUND = "入力ログを取り出せませんでした（セッションの期限が過ぎた可能性があります）。";
const FAILED = "入力ログを取り出せませんでした。時間をおいてもう一度お試しください。";
const DESCRIPTION =
  "ダウンロードしなくても、この端末の中で同じゲームをもう一度見られます（初回はリプレイの部品を約 2〜3 MB 読み込みます）。";

export function ReplayButton(props: {
  session: SessionHandle | null;
  available: boolean;
  fetchFn?: typeof fetch;
}): ReactElement | null {
  const { session, available, fetchFn } = props;
  const headingId = useId();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // 取得の途中で画面を離れたら結果を捨てる。押した直後の 2 回目も弾く。
  const mounted = useRef(true);
  const busy = useRef(false);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  if (session === null || !available) {
    return null;
  }

  async function onReplay(current: SessionHandle): Promise<void> {
    if (busy.current) {
      return;
    }
    busy.current = true;
    setLoading(true);
    setError(null);
    let failure: string | null = null;
    try {
      const result = await fetchInputLog(current, fetchFn);
      if (!mounted.current) {
        return;
      }
      if (!result.ok) {
        failure = result.reason === "not_found" ? NOT_FOUND : FAILED;
        return;
      }
      if (result.blob.size > MAX_INPUT_LOG_BYTES) {
        failure = FAILED;
        return;
      }
      const text = await result.blob.text();
      if (!mounted.current) {
        return;
      }
      queueReplayInput(text);
      navigate({ name: "replay" }, { replace: true });
    } catch {
      failure = FAILED;
    } finally {
      busy.current = false;
      if (mounted.current && failure !== null) {
        setError(failure);
        setLoading(false);
      }
    }
  }

  return (
    <section className="result-section result-input-log" aria-labelledby={headingId}>
      <h2 id={headingId}>リプレイ</h2>
      <p>
        <button
          type="button"
          className="btn"
          disabled={loading}
          onClick={() => {
            void onReplay(session);
          }}
        >
          {loading ? LOADING : LABEL}
        </button>
      </p>
      <p>{DESCRIPTION}</p>
      {/* 読み上げに気づかせるため、領域は先に置いておく */}
      <p role="status">{error ?? ""}</p>
    </section>
  );
}
