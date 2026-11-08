// 接続の状態の文言（T-402b・DesignDoc §13.6）。純粋な関数。
// 文言にトークン・sessionId・URL を入れない。
import type { FailureReason } from "../../net/index.ts";
import type { RoundConnectionView } from "./useRoundConnection.ts";

// departing は、出航を押してからゲームが始まるまでの待ち時間（接続の準備中・接続中）の印。
// 画面は、この間だけ文言に出港の絵を添える（T-436）。
export type ConnectionNotice = Readonly<{
  text: string;
  tone: "info" | "error";
  departing?: true;
}>;

const CONNECTING: ConnectionNotice = {
  text: "出航の準備をしています…",
  tone: "info",
  departing: true,
};

const FAILURE_TEXT: Readonly<Record<FailureReason, string>> = {
  unreachable: "サーバーに接続できませんでした。",
  gave_up: "接続が戻らなかったため、ゲームを続けられません。",
  replaced: "ほかのタブでこのゲームを開いたため、この画面の接続を終えました。",
  fatal_error: "サーバーでエラーが起きたため、ゲームを続けられません。",
  version_mismatch: "ゲームが更新されました。ページを読み込み直してください。",
};

export function connectionNotice(view: RoundConnectionView): ConnectionNotice | null {
  switch (view.kind) {
    case "offline":
      return null;
    case "preparing":
      return CONNECTING;
    case "unavailable":
      return { text: "接続の準備に失敗しました。", tone: "error" };
    case "connected": {
      const { status } = view;
      switch (status.state) {
        case "idle":
        case "connecting":
          return CONNECTING;
        case "reconnecting":
          return { text: "接続が切れました。つなぎ直しています…", tone: "info" };
        case "failed":
          return {
            text: FAILURE_TEXT[status.reason ?? "unreachable"],
            tone: "error",
          };
        case "ready":
        case "syncing":
        case "ended":
        case "closed":
          return null;
      }
    }
  }
}

// mockNotice は記録を再生するモックのときだけ文言を返す。
export function mockNotice(view: RoundConnectionView): string | null {
  if ((view.kind === "preparing" || view.kind === "connected") && view.source === "mock") {
    return "開発用の記録を再生しています（スコアなどは仮の値です）。";
  }
  return null;
}

// replayNotice はリプレイ（ADR-0028 R4）の接続のときだけ文言を返す。
export function replayNotice(view: RoundConnectionView): string | null {
  if ((view.kind === "preparing" || view.kind === "connected") && view.source === "replay") {
    return "リプレイを再生しています（船の操作はできません）。";
  }
  return null;
}
