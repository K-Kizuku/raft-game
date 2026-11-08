// セッションの作成の状態（T-413）。画面からの要求 1 回で、セッションは多くても 1 つ（ADR-0018）。
// 作成中の 2 回目の create は何もしない（作成中かどうかは ref で持ち、描画を待たずに判定する）。
import { useCallback, useEffect, useRef, useState } from "react";

import type { SessionHandle } from "../types.ts";
import type { SessionRequest } from "./api.ts";
import { SessionCreateError, type SessionErrorKind } from "./errors.ts";
import type { TurnstileClient } from "./turnstile.ts";

export type SessionCreationState =
  { status: "idle" } | { status: "pending" } | { status: "error"; kind: SessionErrorKind };

export type SessionCreationDeps = {
  postSession: (request: SessionRequest) => Promise<SessionHandle>;
  turnstile: TurnstileClient | null;
  getHost: () => HTMLElement | null; // Turnstile を描く要素
};

export type SessionCreation = {
  state: SessionCreationState;
  create(request: { stageId: string; seed: number; demo: boolean }): Promise<SessionHandle | null>;
  clearError(): void; // error のときだけ idle に戻す
};

const IDLE: SessionCreationState = { status: "idle" };

export function useSessionCreation(deps: SessionCreationDeps): SessionCreation {
  const { postSession, turnstile, getHost } = deps;
  const [state, setState] = useState<SessionCreationState>(IDLE);
  const busyRef = useRef(false);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const update = useCallback((next: SessionCreationState) => {
    if (mountedRef.current) {
      setState(next);
    }
  }, []);

  const create = useCallback(
    async (request: {
      stageId: string;
      seed: number;
      demo: boolean;
    }): Promise<SessionHandle | null> => {
      if (busyRef.current) {
        return null;
      }
      busyRef.current = true;
      update({ status: "pending" });
      try {
        let turnstileToken: string | null = null;
        if (turnstile !== null) {
          const host = getHost();
          try {
            if (host === null) {
              throw new Error("Turnstile を描く要素がありません");
            }
            turnstileToken = await turnstile.getToken(host);
          } catch {
            turnstile.reset();
            update({ status: "error", kind: "challenge_failed" });
            return null;
          }
        }
        let session: SessionHandle;
        try {
          session = await postSession({ ...request, turnstileToken });
        } catch (error) {
          const kind = error instanceof SessionCreateError ? error.kind : "unavailable";
          update({ status: "error", kind });
          return null;
        } finally {
          // トークンは 1 回しか使えないので、成否によらずやり直す。
          turnstile?.reset();
        }
        update(IDLE);
        return session;
      } finally {
        busyRef.current = false;
      }
    },
    [postSession, turnstile, getHost, update],
  );

  const clearError = useCallback(() => {
    setState((current) => (current.status === "error" ? IDLE : current));
  }, []);

  return { state, create, clearError };
}
