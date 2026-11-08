import { describe, expect, it } from "vitest";

import type { ConnectionState, FailureReason } from "../../net/index.ts";
import { connectionNotice, mockNotice, replayNotice } from "./connectionText.ts";
import type { RoundConnectionView } from "./useRoundConnection.ts";

function connected(
  state: ConnectionState,
  reason: FailureReason | null = null,
  source: "server" | "mock" | "replay" = "server",
): RoundConnectionView {
  return { kind: "connected", source, status: { state, attempt: 1, reason } };
}

const CONNECTING = { text: "サーバーに接続しています…", tone: "info" };

describe("connectionNotice", () => {
  it.each<{ name: string; view: RoundConnectionView; want: unknown }>([
    { name: "offline", view: { kind: "offline" }, want: null },
    { name: "preparing", view: { kind: "preparing", source: "server" }, want: CONNECTING },
    { name: "idle", view: connected("idle"), want: CONNECTING },
    { name: "connecting", view: connected("connecting"), want: CONNECTING },
    { name: "ready", view: connected("ready"), want: null },
    { name: "syncing", view: connected("syncing"), want: null },
    { name: "ended", view: connected("ended"), want: null },
    { name: "closed", view: connected("closed"), want: null },
    {
      name: "reconnecting",
      view: connected("reconnecting"),
      want: { text: "接続が切れました。つなぎ直しています…", tone: "info" },
    },
    {
      name: "failed・unreachable",
      view: connected("failed", "unreachable"),
      want: { text: "サーバーに接続できませんでした。", tone: "error" },
    },
    {
      name: "failed・gave_up",
      view: connected("failed", "gave_up"),
      want: { text: "接続が戻らなかったため、ゲームを続けられません。", tone: "error" },
    },
    {
      name: "failed・replaced",
      view: connected("failed", "replaced"),
      want: {
        text: "ほかのタブでこのゲームを開いたため、この画面の接続を終えました。",
        tone: "error",
      },
    },
    {
      name: "failed・fatal_error",
      view: connected("failed", "fatal_error"),
      want: { text: "サーバーでエラーが起きたため、ゲームを続けられません。", tone: "error" },
    },
    {
      name: "failed・version_mismatch",
      view: connected("failed", "version_mismatch"),
      want: { text: "ゲームが更新されました。ページを読み込み直してください。", tone: "error" },
    },
    {
      name: "unavailable",
      view: { kind: "unavailable" },
      want: { text: "接続の準備に失敗しました。", tone: "error" },
    },
  ])("CT-1 $name", ({ view, want }) => {
    expect(connectionNotice(view)).toEqual(want);
  });
});

describe("mockNotice", () => {
  const TEXT = "開発用の記録を再生しています（スコアなどは仮の値です）。";
  it.each<{ name: string; view: RoundConnectionView; want: string | null }>([
    { name: "mock・preparing", view: { kind: "preparing", source: "mock" }, want: TEXT },
    { name: "mock・connected", view: connected("ready", null, "mock"), want: TEXT },
    { name: "server・preparing", view: { kind: "preparing", source: "server" }, want: null },
    { name: "server・connected", view: connected("ready"), want: null },
    { name: "offline", view: { kind: "offline" }, want: null },
    { name: "unavailable", view: { kind: "unavailable" }, want: null },
    { name: "replay・preparing", view: { kind: "preparing", source: "replay" }, want: null }, // RS-10
    { name: "replay・connected", view: connected("ready", null, "replay"), want: null }, // RS-10
  ])("CT-2 $name", ({ view, want }) => {
    expect(mockNotice(view)).toBe(want);
  });
});

describe("replayNotice", () => {
  const TEXT = "リプレイを再生しています（船の操作はできません）。";
  it.each<{ name: string; view: RoundConnectionView; want: string | null }>([
    { name: "replay・preparing", view: { kind: "preparing", source: "replay" }, want: TEXT },
    { name: "replay・connected", view: connected("ready", null, "replay"), want: TEXT },
    { name: "replay・connected（ended）", view: connected("ended", null, "replay"), want: TEXT },
    { name: "mock・preparing", view: { kind: "preparing", source: "mock" }, want: null },
    { name: "mock・connected", view: connected("ready", null, "mock"), want: null },
    { name: "server・preparing", view: { kind: "preparing", source: "server" }, want: null },
    { name: "server・connected", view: connected("ready"), want: null },
    { name: "offline", view: { kind: "offline" }, want: null },
    { name: "unavailable", view: { kind: "unavailable" }, want: null },
  ])("RS-10 $name", ({ view, want }) => {
    expect(replayNotice(view)).toBe(want);
  });
});
