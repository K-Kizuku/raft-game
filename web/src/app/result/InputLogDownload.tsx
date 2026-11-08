// リザルトの「入力ログをダウンロード」（T-714・FR-LOG-03）。
// 入力ログは Durable Object の SQLite から取り出すので、コンテナが止まった後でも sessionId の期限の間は取れる。
// サーバーなし（session が null）と standalone（inputLogAvailable が false）では何も描かない。
// トークンは画面・console に出さない。
import { useId, useState, type ReactElement } from "react";

import { fetchInputLog } from "../api/inputLog.ts";
import type { SessionHandle } from "../types.ts";

const LABEL = "入力ログをダウンロード（JSON）";
const LOADING = "取り出しています…";
const NOT_FOUND = "入力ログを取り出せませんでした（セッションの期限が過ぎた可能性があります）。";
const FAILED = "入力ログを取り出せませんでした。時間をおいてもう一度お試しください。";
const DESCRIPTION =
  "操作の記録（ステージ・シード・時刻付きの操作）です。開発者向けのツール（simcli）で同じゲームを再現できます。";

// Blob を <a download> で保存する。createObjectURL が無い環境では false。
function saveBlob(blob: Blob, filename: string): boolean {
  if (typeof URL.createObjectURL !== "function") {
    return false;
  }
  const url = URL.createObjectURL(blob);
  try {
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = filename;
    anchor.click();
  } finally {
    URL.revokeObjectURL(url);
  }
  return true;
}

export function InputLogDownload(props: {
  session: SessionHandle | null;
  available: boolean;
  fetchFn?: typeof fetch;
}): ReactElement | null {
  const { session, available, fetchFn } = props;
  const headingId = useId();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (session === null || !available) {
    return null;
  }

  async function onDownload(current: SessionHandle): Promise<void> {
    setLoading(true);
    setError(null);
    try {
      const result = await fetchInputLog(current, fetchFn);
      if (!result.ok) {
        setError(result.reason === "not_found" ? NOT_FOUND : FAILED);
        return;
      }
      if (!saveBlob(result.blob, result.filename)) {
        setError(FAILED);
      }
    } catch {
      setError(FAILED);
    } finally {
      setLoading(false);
    }
  }

  return (
    <section className="result-section result-input-log" aria-labelledby={headingId}>
      <h2 id={headingId}>入力ログ</h2>
      <p>
        <button
          type="button"
          className="btn"
          disabled={loading}
          onClick={() => {
            void onDownload(session);
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
